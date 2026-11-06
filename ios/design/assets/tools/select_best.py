#!/usr/bin/env python3
"""Read every Codex run's raw/index.md, find the attempt the model marked BEST per asset.

usage: select_best.py RUNS_DIR [--json]      (RUNS_DIR holds one directory per run, e.g. <kit>/.lane/runs)
Groups attempts by base name (strip -v2/-v3). BEST = a row whose cells contain the word BEST.
Assets with no BEST marker, or several, are listed as UNRESOLVED: open the candidates and pick
by eye, then `kit.py keep` the one you chose. The model's own QA notes are usually
right but the final pick is yours (look at the image).
"""
from __future__ import annotations

import argparse
import json
import re
from pathlib import Path


def rows(index: Path):
    for line in index.read_text(errors="replace").splitlines():
        if not line.startswith("|"):
            continue
        cells = [c.strip() for c in line.strip().strip("|").split("|")]
        m = re.match(r"(?:\*\*)?`?([\w.\-]+\.png)", cells[0]) if cells else None
        if m:
            best = any(re.search(r"\bBEST\b", c) and not re.search(r"not\s+BEST", c, re.I) for c in cells)
            yield m.group(1), best


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("runs_dir")
    ap.add_argument("--json", action="store_true")
    a = ap.parse_args()
    root = Path(a.runs_dir)
    picked, unresolved = {}, {}
    for run in sorted(p for p in root.iterdir() if p.is_dir()):
        idx = next((p for p in (run / "raw" / "index.md", run / "index.md") if p.is_file()), None)
        if not idx:
            unresolved[run.name] = ["no raw/index.md written by the run"]
            continue
        groups: dict[str, list[tuple[str, bool]]] = {}
        for fn, best in rows(idx):
            groups.setdefault(re.sub(r"-v\d+$", "", fn[:-4]), []).append((fn, best))
        for base, items in groups.items():
            bests = [fn for fn, b in items if b]
            if len(bests) == 1:
                picked[f"{run.name}/{bests[0]}"] = base
            else:
                unresolved[f"{run.name}/{base}"] = [fn for fn, _ in items]
    if a.json:
        print(json.dumps({"best": picked, "unresolved": unresolved}, indent=1))
        return
    print("BEST picks (run/file -> asset):")
    for k, v in picked.items():
        print(f"  {k}  ({v})")
    if unresolved:
        print("UNRESOLVED (no single BEST marker; choose by eye):")
        for k, v in unresolved.items():
            print(f"  {k}: {', '.join(v)}")


if __name__ == "__main__":
    main()
