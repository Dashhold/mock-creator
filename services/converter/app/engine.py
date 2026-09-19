"""Conversion engine: turns uploaded bytes into markdown plus structured blocks.

Two engines, chosen by evidence rather than preference:

*geometry* reads a PDF's own text layer with exact character positions. It is
used whenever that layer exists, because the document already states where every
character sits and a layout model can only guess at it. On a two-column paper the
guess interleaves the columns, which silently moves one question's options into
another question's stem.

*docling* runs the full layout and OCR pipeline. It is used for scanned pages,
images, and every format that is not a PDF, and for PDFs whose geometry could not
be resolved.

Mixed documents get both: pages with no text layer are OCR'd and spliced back
into the geometric reading order at the position they occupy in the document.

Nothing here decides what the content means. The engine reports what it did, how
confident it is and what it could not read, and leaves judgement to the caller.
"""

from __future__ import annotations

import importlib
import io
import logging
import time
from dataclasses import dataclass, field
from typing import Any, Callable

from .geometry import SENTINEL_UNREADABLE, extract_pdf_layout, iter_blocks
from .probe import TextProbe, probe_pdf_text
from .settings import settings

log = logging.getLogger("converter.engine")

# --- Docling imports, resolved defensively --------------------------------

from docling.document_converter import DocumentConverter, PdfFormatOption  # noqa: E402
from docling.datamodel.base_models import InputFormat  # noqa: E402
from docling.datamodel.pipeline_options import PdfPipelineOptions  # noqa: E402

try:  # docling >= 2.5x exposes OcrMode; older releases used a boolean flag.
    from docling.datamodel.pipeline_options import OcrMode
except ImportError:  # pragma: no cover - depends on installed version
    OcrMode = None  # type: ignore[assignment]

try:
    from docling.datamodel.pipeline_options import TableStructureOptions
except ImportError:  # pragma: no cover
    TableStructureOptions = None  # type: ignore[assignment]

try:
    from docling.datamodel.pipeline_options import TableFormerMode
except ImportError:  # pragma: no cover
    TableFormerMode = None  # type: ignore[assignment]

# DocumentStream lets us convert from memory, so nothing touches the filesystem.
DocumentStream: Any = None
for _module, _name in (
    ("docling.datamodel.base_models", "DocumentStream"),
    ("docling_core.types.io", "DocumentStream"),
    ("docling.datamodel.document", "DocumentStream"),
):
    try:
        DocumentStream = getattr(__import__(_module, fromlist=[_name]), _name)
        break
    except (ImportError, AttributeError):  # pragma: no cover
        continue
if DocumentStream is None:  # pragma: no cover
    raise RuntimeError("could not locate DocumentStream in the installed docling")

try:
    from docling.version import __version__ as DOCLING_VERSION
except Exception:  # pragma: no cover
    try:
        from importlib.metadata import version as _pkg_version

        DOCLING_VERSION = _pkg_version("docling")
    except Exception:
        DOCLING_VERSION = "unknown"


# OCR engines: the docling option class must exist *and* the runtime package it
# drives must import. Checking only the option class is how a service ends up
# advertising an engine that fails on the first scanned upload.
OCR_RUNTIME_MODULES = {
    "rapidocr": ("rapidocr_onnxruntime", "rapidocr"),
    "easyocr": ("easyocr",),
    "tesseract": ("tesserocr",),
    "tesseract_cli": (),  # driven by the tesseract binary, not a Python module
}
OCR_OPTION_CLASSES = {
    "rapidocr": "RapidOcrOptions",
    "easyocr": "EasyOcrOptions",
    "tesseract": "TesseractOcrOptions",
    "tesseract_cli": "TesseractCliOcrOptions",
}


def _runtime_available(modules: tuple[str, ...]) -> bool:
    if not modules:
        return False
    for name in modules:
        try:
            importlib.import_module(name)
            return True
        except Exception:
            continue
    return False


def _load_ocr_classes() -> dict[str, Any]:
    """Return OCR option classes whose runtime is genuinely installed."""
    import docling.datamodel.pipeline_options as po

    found: dict[str, Any] = {}
    for key, class_name in OCR_OPTION_CLASSES.items():
        cls = getattr(po, class_name, None)
        if cls is None:
            continue
        if not _runtime_available(OCR_RUNTIME_MODULES.get(key, ())):
            log.info("ocr engine %s has no usable runtime, not offering it", key)
            continue
        found[key] = cls
    return found


