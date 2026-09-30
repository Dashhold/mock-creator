"""HTTP API for the document conversion service.

Documents arrive as multipart uploads, so the service needs no shared volume
with the API and nothing has to be staged on disk. Two entry points exist: a
synchronous one for small files, and an asynchronous one that returns a task id
to poll, which is what the Go API uses for anything that might need OCR.
"""

from __future__ import annotations

import asyncio
import logging
import os

from fastapi import Depends, FastAPI, File, Form, Header, HTTPException, UploadFile
from fastapi.responses import JSONResponse

from .engine import (
    ENGINES,
    GEOMETRY_CONFIDENCE_FLOOR,
    OCR_MODES,
    SUPPORTED_EXTENSIONS,
    TABLE_MODES,
    ConvertOptions,
    available_ocr_engines,
    convert,
    DOCLING_VERSION,
    warm_up,
)
from .settings import settings
from .tasks import registry

logging.basicConfig(
    level=os.environ.get("CONVERTER_LOG_LEVEL", "INFO").upper(),
    format="%(asctime)s %(levelname)s %(name)s %(message)s",
)
log = logging.getLogger("converter")

app = FastAPI(
    title="Mock Creator Converter",
    version="2.0.0",
    description="Format-agnostic document conversion with selectable OCR, built on Docling.",
)

_ready = {"models": not settings.warm_models}


def require_api_key(
    x_api_key: str | None = Header(default=None, alias="X-API-Key"),
    authorization: str | None = Header(default=None),
) -> None:
    """Enforce the shared secret when one is configured."""
    if not settings.api_key:
        return
    supplied = x_api_key or ""
    if not supplied and authorization:
        parts = authorization.split(None, 1)
        if len(parts) == 2 and parts[0].lower() == "bearer":
            supplied = parts[1]
    if supplied != settings.api_key:
        raise HTTPException(status_code=401, detail="invalid or missing API key")


@app.on_event("startup")
async def on_startup() -> None:
    log.info(
        "converter starting: docling=%s ocr_engines=%s concurrency=%d",
        DOCLING_VERSION, available_ocr_engines(), settings.concurrency,
    )

    async def warm() -> None:
        await asyncio.get_running_loop().run_in_executor(None, warm_up)
        _ready["models"] = True

    # Warm up in the background so the health check can come up immediately.
    asyncio.create_task(warm())


@app.get("/health")
async def health() -> JSONResponse:
    return JSONResponse(
        {
            "status": "ok",
            "engines": list(ENGINES),
            "engine_version": DOCLING_VERSION,
            "models_ready": _ready["models"],
            "ocr_engines": available_ocr_engines(),
            "queue": registry.stats(),
        }
    )


@app.get("/v1/capabilities", dependencies=[Depends(require_api_key)])
async def capabilities() -> dict:
    return {
        "engine": "docling",
        "engine_version": DOCLING_VERSION,
        "extensions": sorted(SUPPORTED_EXTENSIONS),
        "engines": list(ENGINES),
        "ocr_engines": available_ocr_engines(),
        "ocr_modes": list(OCR_MODES),
        "table_modes": list(TABLE_MODES),
        "defaults": {
            "engine": "auto",
            "ocr_mode": settings.default_ocr_mode,
            "ocr_engine": settings.default_ocr_engine,
            "table_mode": settings.default_table_mode,
            "languages": settings.default_languages,
        },
        "geometry_confidence_floor": GEOMETRY_CONFIDENCE_FLOOR,
        "max_upload_bytes": settings.max_upload_bytes,
        "concurrency": settings.concurrency,
        "auto_ocr_char_threshold": settings.auto_ocr_char_threshold,
    }


