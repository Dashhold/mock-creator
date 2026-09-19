"""Geometric text extraction for PDFs that already carry a text layer.

Why this exists
---------------
A born-digital question paper already states, exactly, where every character
sits. Running a machine-learning layout model over it throws that certainty away
and guesses: on a two-column paper the guess interleaves the columns, so one
question's options land inside another question's stem and some stems vanish
altogether. No downstream parser can recover from that, because the damage is
already baked into the text.

So when a text layer is present we do not guess. We read the character boxes out
of the PDF, find the column gutters as vertical bands that almost no row of text
crosses, and emit each page in true reading order: genuinely full-width rows cut
the page into bands, and within a band the columns are read left to right.

Everything is measured from the document in front of us. There is no expected
column count, no fixed margin, no per-exam special case: column geometry, the
word-break threshold, the body text size, the running headers and the character
encoding repairs are all derived from the page. A scanned document has no text
layer to measure, so it reports itself unusable and the caller runs OCR instead
of shipping something that merely looks plausible.
"""

from __future__ import annotations

import logging
import re
import statistics
import unicodedata
from dataclasses import dataclass, field
from typing import Any, Iterable, Sequence

log = logging.getLogger("converter.geometry")

# --- tuning constants -----------------------------------------------------
#
# Every threshold is a ratio against something measured on the same page, so
# these hold for any page size, any font and any font size. The values were
# calibrated against the gap and height distributions of real question papers;
# the comments record the margin each one has.

# A gap wider than this fraction of the line's median glyph width is a word
# break. Measured: widest within-word gap 0.21x, narrowest real space 0.37x.
# This only applies to documents that do not supply space characters of their
# own; see TRUST_SPACES_RATIO.
SPACE_GAP_RATIO = 0.28
# When a document supplies space characters for at least this share of its
# glyphs, they are authoritative and measured gaps are ignored for ordinary word
# breaks. Tabular figures carry wide side bearings, so inferring breaks from
# geometry in a document that already states them turns "17" into "1 7".
TRUST_SPACES_RATIO = 0.06
# Even when spaces are trusted, a gap this many times the median glyph width is a
# cell boundary and always breaks, so a missing space cannot merge two cells.
CELL_GAP_RATIO = 2.5
# Two glyphs share a line when their vertical extents overlap by at least this
# fraction of the shorter one. Commas and superscripts sit off the baseline but
# still overlap the letters beside them; the next line down does not overlap at
# all.
LINE_OVERLAP_RATIO = 0.25
# A column gutter must be at least this fraction of the page width.
MIN_GUTTER_RATIO = 0.012
# An x position counts as clear when at most this fraction of the page's text
# rows put ink there. Centred titles cross the gutter, so it is never exactly 0.
GUTTER_INK_RATIO = 0.06
# A detected column holding less than this share of the page's glyphs is noise
# and folds into its neighbour.
MIN_COLUMN_SHARE = 0.04
MAX_COLUMNS = 5
# Superscript: clearly shorter than the body text and clearly raised off the
# baseline. The rise test is what keeps ordinary lowercase out.
SUPERSCRIPT_MAX_HEIGHT_RATIO = 0.86
SUPERSCRIPT_MIN_RISE_RATIO = 0.22
# Subscript: shorter still, and its top must stay below the body x-height, which
# is what separates a real subscript from a descender like "g".
SUBSCRIPT_MAX_HEIGHT_RATIO = 0.75
SUBSCRIPT_MAX_TOP_RATIO = 0.5
# Running headers and footers live within this fraction of the page edge.
FURNITURE_BAND_RATIO = 0.08
# A line must repeat near the same edge on this share of pages to be furniture.
FURNITURE_PAGE_SHARE = 0.4
# A centred line indented from both column edges by this much is a heading.
HEADING_INDENT_RATIO = 0.08
HEADING_MAX_WORDS = 12

SUPERSCRIPT_MAP = {
    "0": "\u2070", "1": "\u00b9", "2": "\u00b2", "3": "\u00b3", "4": "\u2074",
    "5": "\u2075", "6": "\u2076", "7": "\u2077", "8": "\u2078", "9": "\u2079",
    "+": "\u207a", "-": "\u207b", "=": "\u207c", "(": "\u207d", ")": "\u207e",
    "n": "\u207f", "i": "\u2071",
}
SUBSCRIPT_MAP = {
    "0": "\u2080", "1": "\u2081", "2": "\u2082", "3": "\u2083", "4": "\u2084",
    "5": "\u2085", "6": "\u2086", "7": "\u2087", "8": "\u2088", "9": "\u2089",
    "+": "\u208a", "-": "\u208b", "=": "\u208c", "(": "\u208d", ")": "\u208e",
}
# Ordinal suffixes are printed raised but read flat: "12th", never "12ᵗʰ", and
# never "12 th".
ORDINAL_SUFFIXES = {"st", "nd", "rd", "th"}