OCR_CLASSES = _load_ocr_classes()

# Extensions we accept. Docling decides the real parser; this list exists so the
# API can reject obvious junk early and advertise what it supports.
SUPPORTED_EXTENSIONS = {
    "pdf",
    "docx", "doc", "dotx",
    "pptx", "ppt",
    "xlsx", "xls", "csv",
    "html", "htm", "xhtml",
    "md", "markdown", "txt", "text", "rst",
    "adoc", "asciidoc",
    "epub",
    "odt", "ods", "odp",
    "json",
    "png", "jpg", "jpeg", "tif", "tiff", "bmp", "webp", "gif",
}

# Formats we read directly instead of paying for the model pipeline.
PLAIN_TEXT_EXTENSIONS = {"txt", "text", "csv", "rst"}
IMAGE_EXTENSIONS = {"png", "jpg", "jpeg", "tif", "tiff", "bmp", "webp", "gif"}

OCR_MODES = ("off", "auto", "force")
TABLE_MODES = ("fast", "accurate")
ENGINES = ("auto", "geometry", "docling")

# Below this the geometric reading order is not trustworthy enough to ship, and
# the document goes through the layout model instead.
GEOMETRY_CONFIDENCE_FLOOR = 0.55

# Docling labels mapped onto the small vocabulary the Go side stores.
BLOCK_TYPE_MAP = {
    "title": "heading",
    "section_header": "heading",
    "subtitle": "heading",
    "paragraph": "paragraph",
    "text": "paragraph",
    "list_item": "list",
    "table": "table",
    "picture": "picture",
    "caption": "caption",
    "formula": "formula",
    "code": "code",
    "page_header": "page_header",
    "page_footer": "page_footer",
    "footnote": "footnote",
    "document_index": "table",
    "checkbox_selected": "paragraph",
    "checkbox_unselected": "paragraph",
    "form": "paragraph",
    "key_value_region": "paragraph",
    "reference": "paragraph",
}


@dataclass
class ConvertOptions:
    engine: str = "auto"
    ocr_mode: str = settings.default_ocr_mode
    ocr_engine: str = settings.default_ocr_engine
    table_mode: str = settings.default_table_mode
    languages: list[str] = field(default_factory=lambda: list(settings.default_languages))
    max_pages: int = 0
    include_blocks: bool = True

    def normalized(self) -> "ConvertOptions":
        engine = (self.engine or "auto").lower()
        if engine not in ENGINES:
            engine = "auto"
        mode = (self.ocr_mode or "auto").lower()
        if mode not in OCR_MODES:
            mode = "auto"
        ocr_engine = (self.ocr_engine or settings.default_ocr_engine).lower()
        if ocr_engine not in OCR_CLASSES:
            # Fall back to whatever is genuinely installed rather than failing an
            # upload over a configuration typo.
            ocr_engine = next(iter(OCR_CLASSES), "")
        table = (self.table_mode or "accurate").lower()
        if table not in TABLE_MODES:
            table = "accurate"
        return ConvertOptions(
            engine=engine,
            ocr_mode=mode,
            ocr_engine=ocr_engine,
            table_mode=table,
            languages=self.languages or list(settings.default_languages),
            max_pages=max(0, self.max_pages),
            include_blocks=self.include_blocks,
        )


@dataclass
class Block:
    order_index: int
    page_no: int
    block_type: str
    text: str
    level: int = 0

    def as_dict(self) -> dict[str, Any]:
        return {
            "order_index": self.order_index,
            "page_no": self.page_no,
            "block_type": self.block_type,
            "text": self.text,
            "level": self.level,
        }


@dataclass
class ConvertResult:
    markdown: str
    blocks: list[Block]
    metadata: dict[str, Any]

    def as_dict(self) -> dict[str, Any]:
        return {
            "markdown": self.markdown,
            "blocks": [b.as_dict() for b in self.blocks],
            "metadata": self.metadata,
        }


def available_ocr_engines() -> list[str]:
    return sorted(OCR_CLASSES)


# --- docling pipeline -----------------------------------------------------


