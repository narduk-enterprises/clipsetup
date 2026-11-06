#!/usr/bin/env python3
"""Reconcile a finished (or running) Codex run (sol or astra): what did it generate, what reached raw/, was it modified.

usage: collect_raw.py RUN_DIR [--copy]
Reads the thread id from the first line of RUN_DIR/events.jsonl, lists
~/.codex/generated_images/<thread_id>/exec-*.png, and byte-compares them to RUN_DIR/raw/*.png.
  MATCH     raw file is a byte-for-byte copy of an original (good)
  MODIFIED  raw file matches no original: the model resized/recompressed it. Use the exec-*.png original.
  UNCOPIED  original never reached raw/. With --copy it is copied to raw/unnamed-<n>-<id>.png so you
            can rename it and add it to index.md. The run's index.md is the naming authority.
Also prints exit.txt, the generation count and index.md presence, so it doubles as a status check.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import shutil
from pathlib import Path


def sha(p: Path) -> str:
    return hashlib.sha256(p.read_bytes()).hexdigest()


def main() -> None:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("run_dir")
    ap.add_argument("--copy", action="store_true")
    a = ap.parse_args()
    run = Path(a.run_dir)
    ev = run / "events.jsonl"
    exitf = run / "exit.txt"
    print(f"exit: {exitf.read_text().strip() if exitf.is_file() else 'still running (no exit.txt)'}")
    if not ev.is_file() or not ev.read_text().strip():
        raise SystemExit("no events.jsonl yet (run not started?)")
    first = json.loads(ev.read_text().splitlines()[0])
    tid = first.get("thread_id")
    if not tid:
        raise SystemExit(f"first events line has no thread_id: {first}")
    gen_dir = Path.home() / ".codex/generated_images" / tid
    gens = sorted(gen_dir.glob("exec-*.png"), key=lambda p: p.stat().st_mtime) if gen_dir.is_dir() else []
    raws = sorted((run / "raw").glob("*.png")) if (run / "raw").is_dir() else []
    print(f"thread {tid}: {len(gens)} generated, {len(raws)} in raw/, index.md "
          f"{'present' if (run / 'raw/index.md').is_file() else 'MISSING'}")
    gsha = {sha(g): g for g in gens}
    rsha = {sha(r): r for r in raws}
    for r in raws:
        print(f"  {'MATCH   ' if sha(r) in gsha else 'MODIFIED'} raw/{r.name}"
              + (f"  = {gsha[sha(r)].name}" if sha(r) in gsha else ""))
    n = 0
    for h, g in gsha.items():
        if h not in rsha:
            n += 1
            if n <= 12:
                print(f"  UNCOPIED {g}" + ("  (more not shown)" if n == 12 else ""))
            if a.copy:
                (run / "raw").mkdir(exist_ok=True)
                shutil.copy2(g, run / "raw" / f"unnamed-{n:02d}-{g.stem[-8:]}.png")
    if n and not a.copy:
        print(f"{n} original(s) not in raw/. Re-run with --copy to copy them.")


if __name__ == "__main__":
    main()