# Only letters and digits are ever treated as raised or lowered text. Punctuation
# is excluded on purpose: a comma hangs below the baseline and a hyphen rides
# above it, so including them turns every "letter-cluster" into "letter⁻cluster"
# and every "questions," into "questions_(,)".
def _scriptable(char: str) -> bool:
    return char.isalnum()

# Characters legacy Indian typesetting fonts use for the rupee sign when the
# font carries no Unicode mapping for it. Substitution is evidence-based; see
# _measure_currency.
RUPEE_CANDIDATES = ("`", "\u00a4", "\u20a8", "~")

# Some producers emit an unmapped glyph as a /gNNN or /uniXXXX reference. That is
# extraction failure, not content, and it has to stay visible.
UNMAPPED_GLYPH_RE = re.compile(r"/(?:g\d{1,5}|uni[0-9A-Fa-f]{4,6}|c\d{1,5})")

# Code points that cannot be text at all. When a maths or symbol font carries no
# ToUnicode entry for a glyph, extractors fall back to whatever the raw code
# happens to be, and fraction bars and radicals arrive as C0 control bytes or as
# Unicode non-characters. Letting those through puts raw control codes in the
# database and prints as nothing on screen, so the defect becomes invisible
# exactly where it matters most. Every one is marked unreadable instead.
UNTEXT_RE = re.compile(
    "["
    "\u0000-\u0008\u000b\u000c\u000e-\u001f"  # C0 controls, keeping tab/LF/CR
    "\u007f-\u009f"                            # DEL and the C1 block
    "\ue000-\uf8ff"                            # private use area
    "\ufdd0-\ufdef"                            # non-characters
    "\ufffe\uffff"                             # non-characters
    "]"
)

# The marker left where a character could not be recovered. Validation treats it
# as a hard failure, so a question containing one can never reach a paper.
SENTINEL_UNREADABLE = "\ufffd"


@dataclass
class Glyph:
    """One character with its exact position on the page."""

    char: str
    x0: float
    y0: float
    x1: float
    y1: float
    # space_before records that the PDF's own content stream put a space
    # character here. When a producer supplies them they are the most reliable
    # word-break signal available; the geometric gap test backs it up.
    space_before: bool = False

    @property
    def cx(self) -> float:
        return (self.x0 + self.x1) / 2.0

    @property
    def width(self) -> float:
        return max(self.x1 - self.x0, 0.0)

    @property
    def height(self) -> float:
        return max(self.y1 - self.y0, 0.0)


@dataclass
class Line:
    """A run of glyphs sharing a baseline inside one column."""

    text: str
    x0: float
    y0: float
    x1: float
    y1: float
    page_no: int
    column: int
    full_width: bool = False
    is_heading: bool = False


@dataclass
class PageLayout:
    page_no: int
    width: float
    height: float
    lines: list[Line]
    column_bounds: list[tuple[float, float]]
    glyph_count: int
    notes: list[str] = field(default_factory=list)


@dataclass
class GeometryResult:
    """Extraction output plus everything needed to judge whether to trust it."""

    markdown: str
    lines: list[Line]
    page_count: int
    glyph_count: int
    # 0..1. Low confidence means the geometry was ambiguous, and the caller
    # should reach for another engine rather than trust this text.
    confidence: float
    columns_per_page: list[int]
    dropped_furniture: list[str]
    repairs: dict[str, int]
    warnings: list[str]
    usable: bool

    def as_metadata(self) -> dict[str, Any]:
        return {
            "geometry_confidence": round(self.confidence, 4),
            "geometry_columns_per_page": self.columns_per_page,
            "geometry_glyphs": self.glyph_count,
            "geometry_dropped_furniture": self.dropped_furniture[:25],
            "geometry_repairs": self.repairs,
            "geometry_warnings": self.warnings,
        }


# --- glyph reading --------------------------------------------------------