def _build_ocr_options(engine: str, full_page: bool, languages: list[str]) -> Any | None:
    """Construct OCR options for the installed docling, or None to keep defaults."""
    cls = OCR_CLASSES.get(engine)
    if cls is None:
        return None

    attempts: list[dict[str, Any]] = []
    if full_page and OcrMode is not None:
        attempts.append({"mode": OcrMode.FULL_PAGE, "lang": languages})
        attempts.append({"mode": OcrMode.FULL_PAGE})
    if full_page:
        # Pre-OcrMode releases used a boolean.
        attempts.append({"force_full_page_ocr": True, "lang": languages})
        attempts.append({"force_full_page_ocr": True})
    attempts.append({"lang": languages})
    attempts.append({})

    for kwargs in attempts:
        try:
            return cls(**kwargs)
        except Exception:  # pydantic rejects unknown or mistyped fields
            continue
    return None


def _build_pipeline_options(opts: ConvertOptions, force_ocr: bool) -> PdfPipelineOptions:
    pipeline = PdfPipelineOptions()
    pipeline.do_ocr = opts.ocr_mode != "off"
    pipeline.do_table_structure = True
    pipeline.generate_page_images = False
    pipeline.generate_picture_images = False

    if TableStructureOptions is not None:
        table_opts = TableStructureOptions(do_cell_matching=True)
        if TableFormerMode is not None:
            table_opts.mode = (
                TableFormerMode.ACCURATE if opts.table_mode == "accurate" else TableFormerMode.FAST
            )
        pipeline.table_structure_options = table_opts

    if pipeline.do_ocr:
        ocr_options = _build_ocr_options(opts.ocr_engine, force_ocr, opts.languages)
        if ocr_options is not None:
            pipeline.ocr_options = ocr_options

    for attr, value in (
        ("do_formula_enrichment", settings.enrich_formulas),
        ("do_code_enrichment", settings.enrich_code),
    ):
        if hasattr(pipeline, attr):
            setattr(pipeline, attr, value)

    return pipeline


def _converter_for(pipeline: PdfPipelineOptions) -> DocumentConverter:
    """Build a converter applying the PDF pipeline to PDFs and images alike."""
    format_options: dict[Any, Any] = {InputFormat.PDF: PdfFormatOption(pipeline_options=pipeline)}
    image_format = getattr(InputFormat, "IMAGE", None)
    if image_format is not None:
        try:
            format_options[image_format] = PdfFormatOption(pipeline_options=pipeline)
        except Exception:  # pragma: no cover - option class mismatch
            pass
    return DocumentConverter(format_options=format_options)


def _extract_blocks(document: Any) -> tuple[list[Block], int, int]:
    """Walk a converted docling document and collect labelled blocks."""
    blocks: list[Block] = []
    table_count = 0
    picture_count = 0

    try:
        iterator = document.iterate_items()
    except Exception:  # pragma: no cover
        return blocks, table_count, picture_count

    order = 0
    for entry in iterator:
        item, level = (entry if isinstance(entry, tuple) else (entry, 0))

        label = getattr(item, "label", None)
        label_value = getattr(label, "value", label)
        label_value = str(label_value).lower() if label_value is not None else "paragraph"
        block_type = BLOCK_TYPE_MAP.get(label_value, "paragraph")

        if block_type == "table":
            table_count += 1
        elif block_type == "picture":
            picture_count += 1

        text = getattr(item, "text", None)
        if not text and block_type == "table":
            # Tables carry no plain text; render them so answer-key grids survive.
            for method in ("export_to_markdown", "export_to_html"):
                fn: Callable[..., str] | None = getattr(item, method, None)
                if fn is None:
                    continue
                try:
                    text = fn(document)
                except TypeError:
                    try:
                        text = fn()
                    except Exception:
                        text = None
                except Exception:
                    text = None
                if text:
                    break

        if not text:
            continue

        page_no = 0
        prov = getattr(item, "prov", None)
        if prov:
            page_no = int(getattr(prov[0], "page_no", 0) or 0)

        blocks.append(
            Block(
                order_index=order,
                page_no=page_no,
                block_type=block_type,
                text=str(text).strip(),
                level=int(level or 0),
            )
        )
        order += 1

    return blocks, table_count, picture_count


