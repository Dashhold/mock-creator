"""Dump geometric extraction for every PDF in a directory."""
import glob
import os
import sys

sys.path.insert(0, "/app")

from app.geometry import extract_pdf_layout  # noqa: E402

src_glob, out_dir = sys.argv[1], sys.argv[2]
os.makedirs(out_dir, exist_ok=True)

for path in sorted(glob.glob(src_glob)):
    name = os.path.basename(path).rsplit(".", 1)[0]
    with open(path, "rb") as fh:
        res = extract_pdf_layout(fh.read())
    dst = os.path.join(out_dir, name + ".md")
    with open(dst, "w", encoding="utf-8") as fh:
        fh.write(res.markdown)
    print(f"{name:12s} conf={res.confidence:.3f} chars={len(res.markdown):6d}")