def _load_page_glyphs(textpage: Any) -> list[Glyph]:
    """Read every positioned character off one page, in content order."""
    try:
        count = textpage.count_chars()
    except Exception as exc:  # pragma: no cover - corrupt page
        log.debug("count_chars failed: %s", exc)
        return []
    if count <= 0:
        return []

    # One bulk fetch keeps this linear; PDFium indexes the returned string in
    # step with the character indices.
    try:
        text = textpage.get_text_range(0, count)
    except Exception:  # pragma: no cover
        return []

    glyphs: list[Glyph] = []
    pending_space = False

    for index in range(min(count, len(text))):
        char = text[index]
        if char in "\r\n":
            continue
        try:
            left, bottom, right, top = textpage.get_charbox(index, loose=False)
        except Exception:
            continue
        if right < left:
            left, right = right, left
        if top < bottom:
            bottom, top = top, bottom

        if char.isspace():
            # Presence in the character stream is the signal, not the box. Some
            # producers give a space its advance through the next positioning
            # operator instead of the glyph, leaving a zero-width box for a
            # perfectly real space.
            pending_space = True
            continue

        glyphs.append(
            Glyph(char=char, x0=left, y0=bottom, x1=right, y1=top, space_before=pending_space)
        )
        pending_space = False

    return glyphs


# --- line grouping --------------------------------------------------------


def _group_rows(glyphs: Sequence[Glyph], full_page: bool) -> list[list[Glyph]]:
    """Group glyphs into rows of text by vertical overlap.

    Overlap rather than centre distance is what keeps punctuation attached. A
    comma hangs below the baseline, so its centre can sit further from the
    letters beside it than the next line of text does; its extent, however,
    still overlaps those letters, and the next line's does not overlap at all.
    """
    if not glyphs:
        return []

    ordered = sorted(glyphs, key=lambda g: (-g.y1, g.x0))
    rows: list[list[Glyph]] = []
    current: list[Glyph] = []
    low = high = 0.0

    for glyph in ordered:
        if not current:
            current, low, high = [glyph], glyph.y0, glyph.y1
            continue

        overlap = min(high, glyph.y1) - max(low, glyph.y0)
        shorter = min(glyph.height, high - low)
        joins = shorter > 0 and overlap >= shorter * LINE_OVERLAP_RATIO
        if not joins and glyph.height <= 0:
            # A degenerate box joins the row it sits inside.
            joins = low - 0.5 <= glyph.y0 <= high + 0.5

        if joins:
            current.append(glyph)
            low = min(low, glyph.y0)
            high = max(high, glyph.y1)
            continue

        rows.append(current)
        current, low, high = [glyph], glyph.y0, glyph.y1

    if current:
        rows.append(current)

    # Rows used for gutter detection must span the page; rows used for output are
    # already confined to one column, so sorting left to right is enough.
    return [sorted(row, key=lambda g: g.x0) for row in rows] if not full_page else rows


# --- column detection -----------------------------------------------------


def _detect_columns(
    glyphs: Sequence[Glyph], rows: Sequence[Sequence[Glyph]], width: float
) -> list[tuple[float, float]]:
    """Find column x-ranges from vertical bands of the page that carry no ink.

    Counting *rows* rather than characters is what makes this robust. The blank
    strip between two option cells ("1. Chile    2. Peru") is empty on a handful
    of rows; a real column gutter is empty on nearly every row of the page.
    """
    if not glyphs or width <= 0:
        return []

    bins = 240
    bin_width = width / bins
    if bin_width <= 0:
        return []

    inked = [0] * bins
    for row in rows:
        touched: set[int] = set()
        for glyph in row:
            start = max(0, min(bins - 1, int(glyph.x0 / bin_width)))
            end = max(0, min(bins - 1, int(glyph.x1 / bin_width)))
            touched.update(range(start, end + 1))
        for b in touched:
            inked[b] += 1

    threshold = max(1.0, len(rows) * GUTTER_INK_RATIO)
    text_x0 = min(g.x0 for g in glyphs)
    text_x1 = max(g.x1 for g in glyphs)
    first_bin = max(0, min(bins - 1, int(text_x0 / bin_width)))
    last_bin = max(0, min(bins - 1, int(text_x1 / bin_width)))
    min_gutter_bins = max(2, int((width * MIN_GUTTER_RATIO) / bin_width))

    gutters: list[tuple[int, int]] = []
    run_start = -1
    for b in range(first_bin, last_bin + 1):
        clear = inked[b] < threshold
        if clear and run_start < 0:
            run_start = b
        elif not clear and run_start >= 0:
            if b - run_start >= min_gutter_bins and run_start > first_bin:
                gutters.append((run_start, b - 1))
            run_start = -1
    if run_start >= 0 and last_bin - run_start >= min_gutter_bins and run_start > first_bin:
        gutters.append((run_start, last_bin))

    if not gutters:
        return [(text_x0, text_x1)]

    gutters.sort(key=lambda g: g[1] - g[0], reverse=True)
    gutters = sorted(gutters[: MAX_COLUMNS - 1])

    bounds: list[tuple[float, float]] = []
    cursor = text_x0
    for start, end in gutters:
        edge = start * bin_width
        if edge - cursor > width * 0.04:
            bounds.append((cursor, edge))
        cursor = (end + 1) * bin_width
    if text_x1 - cursor > width * 0.04:
        bounds.append((cursor, text_x1))
    if not bounds:
        return [(text_x0, text_x1)]

    total = len(glyphs)
    counts = [0] * len(bounds)
    for glyph in glyphs:
        counts[_column_for(glyph.cx, bounds)] += 1
    kept = [b for b, c in zip(bounds, counts) if c >= total * MIN_COLUMN_SHARE]
    return kept or [(text_x0, text_x1)]