def _page_count(document: Any) -> int:
    for attr in ("num_pages", "page_count"):
        value = getattr(document, attr, None)
        if callable(value):
            try:
                return int(value())
            except Exception:
                continue
        if isinstance(value, int):
            return value
    pages = getattr(document, "pages", None)
    if pages is not None:
        try:
            return len(pages)
        except Exception:
            return 0
    return 0


def _run_docling(
    data: bytes,
    filename: str,
    opts: ConvertOptions,
    force_ocr: bool,
    page_range: tuple[int, int] | None = None,
) -> tuple[str, list[Block], dict[str, Any]]:
    """Run the docling pipeline once, optionally over a page range."""
    pipeline = _build_pipeline_options(opts, force_ocr)
    converter = _converter_for(pipeline)

    source = DocumentStream(name=filename or "document", stream=io.BytesIO(data))
    kwargs: dict[str, Any] = {}
    if page_range is not None:
        kwargs["page_range"] = page_range
    elif opts.max_pages > 0:
        kwargs["page_range"] = (1, opts.max_pages)

    try:
        result = converter.convert(source, **kwargs)
    except TypeError:
        # Older signatures do not accept page_range.
        result = converter.convert(source)

    document = result.document
    markdown = document.export_to_markdown()
    blocks: list[Block] = []
    table_count = picture_count = 0
    if opts.include_blocks:
        blocks, table_count, picture_count = _extract_blocks(document)

    return markdown, blocks, {
        "page_count": _page_count(document),
        "table_count": table_count,
        "picture_count": picture_count,
        "ocr_applied": bool(pipeline.do_ocr and force_ocr),
        "ocr_full_page": force_ocr,
    }


# --- plain text -----------------------------------------------------------


def _plain_text_result(
    data: bytes, filename: str, extension: str, mime_type: str, started: float
) -> ConvertResult:
    """Read a plain-text format directly, skipping the model pipeline."""
    text = data.decode("utf-8", errors="replace")
    lines = [line.strip() for line in text.splitlines()]
    blocks = [
        Block(order_index=i, page_no=1, block_type="paragraph", text=line)
        for i, line in enumerate(line for line in lines if line)
    ]
    return ConvertResult(
        markdown=text,
        blocks=blocks,
        metadata={
            "filename": filename,
            "extension": extension,
            "mime_type": mime_type,
            "size_bytes": len(data),
            "format": "markdown",
            "engine": "plain-text",
            "engine_reason": "format carries no layout to resolve",
            "engine_version": DOCLING_VERSION,
            "extraction_confidence": 1.0,
            "ocr_engine": "",
            "ocr_mode": "off",
            "ocr_applied": False,
            "page_count": 1,
            "char_count": len(text),
            "table_count": 0,
            "picture_count": 0,
            "text_ratio": 1.0,
            "warnings": [],
            "duration_ms": int((time.perf_counter() - started) * 1000),
        },
    )


# --- routing --------------------------------------------------------------


def _splice_ocr_pages(
    geometry_markdown: str,
    ocr_sections: list[tuple[int, str]],
) -> str:
    """Insert OCR'd page text into geometric output at the right page positions.

    A document can be part born-digital and part scan: a printed paper with a
    photographed answer key, say. Splicing keeps one document in one reading
    order instead of forcing the whole file down the slower, lossier path.
    """
    if not ocr_sections:
        return geometry_markdown

    by_page = dict(ocr_sections)
    out: list[str] = []
    current_page = 0
    for line in geometry_markdown.splitlines():
        stripped = line.strip()
        if stripped.startswith("[page]"):
            digits = stripped[6:].strip()
            if digits.isdigit():
                current_page = int(digits)
                out.append(line)
                if current_page in by_page:
                    out.append("")
                    out.append(by_page.pop(current_page).strip())
                continue
        out.append(line)

    # Any page the geometric pass never emitted at all is appended in order.
    for page_no in sorted(by_page):
        out.append("")
        out.append(f"[page] {page_no}")
        out.append("")
        out.append(by_page[page_no].strip())
    return "\n".join(out)


