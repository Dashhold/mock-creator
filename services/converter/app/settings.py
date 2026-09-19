"""Runtime configuration for the conversion service, all from the environment."""

from __future__ import annotations

import os
from dataclasses import dataclass, field


def _env(key: str, default: str) -> str:
    value = os.environ.get(key, "").strip()
    return value or default


def _env_int(key: str, default: int) -> int:
    try:
        value = int(_env(key, ""))
    except ValueError:
        return default
    return value if value > 0 else default


def _env_bool(key: str, default: bool) -> bool:
    raw = _env(key, "").lower()
    if raw in {"1", "true", "t", "yes", "y", "on"}:
        return True
    if raw in {"0", "false", "f", "no", "n", "off"}:
        return False
    return default


def _env_list(key: str, default: str) -> list[str]:
    return [part.strip() for part in _env(key, default).split(",") if part.strip()]


@dataclass(frozen=True)
class Settings:
    host: str = field(default_factory=lambda: _env("CONVERTER_HOST", "0.0.0.0"))
    port: int = field(default_factory=lambda: _env_int("CONVERTER_PORT", 5001))

    # Optional shared secret. When set, requests must carry it as X-API-Key or
    # as a bearer token.
    api_key: str = field(default_factory=lambda: _env("CONVERTER_API_KEY", ""))

    # How many documents convert at once. Conversion is CPU and memory hungry,
    # so this stays small; raise it only with matching container memory.
    concurrency: int = field(default_factory=lambda: _env_int("CONVERTER_CONCURRENCY", 2))

    max_upload_bytes: int = field(
        default_factory=lambda: _env_int("CONVERTER_MAX_UPLOAD_BYTES", 128 * 1024 * 1024)
    )
    # Finished tasks are kept this long so a client that polls slowly still gets
    # its result.
    task_ttl_seconds: int = field(default_factory=lambda: _env_int("CONVERTER_TASK_TTL", 3600))

    default_ocr_mode: str = field(default_factory=lambda: _env("CONVERTER_OCR_MODE", "auto"))
    default_ocr_engine: str = field(default_factory=lambda: _env("CONVERTER_OCR_ENGINE", "rapidocr"))
    default_table_mode: str = field(default_factory=lambda: _env("CONVERTER_TABLE_MODE", "accurate"))
    default_languages: list[str] = field(
        default_factory=lambda: _env_list("CONVERTER_OCR_LANGUAGES", "en")
    )

    # Below this many characters per page, a PDF is treated as scanned and the
    # page is sent through OCR even in "auto" mode.
    auto_ocr_char_threshold: int = field(
        default_factory=lambda: _env_int("CONVERTER_AUTO_OCR_CHAR_THRESHOLD", 120)
    )
    # How many pages to sample when measuring the text layer.
    probe_pages: int = field(default_factory=lambda: _env_int("CONVERTER_PROBE_PAGES", 8))

    enrich_formulas: bool = field(
        default_factory=lambda: _env_bool("CONVERTER_ENRICH_FORMULAS", False)
    )
    enrich_code: bool = field(default_factory=lambda: _env_bool("CONVERTER_ENRICH_CODE", False))

    # Warm the layout and table models at startup so the first upload is not
    # dramatically slower than the rest.
    warm_models: bool = field(default_factory=lambda: _env_bool("CONVERTER_WARM_MODELS", True))


settings = Settings()