def _column_for(x: float, bounds: Sequence[tuple[float, float]]) -> int:
    """Return the index of the column x falls in, or the nearest one."""
    for i, (left, right) in enumerate(bounds):
        if left <= x <= right:
            return i
    best, best_distance = 0, float("inf")
    for i, (left, right) in enumerate(bounds):
        distance = left - x if x < left else x - right
        if distance < best_distance:
            best, best_distance = i, distance
    return best


def _crosses_gutter(row: Sequence[Glyph], bounds: Sequence[tuple[float, float]]) -> bool:
    """Is this row genuinely full width, i.e. does it put ink inside a gutter?

    An option grid that reaches the right edge of its own column is not full
    width. A centred banner that overlaps the gutter is. Getting this distinction
    right is what stops a section title from being filed inside a question, and
    stops an option row from being glued to the neighbouring column.
    """
    if len(bounds) < 2:
        return False
    for left, right in zip(bounds, bounds[1:]):
        gap_start, gap_end = left[1], right[0]
        if gap_end <= gap_start:
            continue
        for glyph in row:
            if glyph.x1 > gap_start + 0.5 and glyph.x0 < gap_end - 0.5:
                return True
    return False


# --- line rendering -------------------------------------------------------


def _render_line(row: Sequence[Glyph], repairs: dict[str, int], trust_spaces: bool) -> str:
    """Turn one row of glyphs into text, recovering word breaks and scripts."""
    if not row:
        return ""

    widths = [g.width for g in row if g.width > 0]
    heights = [g.height for g in row if g.height > 0]
    if not heights:
        return "".join(g.char for g in row).strip()

    median_width = statistics.median(widths) if widths else 1.0
    body_height = statistics.median(heights)
    body_baseline = statistics.median([g.y0 for g in row])
    # When the document states where its spaces are, only an outright cell gap
    # may add one; otherwise breaks are inferred from the measured gap.
    ratio = CELL_GAP_RATIO if trust_spaces else SPACE_GAP_RATIO
    space_threshold = max(median_width * ratio, 0.1)

    pieces: list[str] = []
    pending: list[Glyph] = []
    pending_kind = ""
    previous: Glyph | None = None

    def flush() -> None:
        nonlocal pending, pending_kind
        if not pending:
            return
        text = "".join(g.char for g in pending)
        table = SUPERSCRIPT_MAP if pending_kind == "super" else SUBSCRIPT_MAP

        if text.isalpha():
            # Raised letters in these documents are ordinal suffixes ("59th") or
            # kerning artefacts that split a word ("co|rr|ect", "Optio|n"), never
            # algebra. Flattening both is right; the count is reported so a real
            # letter exponent losing its raised form is visible in metadata
            # rather than hidden.
            pieces.append(text)
            key = "ordinals_joined" if text.lower() in ORDINAL_SUFFIXES else "scripts_flattened"
            repairs[key] = repairs.get(key, 0) + 1
        elif all(ch in table for ch in text):
            pieces.append("".join(table[ch] for ch in text))
            key = "superscripts" if pending_kind == "super" else "subscripts"
            repairs[key] = repairs.get(key, 0) + 1
        else:
            # A mixed run keeps an explicit marker, so an exponent is never
            # silently merged into the number beside it.
            marker = "^" if pending_kind == "super" else "_"
            pieces.append(f"{marker}({text})")
            repairs["scripts_marked"] = repairs.get("scripts_marked", 0) + 1

        pending = []
        pending_kind = ""

    for glyph in row:
        rise = glyph.y0 - body_baseline
        kind = ""
        if body_height > 0 and _scriptable(glyph.char):
            if (
                glyph.height < body_height * SUPERSCRIPT_MAX_HEIGHT_RATIO
                and rise > body_height * SUPERSCRIPT_MIN_RISE_RATIO
            ):
                kind = "super"
            elif (
                glyph.height < body_height * SUBSCRIPT_MAX_HEIGHT_RATIO
                and rise < -body_height * SUPERSCRIPT_MIN_RISE_RATIO
                and glyph.y1 < body_baseline + body_height * SUBSCRIPT_MAX_TOP_RATIO
            ):
                kind = "sub"

        gap = glyph.x0 - previous.x1 if previous is not None else 0.0
        break_here = previous is not None and (glyph.space_before or gap > space_threshold)

        if kind != pending_kind or break_here:
            flush()
            pending_kind = kind
        if break_here:
            pieces.append(" ")

        if kind:
            pending.append(glyph)
        else:
            pieces.append(glyph.char)
        previous = glyph

    flush()
    return "".join(pieces).strip()


