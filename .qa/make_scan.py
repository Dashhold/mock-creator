"""Build a scanned version of a PDF: render pages to images, then wrap them back
into a PDF with no text layer at all.

This is how the OCR path gets tested against real content rather than a synthetic
file: the same questions, with the text layer removed, so the geometric engine
must decline and the pipeline must fall through to OCR.
"""

import sys

import pypdfium2 as pdfium
from PIL import Image

src, dst = sys.argv[1], sys.argv[2]
pages = int(sys.argv[3]) if len(sys.argv) > 3 else 3
scale = float(sys.argv[4]) if len(sys.argv) > 4 else 2.0

doc = pdfium.PdfDocument(src)
images = []
for index in range(min(pages, len(doc))):
    page = doc[index]
    bitmap = page.render(scale=scale)
    images.append(bitmap.to_pil().convert("RGB"))
    page.close()
doc.close()

if not images:
    raise SystemExit("no pages rendered")

images[0].save(dst, "PDF", save_all=True, append_images=images[1:], resolution=72 * scale)
print(f"{src} -> {dst}: {len(images)} page(s) as images at {scale}x")

# Confirm the result really has no text layer.
check = pdfium.PdfDocument(dst)
total = 0
for i in range(len(check)):
    page = check[i]
    tp = page.get_textpage()
    total += tp.count_chars()
    tp.close()
    page.close()
check.close()
print(f"characters in the text layer of the output: {total}")
