#!/usr/bin/env python3
"""Fail if any file in the repository contains an em dash or an en dash.

Walks the repository from its root, skipping the .git directory, and reports
every line that contains U+2014 (em dash) or U+2013 (en dash) as path:line.
Binary files, recognised by a NUL byte, are skipped. Text is read as UTF-8.

Usage:
    python tools/check_dashes.py [root]

Exit code 0 when the tree is clean, 1 when at least one dash was found.
Written in Python rather than grep so that it behaves the same on Windows.
"""

import os
import sys
from pathlib import Path

DASHES = {
    chr(0x2014): "U+2014 em dash",
    chr(0x2013): "U+2013 en dash",
}
SKIP_DIRS = {".git"}


def repo_root() -> Path:
    return Path(__file__).resolve().parent.parent


def scan(root: Path) -> tuple[int, list[str]]:
    scanned = 0
    hits = []
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = sorted(d for d in dirnames if d not in SKIP_DIRS)
        for name in sorted(filenames):
            path = Path(dirpath) / name
            data = path.read_bytes()
            if b"\x00" in data:
                continue
            scanned += 1
            text = data.decode("utf-8", errors="replace")
            rel = path.relative_to(root).as_posix()
            for lineno, line in enumerate(text.splitlines(), start=1):
                for char, label in DASHES.items():
                    if char in line:
                        column = line.index(char) + 1
                        hits.append(f"{rel}:{lineno}:{column}: {label}")
    return scanned, hits


def main(argv: list[str]) -> int:
    root = Path(argv[1]).resolve() if len(argv) > 1 else repo_root()
    scanned, hits = scan(root)
    if hits:
        for hit in hits:
            print(hit)
        print(f"check_dashes: {len(hits)} dash(es) found in {root}")
        return 1
    print(f"check_dashes: OK, {scanned} text files scanned, no dashes")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