# --- character repair -----------------------------------------------------


def _measure_currency(text: str) -> tuple[str, int]:
    """Decide whether a punctuation character is standing in for a currency sign.

    The test is positional, not a guess about the document's origin: a character
    only becomes a rupee sign when it nearly always sits immediately before an
    amount. The count is reported so the substitution appears in the document's
    metadata rather than happening quietly.
    """
    for candidate in RUPEE_CANDIDATES:
        total = text.count(candidate)
        if total < 3:
            continue
        pattern = re.compile(re.escape(candidate) + r"\s?(?=\d)")
        hits = len(pattern.findall(text))
        if hits >= 3 and hits / total >= 0.8:
            return candidate, hits
    return "", 0


def _mark_unreadable(text: str, repairs: dict[str, int]) -> str:
    """Replace glyphs the PDF never mapped to Unicode with a visible sentinel.

    These come from symbol fonts: arrows, overbars, bespoke maths. Keeping the
    raw "/g148" would read as content; deleting it would hide that part of the
    question is missing. A sentinel lets validation refuse the question outright.
    """
    unmapped = UNMAPPED_GLYPH_RE.findall(text)
    if unmapped:
        text = UNMAPPED_GLYPH_RE.sub(SENTINEL_UNREADABLE, text)
        repairs["unmapped_glyphs"] = repairs.get("unmapped_glyphs", 0) + len(unmapped)
    untext = UNTEXT_RE.findall(text)
    if untext:
        text = UNTEXT_RE.sub(SENTINEL_UNREADABLE, text)
        repairs["private_use_glyphs"] = repairs.get("private_use_glyphs", 0) + len(untext)
    return text


_PUNCTUATION_FIXES = {
    "\u00a0": " ", "\u2007": " ", "\u202f": " ", "\u2009": " ", "\u200a": " ",
    "\u200b": "", "\u200c": "", "\u200d": "", "\ufeff": "",
    "\u2018": "'", "\u2019": "'", "\u201a": "'", "\u201b": "'",
    "\u201c": '"', "\u201d": '"', "\u201e": '"',
    "\u2013": "-", "\u2014": "-", "\u2212": "-", "\u2015": "-",
    "\ufb01": "fi", "\ufb02": "fl", "\ufb00": "ff", "\ufb03": "ffi", "\ufb04": "ffl",
}


def _normalize_text(text: str, repairs: dict[str, int]) -> str:
    """Canonicalise spacing and look-alike punctuation without losing meaning.

    NFC is deliberate: it composes accents and keeps Indic conjuncts intact.
    NFKC would undo the superscripts recovered above and rewrite mathematical
    symbols, so it is never used here.
    """
    text = unicodedata.normalize("NFC", text)
    for src, dst in _PUNCTUATION_FIXES.items():
        if src in text:
            repairs["punctuation_normalised"] = (
                repairs.get("punctuation_normalised", 0) + text.count(src)
            )
            text = text.replace(src, dst)
    return re.sub(r"[ \t]{2,}", " ", text).strip()


# --- page furniture -------------------------------------------------------


