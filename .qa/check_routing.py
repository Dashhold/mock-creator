"""Verify engine routing: text layer -> geometry, no text layer -> OCR.

The point of this check is that the decision is made on evidence rather than on a
setting, and that a scan is never silently returned as an empty document.
"""
import sys
import time

sys.path.insert(0, "/app")

from app.engine import ConvertOptions, convert  # noqa: E402
from app.geometry import extract_pdf_layout  # noqa: E402

failures = []


def check(label, ok, detail=""):
    print(("  PASS  " if ok else "  FAIL  ") + label)
    if not ok:
        if detail:
            print("        " + str(detail)[:300])
        failures.append(label)


def load(path):
    with open(path, "rb") as fh:
        return fh.read()


print("=== born-digital PDF ===")
born = load("/tmp/cgl-4.pdf")
started = time.perf_counter()
res = convert(born, "cgl-4.pdf", "pdf", "application/pdf", ConvertOptions(engine="auto"))
elapsed = time.perf_counter() - started
m = res.metadata
print(f"        engine={m['engine']} conf={m['extraction_confidence']} "
      f"chars={m['char_count']} pages={m['page_count']} {elapsed:.1f}s")
check("a text layer routes to the geometric engine", m["engine"] == "geometry", m["engine_reason"])
check("no OCR was run on a born-digital file", m["ocr_applied"] is False)
check("confidence is reported", m["extraction_confidence"] > 0.5)
check("it is fast", elapsed < 20)

print("\n=== the same pages, scanned (no text layer) ===")
scan = load("/tmp/scanned.pdf")

geo = extract_pdf_layout(scan)
check("the geometric engine declines a scan", geo.usable is False,
      f"usable={geo.usable} glyphs={geo.glyph_count}")

started = time.perf_counter()
res = convert(scan, "scanned.pdf", "pdf", "application/pdf", ConvertOptions(engine="auto"))
elapsed = time.perf_counter() - started
m = res.metadata
print(f"        engine={m['engine']} ocr={m['ocr_engine']} applied={m['ocr_applied']} "
      f"chars={m['char_count']} pages={m['page_count']} {elapsed:.1f}s")
check("a scan routes to the layout model", m["engine"] == "docling", m["engine_reason"])
check("OCR actually ran", m["ocr_applied"] is True)
check("OCR recovered real text", m["char_count"] > 1500, f"{m['char_count']} characters")
check("the choice is explained", bool(m["engine_reason"]))

sample = res.markdown[:400].replace("\n", " | ")
print(f"        text: {sample}")

print("\n=== an explicit engine request is honoured, not rerouted ===")
try:
    convert(scan, "scanned.pdf", "pdf", "application/pdf", ConvertOptions(engine="geometry"))
    check("asking for geometry on a scan is refused", False, "it silently succeeded")
except ValueError as exc:
    check("asking for geometry on a scan is refused", True)
    print(f"        refused: {str(exc)[:120]}")
except Exception as exc:
    check("asking for geometry on a scan is refused", False, f"{type(exc).__name__}: {exc}")

print("\n=== forcing the layout model on a born-digital file still works ===")
res = convert(born, "cgl-4.pdf", "pdf", "application/pdf", ConvertOptions(engine="docling"))
m = res.metadata
print(f"        engine={m['engine']} chars={m['char_count']} pages={m['page_count']}")
check("the layout model can be forced", m["engine"] == "docling")
check("it returns usable text", m["char_count"] > 10000, f"{m['char_count']} characters")

print("\n=== result ===")
if failures:
    print(f"{len(failures)} CHECK(S) FAILED")
    for f in failures:
        print("  - " + f)
    sys.exit(1)
print("ALL ROUTING CHECKS PASSED")
