"""Measure how much extractable text a PDF already carries.

This is what makes "auto" OCR mode useful. A born-digital paper has a text
layer and running full-page OCR on it is slow and usually worse than reading
the text directly. A scanned paper has almost no text layer, and reading it
without OCR yields nothing. Sampling a few pages tells the two apart in
milliseconds instead of guessing.
"""

from __future__ import annotations

import io
import logging
from dataclasses import dataclass

log = logging.getLogger("converter.probe")


@dataclass
class TextProbe:
    page_count: int = 0
    sampled_pages: int = 0
    total_chars: int = 0
    chars_per_page: float = 0.0
    # text_ratio is a 0..1 score of how well-populated the text layer looks,
    # saturating at 1000 characters per page.
    text_ratio: float = 0.0


def probe_pdf_text(data: bytes, sample_pages: int = 8) -> TextProbe:
    """Sample a PDF's text layer. Returns zeros when the file cannot be read."""
    try:
        from pypdf import PdfReader
    except ImportError:  # pragma: no cover
        return TextProbe()

    try:
        reader = PdfReader(io.BytesIO(data))
        pages = reader.pages
        page_count = len(pages)
    except Exception as exc:
        log.info("text probe failed, assuming no text layer: %s", exc)
        return TextProbe()

    if page_count == 0:
        return TextProbe()

    # Sample evenly across the document so a text-only cover page on a scanned
    # paper cannot make the whole file look born-digital.
    limit = max(1, min(sample_pages, page_count))
    if limit >= page_count:
        indexes = range(page_count)
    else:
        step = page_count / limit
        indexes = [int(i * step) for i in range(limit)]

    total = 0
    sampled = 0
    for index in indexes:
        try:
            text = pages[index].extract_text() or ""
        except Exception:
            text = ""
        total += len(text.strip())
        sampled += 1

    per_page = total / sampled if sampled else 0.0
    return TextProbe(
        page_count=page_count,
        sampled_pages=sampled,
        total_chars=total,
        chars_per_page=per_page,
        text_ratio=min(1.0, per_page / 1000.0),
    )
