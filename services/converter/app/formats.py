"""Turn formats Docling cannot read into ones it can, before routing.

Docling reads PDF, OOXML (docx, xlsx, pptx), HTML, Markdown, AsciiDoc, CSV and
the common image formats. Exam material also arrives as legacy Word, Excel and
PowerPoint files, RTF, OpenDocument, EPUB and GIF. Rather than accept those and
fail after the upload, each is turned into its nearest readable equivalent here,
and the step is reported so a reading can always be explained.

This module does not import Docling, so it can be exercised on its own.
"""

from __future__ import annotations

import io
import logging
import os
import posixpath
import re
import shutil
import signal
import subprocess
import tempfile
import zipfile
from dataclasses import dataclass
from pathlib import Path
from urllib.parse import unquote
from xml.etree import ElementTree

from .settings import settings

log = logging.getLogger("converter.formats")

# Legacy and OpenDocument office formats, and the OOXML format each becomes.
# Docling reads OOXML structurally, so this keeps these documents on the same
# path as a modern upload instead of rendering them to images and OCRing them.
OFFICE_TARGETS = {
    "doc": "docx",
    "dot": "docx",
    "rtf": "docx",
    "odt": "docx",
    "xls": "xlsx",
    "ods": "xlsx",
    "ppt": "pptx",
    "odp": "pptx",
}

# Every extension prepare() changes. Anything else passes through untouched.
PREPARED_EXTENSIONS = frozenset(OFFICE_TARGETS) | {"epub", "gif", "markdown"}

HTML_SUFFIXES = (".xhtml", ".html", ".htm")


@dataclass
class Prepared:
    """A document in a form the engine can read, and how it got that way."""

    data: bytes
    filename: str
    extension: str
    # Empty when nothing was done; otherwise an account of the step taken.
    note: str = ""


def _find_soffice() -> str | None:
    for name in ("soffice", "libreoffice"):
        found = shutil.which(name)
        if found:
            return found
    for candidate in ("/usr/lib/libreoffice/program/soffice", "/opt/libreoffice/program/soffice"):
        if os.path.exists(candidate):
            return candidate
    return None


SOFFICE = _find_soffice()


def office_available() -> bool:
    """Whether legacy and OpenDocument office formats can be read here."""
    return SOFFICE is not None


def prepare(data: bytes, filename: str, extension: str) -> Prepared:
    """Return the document in a form the engine can read.

    Formats the engine already reads come back unchanged, with no note.
    """
    ext = extension.lower().lstrip(".")
    if ext in OFFICE_TARGETS:
        return _via_libreoffice(data, filename, ext, settings.office_timeout_seconds)
    if ext == "epub":
        return _epub_to_html(data, filename)
    if ext == "gif":
        return _gif_to_png(data, filename)
    if ext == "markdown":
        # Docling recognises Markdown by the .md extension only.
        return Prepared(data, _with_extension(filename, "md"), "md")
    return Prepared(data, filename, ext)


def _with_extension(filename: str, extension: str) -> str:
    stem = filename.rsplit(".", 1)[0] if "." in filename else filename
    return f"{stem or 'document'}.{extension}"


# --- legacy office formats -------------------------------------------------


def _via_libreoffice(data: bytes, filename: str, ext: str, timeout: int) -> Prepared:
    target = OFFICE_TARGETS[ext]
    if SOFFICE is None:
        raise ValueError(
            f".{ext} files are read by converting them with LibreOffice, "
            "which is not installed in this converter"
        )

    with tempfile.TemporaryDirectory(prefix="office-") as work:
        # A fixed input name, so an unusual filename cannot confuse the command.
        source = os.path.join(work, f"input.{ext}")
        out_dir = os.path.join(work, "out")
        os.makedirs(out_dir)
        with open(source, "wb") as fh:
            fh.write(data)

        # A private profile per call: LibreOffice will not run twice against one
        # profile, and conversions run concurrently.
        profile = Path(work, "profile").as_uri()
        command = [
            SOFFICE,
            "--headless",
            "--norestore",
            "--nolockcheck",
            "--nodefault",
            "--nologo",
            f"-env:UserInstallation={profile}",
            "--convert-to",
            target,
            "--outdir",
            out_dir,
            source,
        ]
        # Its own process group, so a hung conversion can be killed along with
        # the helper processes LibreOffice starts.
        process = subprocess.Popen(
            command,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            start_new_session=hasattr(os, "killpg"),
        )
        try:
            stdout, stderr = process.communicate(timeout=timeout)
        except subprocess.TimeoutExpired as exc:
            if hasattr(os, "killpg"):
                os.killpg(process.pid, signal.SIGKILL)
            else:  # pragma: no cover - local runs on Windows
                process.kill()
            process.communicate()
            raise RuntimeError(
                f"LibreOffice did not finish converting {filename} within {timeout}s"
            ) from exc

        produced = os.path.join(out_dir, f"input.{target}")
        if process.returncode != 0 or not os.path.exists(produced):
            detail = (stderr or stdout or b"").decode("utf-8", "replace").strip()
            raise RuntimeError(
                f"LibreOffice could not convert {filename}: {detail[:300] or 'it produced no output'}"
            )
        with open(produced, "rb") as fh:
            converted = fh.read()

    log.info("converted %s from .%s to .%s with LibreOffice", filename, ext, target)
    return Prepared(
        converted,
        _with_extension(filename, target),
        target,
        note=f"converted from .{ext} to .{target} with LibreOffice before reading",
    )