def _furniture_key(text: str) -> str:
    """Reduce a line to a form that survives a changing page number."""
    key = re.sub(r"\d+", "#", text.lower())
    key = re.sub(r"[^a-z#]+", " ", key)
    key = " ".join(key.split())
    # A line that was nothing but digits is a page number wherever it appears.
    return key if len(key.replace("#", "").strip()) >= 3 else "#"


def _find_furniture(pages: Sequence[PageLayout]) -> set[str]:
    """Identify running headers and footers by repetition near the page edges.

    Wording alone cannot tell a header from a heading: this paper prints "STAFF
    SELECTION COMMISSION" as a running header, and under the old pipeline that
    string ended up inside a question's options. Repetition at the same edge of
    many pages is the signal that does distinguish them.
    """
    if len(pages) < 3:
        return set()

    seen: dict[str, set[int]] = {}
    for page in pages:
        band = page.height * FURNITURE_BAND_RATIO
        for line in page.lines:
            if not (line.y1 >= page.height - band or line.y0 <= band):
                continue
            seen.setdefault(_furniture_key(line.text), set()).add(page.page_no)

    needed = max(3, int(len(pages) * FURNITURE_PAGE_SHARE))
    return {key for key, pages_seen in seen.items() if len(pages_seen) >= needed}


# --- page assembly --------------------------------------------------------


def _is_centred_heading(line: Line, bounds: Sequence[tuple[float, float]]) -> bool:
    """Is this line a centred title rather than a line of justified body text?

    Size does not separate them in real papers: a section banner is often set at
    the body size in bold. Symmetrical indentation from both column edges does,
    because justified body text fills its column.
    """
    if line.column < 0 or line.column >= len(bounds):
        return False
    left, right = bounds[line.column]
    span = right - left
    if span <= 0:
        return False
    lead, trail = line.x0 - left, right - line.x1
    if lead < span * HEADING_INDENT_RATIO or trail < span * HEADING_INDENT_RATIO:
        return False
    # Roughly equal margins mean centred rather than merely short.
    if abs(lead - trail) > max(lead, trail) * 0.6:
        return False
    text = line.text
    if len(text.split()) > HEADING_MAX_WORDS or text.endswith((".", "?", ":", ";", ",")):
        return False
    return True


def _layout_page(glyphs: list[Glyph], page_no: int, width: float, height: float,
                 repairs: dict[str, int]) -> PageLayout:
    """Order one page's glyphs into reading order."""
    if not glyphs:
        return PageLayout(page_no, width, height, [], [], 0, ["page carries no text layer"])

    page_rows = _group_rows(glyphs, full_page=True)
    bounds = _detect_columns(glyphs, page_rows, width)
    if not bounds:
        return PageLayout(page_no, width, height, [], [], len(glyphs),
                          ["no column structure could be established"])

    supplied_spaces = sum(1 for g in glyphs if g.space_before)
    trust_spaces = supplied_spaces >= len(glyphs) * TRUST_SPACES_RATIO

    out: list[Line] = []

    def emit(row: Sequence[Glyph], column: int, full_width: bool) -> None:
        text = _render_line(sorted(row, key=lambda g: g.x0), repairs, trust_spaces)
        if not text:
            return
        out.append(
            Line(
                text=text,
                x0=min(g.x0 for g in row),
                y0=min(g.y0 for g in row),
                x1=max(g.x1 for g in row),
                y1=max(g.y1 for g in row),
                page_no=page_no,
                column=column,
                full_width=full_width,
            )
        )

    def flush(band_rows: Sequence[Sequence[Glyph]]) -> None:
        """Emit one band: every column of it, left to right, top to bottom."""
        if not band_rows:
            return
        by_column: list[list[Glyph]] = [[] for _ in bounds]
        for row in band_rows:
            for glyph in row:
                by_column[_column_for(glyph.cx, bounds)].append(glyph)
        for column_index, column_glyphs in enumerate(by_column):
            if not column_glyphs:
                continue
            for line_glyphs in _group_rows(column_glyphs, full_page=False):
                emit(line_glyphs, column_index, False)

    # Walk the page top to bottom. A genuinely full-width row closes the band
    # above it and is emitted in its own place, because it sits above everything
    # that follows. One level of this covers the layouts papers actually use: a
    # masthead or a section banner interrupting the columns.
    band: list[Sequence[Glyph]] = []
    for row in page_rows:
        if len(bounds) > 1 and _crosses_gutter(row, bounds):
            flush(band)
            band = []
            emit(row, -1, True)
        else:
            band.append(row)
    flush(band)

    for line in out:
        line.is_heading = line.full_width or _is_centred_heading(line, bounds)

    stacked = _count_stacked_math(out)
    if stacked:
        repairs["stacked_math_rows"] = repairs.get("stacked_math_rows", 0) + stacked

    return PageLayout(page_no, width, height, out, bounds, len(glyphs), [])


