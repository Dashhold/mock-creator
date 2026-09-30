"""Verification harness for the geometric extractor.

Runs over one or more PDFs and reports the signals that matter for content
quality: reading-order sanity, leftover extraction artefacts, and whether the
hard characters survived.
"""

import glob
import re
import sys

sys.path.insert(0, "/app")

from app.geometry import extract_pdf_layout  # noqa: E402

ARTEFACT_RE = re.compile(r"\^\(|_\(|\ufffd|/g\d+|\\{2,}")
QUESTION_RE = re.compile(r"^\s*(\d{1,3})\s*[.)]\s+\S")


def check(path: str, verbose: bool = False) -> None:
    with open(path, "rb") as fh:
        data = fh.read()
    res = extract_pdf_layout(data)
    lines = res.markdown.splitlines()

    # Reading-order sanity: how monotone is the question numbering?
    numbers = []
    for line in lines:
        m = QUESTION_RE.match(line)
        if m:
            numbers.append(int(m.group(1)))
    # Longest strictly increasing run starting from a small number tells us
    # whether the columns were read in order.
    best = run = 0
    previous = 0
    for n in numbers:
        if n == previous + 1:
            run += 1
        else:
            run = 1
        previous = n
        best = max(best, run)

    artefacts = [l for l in lines if ARTEFACT_RE.search(l)]
    name = path.rsplit("/", 1)[-1]
    print(
        f"{name:14s} conf={res.confidence:4.2f} pages={res.page_count:3d} "
        f"cols={max(res.columns_per_page) if res.columns_per_page else 0} "
        f"chars={len(res.markdown):6d} markers={len(numbers):4d} "
        f"longest_run={best:3d} artefacts={len(artefacts):3d}"
    )
    if res.warnings:
        for w in res.warnings:
            print(f"   ! {w}")
    if verbose:
        print("   repairs:", res.repairs)
        for line in artefacts[:10]:
            print("   artefact:", line[:120])


if __name__ == "__main__":
    pattern = sys.argv[1]
    verbose = "-v" in sys.argv
    paths = sorted(glob.glob(pattern))
    if not paths:
        print("no files matched", pattern)
        sys.exit(1)
    for path in paths:
        try:
            check(path, verbose)
        except Exception as exc:
            print(f"{path}: FAILED {type(exc).__name__}: {exc}")