def _contiguous_ranges(pages: list[int]) -> list[tuple[int, int]]:
    """Group consecutive page numbers into contiguous ranges."""
    ranges: list[tuple[int, int]] = []
    for page in sorted(pages):
        if ranges and page == ranges[-1][1] + 1:
            ranges[-1] = (ranges[-1][0], page)
        else:
            ranges.append((page, page))
    return ranges


def convert(
    data: bytes,
    filename: str,
    extension: str,
    mime_type: str,
    opts: ConvertOptions,
    on_stage: Callable[[str, int], None] | None = None,
) -> ConvertResult:
    """Convert a document to markdown plus structured blocks."""
    opts = opts.normalized()
    started = time.perf_counter()
    extension = extension.lower().lstrip(".")

    def stage(name: str, percent: int) -> None:
        if on_stage is not None:
            on_stage(name, percent)

    if extension in PLAIN_TEXT_EXTENSIONS:
        stage("reading text", 50)
        return _plain_text_result(data, filename, extension, mime_type, started)

    warnings: list[str] = []
    probe = TextProbe()
    geometry = None
    reason = ""

    # --- try the text layer first ----------------------------------------
    if extension == "pdf" and opts.engine in {"auto", "geometry"} and opts.ocr_mode != "force":
        stage("reading text layer", 15)
        geometry = extract_pdf_layout(data, opts.max_pages)
        if not geometry.usable:
            reason = "no usable text layer; " + "; ".join(geometry.warnings[:2])
            geometry = None
        elif geometry.confidence < GEOMETRY_CONFIDENCE_FLOOR:
            reason = (
                f"text layer present but its layout scored {geometry.confidence:.2f}, "
                f"below the {GEOMETRY_CONFIDENCE_FLOOR:.2f} floor"
            )
            warnings.append(reason)
            geometry = None

    if geometry is not None and opts.engine in {"auto", "geometry"}:
        stage("resolving layout", 55)
        markdown = geometry.markdown
        ocr_metadata: dict[str, Any] = {}

        # Pages with no text layer at all are OCR'd and spliced back in.
        glyphless = _glyphless_pages(geometry)
        if glyphless and opts.ocr_mode != "off" and OCR_CLASSES:
            stage("running ocr on scanned pages", 70)
            sections: list[tuple[int, str]] = []
            for first, last in _contiguous_ranges(glyphless):
                try:
                    page_markdown, _, meta = _run_docling(
                        data, filename, opts, force_ocr=True, page_range=(first, last)
                    )
                except Exception as exc:
                    warnings.append(f"OCR of pages {first}-{last} failed: {exc}")
                    continue
                sections.append((first, page_markdown))
                ocr_metadata = meta
            if sections:
                markdown = _splice_ocr_pages(markdown, sections)
                warnings.append(
                    f"{len(glyphless)} page(s) had no text layer and were read with OCR"
                )
        elif glyphless:
            warnings.append(
                f"{len(glyphless)} page(s) have no text layer and OCR is disabled, "
                "so their content is missing"
            )

        blocks = [Block(**b) for b in iter_blocks(geometry.lines)] if opts.include_blocks else []
        unreadable = markdown.count(SENTINEL_UNREADABLE)

        metadata: dict[str, Any] = {
            "filename": filename,
            "extension": extension,
            "mime_type": mime_type,
            "size_bytes": len(data),
            "format": "markdown",
            "engine": "geometry",
            "engine_reason": "PDF carries a usable text layer; read it directly",
            "engine_version": f"pypdfium2/docling-{DOCLING_VERSION}",
            "extraction_confidence": round(geometry.confidence, 4),
            "ocr_engine": opts.ocr_engine if ocr_metadata else "",
            "ocr_mode": opts.ocr_mode,
            "ocr_applied": bool(ocr_metadata),
            "ocr_full_page": bool(ocr_metadata),
            "languages": opts.languages,
            "page_count": geometry.page_count,
            "char_count": len(markdown),
            "table_count": ocr_metadata.get("table_count", 0),
            "picture_count": ocr_metadata.get("picture_count", 0),
            "text_ratio": 1.0,
            "unreadable_chars": unreadable,
            "warnings": warnings + geometry.warnings,
        }
        metadata.update(geometry.as_metadata())
        metadata["duration_ms"] = int((time.perf_counter() - started) * 1000)

        stage("done", 100)
        return ConvertResult(markdown=markdown, blocks=blocks, metadata=metadata)

    if opts.engine == "geometry":
        # An explicit request is not quietly rerouted: the caller asked for the
        # text layer and needs to know it was not there.
        raise ValueError(reason or "geometric extraction is only available for PDFs")

    # --- layout model path ------------------------------------------------
    stage("inspecting document", 10)
    force_ocr = opts.ocr_mode == "force"
    if opts.ocr_mode == "auto":
        if extension in IMAGE_EXTENSIONS:
            force_ocr = True
            probe = TextProbe(page_count=1, chars_per_page=0.0, text_ratio=0.0, sampled_pages=1)
        elif extension == "pdf":
            probe = probe_pdf_text(data, settings.probe_pages)
            force_ocr = probe.chars_per_page < settings.auto_ocr_char_threshold
            log.info(
                "auto ocr: %s chars/page=%.1f threshold=%d -> force=%s",
                filename, probe.chars_per_page, settings.auto_ocr_char_threshold, force_ocr,
            )
    elif extension == "pdf":
        probe = probe_pdf_text(data, settings.probe_pages)

    if force_ocr and not OCR_CLASSES:
        raise RuntimeError(
            "this document needs OCR but no OCR engine is installed in the converter image"
        )

    stage("running ocr" if force_ocr else "converting", 30)
    markdown, blocks, meta = _run_docling(data, filename, opts, force_ocr)
    stage("serializing", 85)

    if reason:
        warnings.append(f"used the layout model because {reason}")

    metadata = {
        "filename": filename,
        "extension": extension,
        "mime_type": mime_type,
        "size_bytes": len(data),
        "format": "markdown",
        "engine": "docling",
        "engine_reason": reason or (
            "OCR was requested" if force_ocr else "format has no readable text layer"
        ),
        "engine_version": DOCLING_VERSION,
        # The layout model infers reading order, so its output is never treated
        # as certain, even when it looks clean.
        "extraction_confidence": 0.7 if not force_ocr else 0.6,
        "ocr_engine": opts.ocr_engine if opts.ocr_mode != "off" else "",
        "ocr_mode": opts.ocr_mode,
        "ocr_applied": meta["ocr_applied"],
        "ocr_full_page": meta["ocr_full_page"],
        "table_mode": opts.table_mode,
        "languages": opts.languages,
        "page_count": meta["page_count"] or probe.page_count,
        "char_count": len(markdown),
        "table_count": meta["table_count"],
        "picture_count": meta["picture_count"],
        "text_ratio": round(probe.text_ratio, 4),
        "probe_chars_per_page": round(probe.chars_per_page, 2),
        "unreadable_chars": markdown.count(SENTINEL_UNREADABLE),
        "warnings": warnings,
        "duration_ms": int((time.perf_counter() - started) * 1000),
    }

    stage("done", 100)
    return ConvertResult(markdown=markdown, blocks=blocks, metadata=metadata)