def _count_stacked_math(lines: Sequence[Line]) -> int:
    """Count rows that look like half of a two-dimensional expression.

    A stacked fraction puts its numerator and denominator on separate baselines
    a fraction of a line apart. Text extraction is one-dimensional, so those
    rows arrive as two short rows instead of one value, and no amount of
    reordering recovers "2/3" from them.

    Rather than invent a value, this counts the occurrences so the document
    reports that it contains mathematics which did not linearise, and validation
    can hold the affected questions back for review.
    """
    by_column: dict[int, list[Line]] = {}
    for line in lines:
        by_column.setdefault(line.column, []).append(line)

    total = 0
    for column_lines in by_column.values():
        if len(column_lines) < 4:
            continue
        ordered = sorted(column_lines, key=lambda l: -l.y1)
        pitches = [
            a.y0 - b.y0
            for a, b in zip(ordered, ordered[1:])
            if 0 < a.y0 - b.y0 < 100
        ]
        if len(pitches) < 3:
            continue
        pitch = statistics.median(pitches)
        if pitch <= 0:
            continue
        for a, b in zip(ordered, ordered[1:]):
            gap = a.y0 - b.y0
            if not 0 < gap < pitch * 0.6:
                continue
            # Both halves of a stacked expression are short and mostly numeric.
            short = len(a.text) <= 24
            digits = sum(ch.isdigit() for ch in a.text)
            if short and digits and digits >= len(a.text.replace(" ", "")) * 0.5:
                total += 1
    return total


def _confidence(pages: Sequence[PageLayout], repairs: dict[str, int]) -> tuple[float, list[str]]:
    """Score how far the geometry can be trusted, with the reasons spelled out.

    This judges the reliability of the *layout*, not the quality of the content.
    A page with no text layer, or one whose columns could not be separated, drags
    the score down so the caller reaches for OCR instead of shipping this text.
    """
    if not pages:
        return 0.0, ["document produced no pages"]

    warnings: list[str] = []
    score = 1.0

    empty = sum(1 for p in pages if p.glyph_count == 0)
    if empty:
        score -= (empty / len(pages)) * 0.9
        warnings.append(f"{empty} of {len(pages)} pages carry no text layer and need OCR")

    unstructured = sum(1 for p in pages if p.glyph_count > 0 and not p.column_bounds)
    if unstructured:
        score -= (unstructured / len(pages)) * 0.5
        warnings.append(f"{unstructured} pages had no separable column structure")

    text_pages = [p for p in pages if p.glyph_count > 0]
    if text_pages:
        thin = sum(1 for p in text_pages if len(p.lines) < 5)
        if thin:
            score -= (thin / len(text_pages)) * 0.3
            warnings.append(f"{thin} pages yielded fewer than five lines of text")

    total_chars = sum(len(line.text) for p in pages for line in p.lines)
    unreadable = repairs.get("unmapped_glyphs", 0) + repairs.get("private_use_glyphs", 0)
    if total_chars and unreadable / total_chars > 0.001:
        score -= min(0.4, (unreadable / total_chars) * 20)
        warnings.append(
            f"{unreadable} characters carry no Unicode mapping in the source fonts "
            "and are marked unreadable"
        )

    stacked = repairs.get("stacked_math_rows", 0)
    if stacked:
        # This does not make the layout wrong, so it barely moves the score, but
        # it has to be stated: the affected questions need a human to read the
        # original.
        score -= min(0.1, stacked / 400.0)
        warnings.append(
            f"{stacked} rows hold two-dimensional mathematics (stacked fractions or "
            "similar) that cannot be linearised; questions using them are flagged for review"
        )

    return max(0.0, min(1.0, score)), warnings


