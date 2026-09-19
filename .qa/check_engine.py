"""Verification harness for engine routing."""
import sys

sys.path.insert(0, "/app")

from app.engine import ConvertOptions, available_ocr_engines, convert  # noqa: E402

print("ocr engines available:", available_ocr_engines())

with open("/tmp/cgl-1.pdf", "rb") as fh:
    data = fh.read()

for engine in ("auto", "geometry"):
    res = convert(data, "cgl-1.pdf", "pdf", "application/pdf",
                  ConvertOptions(engine=engine, include_blocks=True))
    m = res.metadata
    print(
        f"\nengine={engine!r:10s} -> used={m['engine']!r} "
        f"conf={m['extraction_confidence']} pages={m['page_count']} "
        f"chars={m['char_count']} blocks={len(res.blocks)} ms={m['duration_ms']}"
    )
    print("  reason:", m["engine_reason"])
    for w in m.get("warnings", [])[:4]:
        print("  warn  :", w)

# A file with no text layer at all must be refused by the geometry engine
# rather than quietly returning nothing.
fake = b"%PDF-1.4\ntrailer<</Root 1 0 R>>\n"
try:
    convert(fake, "broken.pdf", "pdf", "application/pdf", ConvertOptions(engine="geometry"))
    print("\nbroken pdf + engine=geometry: NO ERROR (unexpected)")
except ValueError as exc:
    print("\nbroken pdf + engine=geometry: refused ->", str(exc)[:110])
except Exception as exc:
    print("\nbroken pdf + engine=geometry:", type(exc).__name__, str(exc)[:110])
