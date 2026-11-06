#!/usr/bin/env bash
# Preflight for an asset kit. usage: preflight.sh [--backend grok|sol|astra|all] [KIT_DIR]   (exit 0 = ready, 1 = something to fix)
# Backend grok (default): grok on PATH and signed in (test -f ~/.grok/auth.json, never read). Backends sol and astra: Codex >= 0.159
# (0.158 rejects gpt-6.1-sol on a ChatGPT account), the model listed in ~/.codex/models_cache.json, and the Codex quota.
# Always: ImageMagick 7 and pillow. Pass the kit dir to also check what that kit needs: potrace and librsvg only when an icon
# is kept with --vector, and a font file when an og asset has a title (typeset at derive time).
set -u
rc=0; ok(){ echo "ok   $*"; }; bad(){ echo "FAIL $*"; rc=1; }
BACKEND=grok
if [ "${1:-}" = "--backend" ]; then BACKEND="${2:?--backend needs grok, sol, astra or all}"; shift 2; fi
case "$BACKEND" in grok|sol|astra|all) ;; *) echo "unknown backend $BACKEND (grok, sol, astra, all)" >&2; exit 2 ;; esac
want(){ [ "$BACKEND" = all ] || [ "$BACKEND" = "$1" ]; }

if want grok; then
  if command -v grok >/dev/null; then ok "grok $(grok --version 2>/dev/null | head -1)"; else bad "grok not found on PATH"; fi
  if test -f "$HOME/.grok/auth.json"; then ok "grok signed in (~/.grok/auth.json present)"
  else bad "grok is not signed in (run grok once and sign in; never read ~/.grok/auth.json)"; fi
fi

if want sol || want astra; then
  CODEX="${CODEX:-codex}"
  # every codex on PATH (volta + brew installs) must be new enough: a stale one wins depending on PATH order
  for bin in $(which -a "$CODEX" 2>/dev/null | awk '!s[$0]++'); do
    v="$("$bin" --version 2>/dev/null | grep -Eo '[0-9]+\.[0-9]+\.[0-9]+' | head -1)"
    IFS=. read -r a b _ <<<"${v:-0.0.0}"
    if [ "$a" -gt 0 ] || [ "$b" -ge 159 ]; then ok "codex $v ($bin)"
    else bad "codex ${v:-?} ($bin) < 0.159 rejects gpt-6.1-sol on a ChatGPT account. Fix: volta install @openai/codex@latest; brew upgrade codex"; fi
  done
  command -v "$CODEX" >/dev/null || bad "codex not found (volta install @openai/codex@latest)"
  for pair in "sol:gpt-6.1-sol" "astra:gpt-6-astra"; do
    want "${pair%%:*}" || continue
    slug="${pair#*:}"
    if SLUG="$slug" python3 - <<'PY'
import json, os, sys
p = os.path.expanduser("~/.codex/models_cache.json")
try:
    d = json.load(open(p)); ms = d.get("models", d)
    sys.exit(0 if any(m.get("slug") == os.environ["SLUG"] for m in ms) else 1)
except Exception:
    sys.exit(2)
PY
    then ok "$slug listed in ~/.codex/models_cache.json"
    else bad "$slug not in ~/.codex/models_cache.json (run any codex command to refresh it; still missing = not available to this login)"; fi
  done
  HERE="$(cd "$(dirname "$0")" && pwd)"
  for q in "$HERE/../../../scripts/model-usage-status.py" "$HOME/.local/share/agent-infrastructure/scripts/model-usage-status.py"; do
    [ -f "$q" ] && { python3 "$q" --json 2>/dev/null | python3 -c '
import json,sys
try:
    for p in json.load(sys.stdin)["providers"]:
        if p.get("provider")=="codex": print("note codex quota: %s%% used, state %s (%s)" % (p.get("percent_used"), p.get("state"), p.get("plan_type")))
except Exception: print("note quota probe gave no codex row (unknown is not zero)")'; break; }
  done
fi

command -v magick >/dev/null && ok "magick" || bad "magick missing (brew install imagemagick; kit.py keep and derive need ImageMagick 7)"
command -v python3 >/dev/null && ok "python3" || bad "python3 missing"
python3 - <<'PY' || rc=1
import importlib.util
if importlib.util.find_spec("PIL") is None:
    print("FAIL python module pillow missing (pip3 install pillow; the gate's pixel-fidelity check needs it)")
    raise SystemExit(1)
print("ok   pillow")
if importlib.util.find_spec("numpy") is None:
    print("warn numpy missing (only slice_sheet.py and inspect_raw.py need it: pip3 install numpy)")
PY
# potrace and librsvg serve only an icon kept with --vector ("vector": true on that icon in icons/manifest.json)
KIT=""; [ -d "${1:-}" ] && KIT="$(cd "$1" && pwd)"
VEC=0; [ -n "$KIT" ] && [ -f "$KIT/icons/manifest.json" ] && grep -Eq '"vector": *true' "$KIT/icons/manifest.json" && VEC=1
for t in potrace rsvg-convert; do
  if command -v "$t" >/dev/null; then ok "$t"
  elif [ "$VEC" = 1 ]; then bad "$t missing (brew install potrace librsvg; an icon in this kit is kept with --vector)"
  else echo "warn $t missing (only needed when an icon is kept with --vector)"; fi
done
# an og asset with a title is typeset at derive time: it needs a font file
if [ -n "$KIT" ] && [ -f "$KIT/kit.json" ] && grep -q '"title"' "$KIT/kit.json"; then
  HERE="$(cd "$(dirname "$0")" && pwd)"
  if font="$(python3 - "$HERE" "$KIT" <<'PY' 2>&1
import json, sys
sys.path.insert(0, sys.argv[1])
import kit
man = json.load(open(sys.argv[2] + "/kit.json"))
og = next((a for a in man.get("assets", []) if a.get("kind") == "og" and a.get("title")), None)
if og:
    print(kit.resolve_font(og.get("font")))
PY
)"; then [ -n "$font" ] && ok "og title font $font"
  else bad "${font#error: }"; fi
fi
exit $rc