def extract_pdf_layout(data: bytes, max_pages: int = 0) -> GeometryResult:
    """Read a PDF's text layer in true reading order.

    ``usable`` comes back false when there is not enough of a text layer to work
    with, which is the signal to run OCR instead.
    """
    try:
        import pypdfium2 as pdfium
    except ImportError as exc:  # pragma: no cover - dependency is pinned
        return GeometryResult("", [], 0, 0, 0.0, [], [], {},
                              [f"pypdfium2 unavailable: {exc}"], False)

    try:
        document = pdfium.PdfDocument(data)
    except Exception as exc:
        return GeometryResult("", [], 0, 0, 0.0, [], [], {},
                              [f"PDF could not be opened: {exc}"], False)

    repairs: dict[str, int] = {}
    pages: list[PageLayout] = []

    try:
        total = len(document)
        limit = min(total, max_pages) if max_pages > 0 else total

        for index in range(limit):
            page = document[index]
            try:
                width = float(page.get_width())
                height = float(page.get_height())
                textpage = page.get_textpage()
                try:
                    glyphs = _load_page_glyphs(textpage)
                finally:
                    textpage.close()
                pages.append(_layout_page(glyphs, index + 1, width, height, repairs))
            except Exception as exc:
                log.info("page %d geometry failed: %s", index + 1, exc)
                pages.append(PageLayout(index + 1, 0.0, 0.0, [], [], 0, [str(exc)]))
            finally:
                try:
                    page.close()
                except Exception:
                    pass
    finally:
        try:
            document.close()
        except Exception:
            pass

    # Currency evidence is gathered across the whole document so one page's
    # coincidence cannot trigger a substitution.
    joined = "\n".join(line.text for page in pages for line in page.lines)
    candidate, hits = _measure_currency(joined)
    if candidate:
        repairs["currency_restored"] = hits
        currency_re = re.compile(re.escape(candidate) + r"\s?(?=\d)")
    else:
        currency_re = None

    for page in pages:
        for line in page.lines:
            text = line.text
            if currency_re is not None:
                text = currency_re.sub("\u20b9", text)
            text = _mark_unreadable(text, repairs)
            line.text = _normalize_text(text, repairs)
        page.lines = [line for line in page.lines if line.text]

    furniture = _find_furniture(pages)
    dropped: list[str] = []
    for page in pages:
        band = page.height * FURNITURE_BAND_RATIO
        kept: list[Line] = []
        for line in page.lines:
            at_edge = line.y1 >= page.height - band or line.y0 <= band
            if at_edge and _furniture_key(line.text) in furniture:
                dropped.append(line.text)
                continue
            kept.append(line)
        page.lines = kept

    confidence, warnings = _confidence(pages, repairs)
    glyph_total = sum(p.glyph_count for p in pages)
    text_pages = sum(1 for p in pages if p.glyph_count > 0)

    # Usability is a low bar about the *text layer*, not about quality: enough
    # characters to be worth parsing, on enough of the pages.
    usable = bool(pages) and glyph_total >= 200 and text_pages >= max(1, len(pages) // 2)
    if not usable:
        warnings.append("text layer too sparse for geometric extraction")

    return GeometryResult(
        markdown=_to_markdown(pages),
        lines=[line for page in pages for line in page.lines],
        page_count=len(pages),
        glyph_count=glyph_total,
        confidence=confidence,
        columns_per_page=[max(1, len(p.column_bounds)) for p in pages],
        dropped_furniture=sorted(set(dropped)),
        repairs=repairs,
        warnings=warnings,
        usable=usable,
    )


def _to_markdown(pages: Sequence[PageLayout]) -> str:
    """Emit reading-order text with page markers and headings.

    One source line becomes one output line. Keeping that mapping is what lets
    the parser treat a wrapped stem as a wrapped stem, and lets every stored
    question point back at the page it came from.
    """
    out: list[str] = []
    for page in pages:
        out.append(f"[page] {page.page_no}")
        out.append("")
        previous_column: int | None = None
        for line in page.lines:
            if previous_column is not None and line.column != previous_column:
                # A column or band boundary is a hard break. Without it the last
                # line of one column glues onto the first line of the next, which
                # is exactly how an option ends up holding another question.
                out.append("")
            out.append(f"## {line.text}" if line.is_heading else line.text)
            previous_column = line.column
        out.append("")
    return "\n".join(out)


def iter_blocks(lines: Iterable[Line]) -> list[dict[str, Any]]:
    """Expose lines as the block records the Go side stores for provenance."""
    return [
        {
            "order_index": index,
            "page_no": line.page_no,
            "block_type": "heading" if line.is_heading else "paragraph",
            "text": line.text,
            "level": 0,
        }
        for index, line in enumerate(lines)
    ]
