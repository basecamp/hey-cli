#!/usr/bin/env python3
"""Check that every fact in the hey skill as it stood at BASE is still served.

The old skills/hey/SKILL.md is split into facts: one per sentence of prose, table
row, list item or code line. Each fact needs a ledger entry (ledger/*.tsv):

    <fact id> TAB skill TAB <evidence>
    <fact id> TAB help:<command path> TAB <evidence>
    <fact id> TAB uncovered:<destination> TAB <note>
    <fact id> TAB retired TAB <why the old skill was wrong>

An entry passes when its evidence appears, whitespace collapsed, in the new
SKILL.md or in the strings `hey <command path> --agent --help` prints. A fact may
carry several entries; it is covered only when it has at least one and every one
passes. The script proves the cited text exists. Whether the text states the
fact is the reviewer's call, which is why the evidence is in the ledger to read.

Usage: python3 -I scripts/skill-coverage/check.py [--base REV] [--binary bin/hey]
       [--facts] [--section PREFIX] [--verbose]
"""
import argparse
import glob
import json
import os
import re
import subprocess
import sys
from collections import OrderedDict

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
HERE = os.path.dirname(os.path.abspath(__file__))
BASE = "76ea03f"
NO_SPLIT_AFTER = ("e.g.", "i.e.", "vs.", "etc.", "approx.")


def slug(heading):
    return re.sub(r"[^a-z0-9]+", "-", heading.lower()).strip("-")


def sentences(text):
    out, start = [], 0
    for m in re.finditer(r"[.!?](?=\s+[A-Z`*(\"])", text):
        end = m.end()
        if text[start:end].rstrip().endswith(NO_SPLIT_AFTER):
            continue
        out.append(text[start:end].strip())
        start = end
    tail = text[start:].strip()
    if tail:
        out.append(tail)
    return out


def extract(skill):
    body = skill.split("\n---\n", 1)[1]
    facts = OrderedDict()
    section, counts = "intro", {}
    in_code, para = False, []

    def add(text):
        text = text.strip()
        if not text:
            return
        counts[section] = counts.get(section, 0) + 1
        if re.fullmatch(r"\d+\.", text):
            return
        facts[f"{section}/{counts[section]}"] = text

    def flush():
        if para:
            for s in sentences(" ".join(para)):
                add(s)
            para.clear()

    for line in body.split("\n"):
        stripped = line.strip()
        if stripped.startswith("```"):
            flush()
            in_code = not in_code
            continue
        if in_code:
            add(line)
            continue
        if m := re.match(r"^(#{1,3}) (.*)", line):
            flush()
            if len(m.group(1)) > 1:
                section = slug(m.group(2))
            continue
        if not stripped:
            flush()
            continue
        if stripped.startswith("|"):
            flush()
            if re.match(r"^\|[\s|:-]+\|$", stripped) or stripped.startswith("| Task |"):
                continue
            add(stripped)
            continue
        if re.match(r"^([-*]|\d+\.) ", stripped):
            flush()
        para.append(stripped)
    flush()
    return facts


def norm(text):
    return re.sub(r"\s+", " ", text).strip()


def strings_in(value):
    if isinstance(value, str):
        yield value
    elif isinstance(value, dict):
        for v in value.values():
            yield from strings_in(v)
    elif isinstance(value, list):
        for v in value:
            yield from strings_in(v)


class Sources:
    def __init__(self, binary, skill):
        self.binary, self.skill, self.help = binary, norm(skill), {}

    def text(self, where):
        if where == "skill":
            return self.skill
        path = where.split(":", 1)[1].split()
        key = " ".join(path)
        if key not in self.help:
            run = subprocess.run([self.binary, *path[1:], "--agent", "--help"],
                                 capture_output=True, text=True, env={**os.environ, "HEY_NO_KEYRING": "1"})
            try:
                data = json.loads(run.stdout)
                ok = data.get("name") == path[-1] or len(path) == 1
                self.help[key] = norm(" ".join(strings_in(data))) if ok else None
            except json.JSONDecodeError:
                self.help[key] = None
        return self.help[key]