def _glyphless_pages(geometry: Any) -> list[int]:
    """Page numbers the geometric pass found no characters on."""
    seen: dict[int, int] = {}
    for line in geometry.lines:
        seen[line.page_no] = seen.get(line.page_no, 0) + 1
    return [page for page in range(1, geometry.page_count + 1) if seen.get(page, 0) == 0]


def warm_up() -> None:
    """Run one tiny conversion so model weights load before real traffic."""
    if not settings.warm_models:
        return
    tiny_pdf = (
        b"%PDF-1.4\n"
        b"1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n"
        b"2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n"
        b"3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 200 200]/Contents 4 0 R"
        b"/Resources<</Font<</F1 5 0 R>>>>>>endobj\n"
        b"4 0 obj<</Length 44>>stream\nBT /F1 12 Tf 20 100 Td (warmup) Tj ET\nendstream endobj\n"
        b"5 0 obj<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>endobj\n"
        b"trailer<</Root 1 0 R>>\n"
    )
    try:
        convert(
            tiny_pdf,
            "warmup.pdf",
            "pdf",
            "application/pdf",
            ConvertOptions(engine="docling", ocr_mode="off", include_blocks=False),
        )
        log.info("model warm-up complete")
    except Exception as exc:  # pragma: no cover - warm-up must never be fatal
        log.warning("model warm-up skipped: %s", exc)