# --- EPUB -------------------------------------------------------------------

_CONTAINER = "META-INF/container.xml"
_BODY = re.compile(r"<body[^>]*>(.*?)</body\s*>", re.IGNORECASE | re.DOTALL)
_SCRIPT_STYLE = re.compile(r"<(script|style)\b[^>]*>.*?</\1\s*>", re.IGNORECASE | re.DOTALL)
_PROLOGUE = re.compile(r"<\?xml[^>]*\?>|<!DOCTYPE[^>]*>", re.IGNORECASE)


def _epub_to_html(data: bytes, filename: str) -> Prepared:
    """Join an EPUB's chapters, in reading order, into one HTML document.

    An EPUB is a zip of XHTML chapters plus a package file stating their order,
    so this needs no conversion tool: the chapters are HTML already.
    """
    try:
        archive = zipfile.ZipFile(io.BytesIO(data))
    except zipfile.BadZipFile as exc:
        raise ValueError(f"{filename} is not a readable EPUB: it is not a zip archive") from exc

    with archive:
        chapters = [_chapter_body(archive.read(name)) for name in _epub_reading_order(archive)]
    chapters = [chapter for chapter in chapters if chapter.strip()]
    if not chapters:
        raise ValueError(f"{filename} contains no readable chapters")

    html = (
        '<!DOCTYPE html>\n<html><head><meta charset="utf-8"></head><body>\n'
        + "\n<hr/>\n".join(chapters)
        + "\n</body></html>\n"
    )
    return Prepared(
        html.encode("utf-8"),
        _with_extension(filename, "html"),
        "html",
        note=f"EPUB read as HTML: {len(chapters)} chapter(s) joined in reading order",
    )


def _epub_reading_order(archive: zipfile.ZipFile) -> list[str]:
    names = set(archive.namelist())
    package = _epub_package_path(archive, names)
    if package is None:
        # No package file to say otherwise: every HTML file, in name order.
        return sorted(name for name in names if name.lower().endswith(HTML_SUFFIXES))

    try:
        root = ElementTree.fromstring(archive.read(package))
    except ElementTree.ParseError:
        return sorted(name for name in names if name.lower().endswith(HTML_SUFFIXES))

    base = posixpath.dirname(package)
    manifest: dict[str, tuple[str, str]] = {}
    for item in root.iterfind(".//{*}manifest/{*}item"):
        item_id, href = item.get("id"), item.get("href")
        if item_id and href:
            path = posixpath.normpath(posixpath.join(base, unquote(href)))
            manifest[item_id] = (path, item.get("media-type", ""))

    order: list[str] = []
    for ref in root.iterfind(".//{*}spine/{*}itemref"):
        entry = manifest.get(ref.get("idref", ""))
        if entry is None:
            continue
        path, media_type = entry
        if path in names and ("html" in media_type or path.lower().endswith(HTML_SUFFIXES)):
            order.append(path)
    return order


def _epub_package_path(archive: zipfile.ZipFile, names: set[str]) -> str | None:
    if _CONTAINER in names:
        try:
            container = ElementTree.fromstring(archive.read(_CONTAINER))
        except ElementTree.ParseError:
            container = None
        if container is not None:
            rootfile = container.find(".//{*}rootfile")
            path = rootfile.get("full-path") if rootfile is not None else None
            if path and path in names:
                return path
    candidates = sorted(name for name in names if name.lower().endswith(".opf"))
    return candidates[0] if candidates else None


def _chapter_body(raw: bytes) -> str:
    text = raw.decode("utf-8", errors="replace")
    match = _BODY.search(text)
    body = match.group(1) if match else _PROLOGUE.sub("", text)
    return _SCRIPT_STYLE.sub("", body)


# --- GIF --------------------------------------------------------------------


def _gif_to_png(data: bytes, filename: str) -> Prepared:
    """Docling's image pipeline has no GIF reader; its first frame is enough."""
    from PIL import Image  # Pillow ships with Docling; imported here to keep this module light

    try:
        with Image.open(io.BytesIO(data)) as image:
            image.seek(0)
            frame = image.convert("RGB")
    except Exception as exc:
        raise ValueError(f"{filename} is not a readable GIF: {exc}") from exc

    out = io.BytesIO()
    frame.save(out, format="PNG")
    return Prepared(
        out.getvalue(),
        _with_extension(filename, "png"),
        "png",
        note="GIF read as PNG (first frame only)",
    )