def load_ledger():
    ledger, problems = {}, []
    for path in sorted(glob.glob(os.path.join(HERE, "ledger", "*.tsv"))):
        for n, line in enumerate(open(path, encoding="utf-8"), 1):
            line = line.rstrip("\n")
            if not line.strip() or line.startswith("#"):
                continue
            parts = line.split("\t")
            if len(parts) != 3:
                problems.append(f"{os.path.basename(path)}:{n}: want 3 tab-separated fields")
                continue
            ledger.setdefault(parts[0], []).append((parts[1].strip(), parts[2].strip()))
    return ledger, problems


def judge(entries, sources):
    if not entries:
        return "uncovered", "no ledger entry"
    kinds = set()
    if any(where == "retired" for where, _ in entries):
        reason = next(e for w, e in entries if w == "retired")
        return ("retired", "") if reason else ("uncovered", "retired without a reason")
    for where, evidence in entries:
        if where.startswith("uncovered"):
            return "uncovered", f"{where}: {evidence}"
        if where != "skill" and not where.startswith("help:hey"):
            return "uncovered", f"bad source {where!r}"
        text = sources.text(where)
        if text is None:
            return "uncovered", f"{where}: no such command"
        if not evidence or norm(evidence) not in text:
            return "uncovered", f"{where}: evidence not found: {evidence[:90]}"
        kinds.add("skill" if where == "skill" else "help")
    return ("skill" if kinds == {"skill"} else "help"), ""


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--base", default=BASE)
    ap.add_argument("--binary", default=os.path.join(ROOT, "bin", "hey"))
    ap.add_argument("--facts", action="store_true", help="print the facts as id TAB text and exit")
    ap.add_argument("--section", default="", help="only facts whose id starts with this")
    ap.add_argument("--verbose", action="store_true", help="list every fact, not only uncovered ones")
    args = ap.parse_args()

    old = subprocess.run(["git", "-C", ROOT, "show", f"{args.base}:skills/hey/SKILL.md"],
                         capture_output=True, text=True, check=True).stdout
    facts = OrderedDict((k, v) for k, v in extract(old).items() if k.startswith(args.section))
    if args.facts:
        for k, v in facts.items():
            print(f"{k}\t{v}")
        return 0

    ledger, problems = load_ledger()
    unknown = sorted(set(ledger) - set(extract(old)))
    problems += [f"ledger names no fact: {k}" for k in unknown]
    new = open(os.path.join(ROOT, "skills", "hey", "SKILL.md"), encoding="utf-8").read()
    sources = Sources(args.binary, new)

    totals, uncovered = OrderedDict(), []
    for fid, text in facts.items():
        verdict, why = judge(ledger.get(fid, []), sources)
        sec = fid.rsplit("/", 1)[0]
        row = totals.setdefault(sec, {"facts": 0, "help": 0, "skill": 0, "retired": 0, "uncovered": 0})
        row["facts"] += 1
        row[verdict] += 1
        if verdict == "uncovered":
            uncovered.append((fid, text, why))
        elif args.verbose:
            print(f"{verdict:9} {fid}  {text[:100]}")

    print(f"SKILL.md at {args.base}: {len(facts)} facts. New SKILL.md: {len(new.encode())} bytes.")
    cols = ("facts", "help", "skill", "retired", "uncovered")
    print(f"{'section':42}" + "".join(f" {c[:7]:>7}" for c in cols))
    for sec, row in list(totals.items()) + [("total", {c: sum(r[c] for r in totals.values()) for c in cols})]:
        print(f"{sec:42}" + "".join(f" {row[c]:7}" for c in cols))
    for p in problems:
        print(f"ledger problem: {p}")
    if uncovered:
        print("\nuncovered:")
        for fid, text, why in uncovered:
            print(f"  {fid}  {text[:110]}\n      {why}")
    return 1 if uncovered or problems else 0


if __name__ == "__main__":
    sys.exit(main())
