#!/usr/bin/env python3
"""Apply fix files to the ledger.

A fix file line is: <fact id> TAB <old where> TAB <new where> TAB <new evidence>.
Every ledger line matching (fact id, old where) is replaced by the fix lines that
name it, so one uncovered line can become several help lines.

Usage: python3 -I scripts/skill-coverage/apply.py FIX.tsv...
"""
import glob
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
fixes, missing = {}, []
for path in sys.argv[1:]:
    for n, line in enumerate(open(path, encoding="utf-8"), 1):
        line = line.rstrip("\n")
        if not line.strip() or line.startswith("#"):
            continue
        parts = line.split("\t")
        if len(parts) != 4:
            sys.exit(f"{path}:{n}: want 4 tab-separated fields")
        fixes.setdefault((parts[0], parts[1]), []).append(f"{parts[0]}\t{parts[2]}\t{parts[3]}")

used = set()
for ledger in sorted(glob.glob(os.path.join(HERE, "ledger", "*.tsv"))):
    out = []
    for line in open(ledger, encoding="utf-8"):
        parts = line.rstrip("\n").split("\t")
        key = (parts[0], parts[1]) if len(parts) == 3 else None
        if key in fixes:
            if key not in used:
                out.extend(l + "\n" for l in fixes[key])
                used.add(key)
            continue
        out.append(line)
    open(ledger, "w", encoding="utf-8").writelines(out)

for key in fixes:
    if key not in used:
        missing.append(key)
for fid, where in missing:
    print(f"no ledger line for {fid}\t{where}", file=sys.stderr)
sys.exit(1 if missing else 0)