async def _read_upload(file: UploadFile) -> tuple[bytes, str, str]:
    """Read and validate an upload, returning bytes, filename and extension."""
    filename = os.path.basename(file.filename or "document")
    extension = filename.rsplit(".", 1)[-1].lower() if "." in filename else ""

    if not extension:
        raise HTTPException(status_code=400, detail="file has no extension, cannot determine format")
    if extension not in SUPPORTED_EXTENSIONS:
        raise HTTPException(
            status_code=415,
            detail=f"unsupported format '{extension}'; see /v1/capabilities for the list",
        )

    data = await file.read()
    if not data:
        raise HTTPException(status_code=400, detail="uploaded file is empty")
    if len(data) > settings.max_upload_bytes:
        raise HTTPException(
            status_code=413,
            detail=f"file is {len(data)} bytes, limit is {settings.max_upload_bytes}",
        )
    return data, filename, extension


def _options(engine: str, ocr_mode: str, ocr_engine: str, table_mode: str,
             languages: str, max_pages: int, include_blocks: bool) -> ConvertOptions:
    langs = [part.strip() for part in (languages or "").split(",") if part.strip()]
    return ConvertOptions(
        engine=engine or "auto",
        ocr_mode=ocr_mode or settings.default_ocr_mode,
        ocr_engine=ocr_engine or settings.default_ocr_engine,
        table_mode=table_mode or settings.default_table_mode,
        languages=langs or list(settings.default_languages),
        max_pages=max_pages or 0,
        include_blocks=include_blocks,
    ).normalized()


@app.post("/v1/convert", dependencies=[Depends(require_api_key)])
async def convert_sync(
    file: UploadFile = File(...),
    engine: str = Form(""),
    ocr_mode: str = Form(""),
    ocr_engine: str = Form(""),
    table_mode: str = Form(""),
    languages: str = Form(""),
    max_pages: int = Form(0),
    include_blocks: bool = Form(True),
) -> dict:
    """Convert a document and return the result in the same request."""
    data, filename, extension = await _read_upload(file)
    opts = _options(engine, ocr_mode, ocr_engine, table_mode, languages, max_pages, include_blocks)

    loop = asyncio.get_running_loop()
    try:
        result = await loop.run_in_executor(
            None,
            lambda: convert(data, filename, extension, file.content_type or "", opts),
        )
    except ValueError as exc:
        # A requested engine that cannot serve this document is the caller's
        # problem to resolve, not a server fault.
        raise HTTPException(status_code=422, detail=str(exc)) from exc
    except Exception as exc:
        log.exception("synchronous conversion failed for %s", filename)
        raise HTTPException(status_code=500, detail=f"{type(exc).__name__}: {exc}") from exc

    return result.as_dict()


@app.post("/v1/convert/async", dependencies=[Depends(require_api_key)], status_code=202)
async def convert_async(
    file: UploadFile = File(...),
    engine: str = Form(""),
    ocr_mode: str = Form(""),
    ocr_engine: str = Form(""),
    table_mode: str = Form(""),
    languages: str = Form(""),
    max_pages: int = Form(0),
    include_blocks: bool = Form(True),
) -> dict:
    """Queue a conversion and return a task id to poll."""
    data, filename, extension = await _read_upload(file)
    opts = _options(engine, ocr_mode, ocr_engine, table_mode, languages, max_pages, include_blocks)
    task = registry.submit(data, filename, extension, file.content_type or "", opts)
    return task.as_dict(include_result=False)


@app.get("/v1/tasks/{task_id}", dependencies=[Depends(require_api_key)])
async def task_status(task_id: str, include_result: bool = True) -> dict:
    task = registry.get(task_id)
    if task is None:
        raise HTTPException(status_code=404, detail="unknown task")
    return task.as_dict(include_result=include_result)


@app.delete("/v1/tasks/{task_id}", dependencies=[Depends(require_api_key)])
async def cancel_task(task_id: str) -> dict:
    if not registry.cancel(task_id):
        raise HTTPException(status_code=404, detail="unknown task")
    return {"task_id": task_id, "cancelled": True}
