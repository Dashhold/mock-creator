"""Diagnostic: show the exact characters the mojibake check is matching.

Mirrors the Go detector so a false positive can be told from a real encoding
fault by looking at the code points.
"""
import glob
import sys
import unicodedata

LEADS = set("ÃÂâÎÐÑÒÓÔÕØÞàáãå") | {"\u0393"}


def is_lead(ch):
    if ch in LEADS:
        return True
    return "\u2500" <= ch <= "\u259f"


def is_follow(ch):
    return (
        "\u0080" <= ch <= "\u00ff"
        or "\u2000" <= ch <= "\u206f"
        or "\u20a0" <= ch <= "\u20bf"
        or "\u2500" <= ch <= "\u259f"
        or ch in "\u0152\u0153\u0161\u017e"
    )


total = 0
for path in sorted(glob.glob(sys.argv[1])):
    with open(path, encoding="utf-8") as fh:
        text = fh.read()
    hits = []
    i = 0
    while i + 1 < len(text):
        if is_lead(text[i]) and is_follow(text[i + 1]):
            end = i + 2
            while end < len(text) and is_follow(text[end]):
                end += 1
            hits.append((i, text[i:end]))
            i = end
            continue
        i += 1
    if not hits:
        continue
    total += len(hits)
    name = path.rsplit("\\", 1)[-1].rsplit("/", 1)[-1]
    print(f"\n=== {name}: {len(hits)} match(es) ===")
    for pos, frag in hits[:6]:
        points = " ".join(f"U+{ord(c):04X}" for c in frag)
        try:
            names = ", ".join(unicodedata.name(c, "?") for c in frag)
        except Exception:
            names = "?"
        context = text[max(0, pos - 45): pos + len(frag) + 45].replace("\n", "|")
        print(f"  {frag!r} [{points}]")
        print(f"    names: {names}")
        print(f"    context: {context!r}")

print(f"\ntotal matches across corpus: {total}")
