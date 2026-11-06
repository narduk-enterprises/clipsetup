#!/usr/bin/env bash
# Launch ONE Codex image run (the sol or astra backend). usage: codex_run.sh [--model sol|astra|<slug>] RUN_DIR [ref-image ...]
# RUN_DIR must hold brief.md. Model: sol = gpt-6.1-sol (default), astra = gpt-6-astra, or any slug from ~/.codex/models_cache.json.
# Reference images (style refs, the chosen mark) are attached with -i; the prompt still comes from stdin, so there is NO
# trailing "-" when -i is used (clap would take it as an image).
# Logs: RUN_DIR/events.jsonl (JSONL, line 1 = thread id), stderr.log, last-message.md, exit.txt.
# Run it in the background (a run takes 1-25 min); watch it with collect_raw.py RUN_DIR.
# Env: CODEX (binary, default codex), EFFORT (reasoning effort, default high), CODEX_PROFILE (optional --profile).
set -u
MODEL=sol
if [ "${1:-}" = "--model" ]; then MODEL="${2:?--model needs sol, astra or a slug}"; shift 2; fi
case "$MODEL" in sol) SLUG=gpt-6.1-sol ;; astra) SLUG=gpt-6-astra ;; *) SLUG="$MODEL" ;; esac
RUN="${1:?usage: codex_run.sh [--model sol|astra|<slug>] RUN_DIR [ref-image ...]}"; shift
CODEX="${CODEX:-codex}"
[ -f "$RUN/brief.md" ] || { echo "no $RUN/brief.md" >&2; exit 2; }
mkdir -p "$RUN/raw"; RUN="$(cd "$RUN" && pwd)"
refs=(); for f in "$@"; do [ -f "$f" ] || { echo "no reference image $f" >&2; exit 2; }; refs+=("$(cd "$(dirname "$f")" && pwd)/$(basename "$f")"); done
cd "$RUN" || exit 1
ARGS=(exec -m "$SLUG" -c "model_reasoning_effort=${EFFORT:-high}" --enable image_generation
      --skip-git-repo-check --approve-for-me --json -C "$RUN" -o "$RUN/last-message.md")
[ -n "${CODEX_PROFILE:-}" ] && ARGS+=(--profile "$CODEX_PROFILE")
rm -f "$RUN/exit.txt"; echo "$SLUG" > "$RUN/model.txt"
if [ "${#refs[@]}" -gt 0 ]; then
  "$CODEX" "${ARGS[@]}" -i "${refs[@]}" < "$RUN/brief.md" > "$RUN/events.jsonl" 2> "$RUN/stderr.log"
else
  "$CODEX" "${ARGS[@]}" - < "$RUN/brief.md" > "$RUN/events.jsonl" 2> "$RUN/stderr.log"
fi
echo "exit $?" > "$RUN/exit.txt"
