#!/bin/bash
# Full stderr for the two C-29 "missing parent" scenarios, plus a look at what the junk
# TOML becomes after the tool appends its managed block.
set -u
BIN=/evidence/oh-my-cpa
HELPER=/evidence/keytest-helper
LOG=/evidence/case-logs/case-03d-missing.log
mkdir -p /evidence/case-logs; : > "$LOG"
say() { echo "$@" | tee -a "$LOG"; }
export TZ=UTC ASSISTANT_NO_SEND=1
WORK=$(mktemp -d /tmp/t5s14-case03d.XXXXXX)
UH="$WORK/uh"; H="$WORK/h"; REPO=org/fixture-repo; BASE=http://127.0.0.1:18317
printf 'sk-cpa-0123456789abcdef0123456789abcdef' > "$WORK/s.txt"
mkdir -p "$H"
"$BIN" key gen --repo "$REPO" --home "$H" --base-url "$BASE" > "$WORK/pub.txt" 2>/dev/null
"$HELPER" -mode envelope -public-key-file "$WORK/pub.txt" -plaintext-file "$WORK/s.txt" -out "$WORK/mcp.json" -mcp -key-mask 'sk-cpa-01***cdef' -application-id a1
"$BIN" key accept --repo "$REPO" --home "$H" < "$WORK/mcp.json" >/dev/null 2>&1
mkdir -p "$UH"
HOME="$UH" "$BIN" key install --runtime claude --repo "$REPO" --home "$H" --base-url "$BASE" > "$WORK/c.out" 2> "$WORK/c.err"; C=$?
say "missing ~/.claude exit=$C"
say "stdout:"; sed -n '1,5p' "$WORK/c.out" >> "$LOG"
say "stderr:"; sed -n '1,30p' "$WORK/c.err" >> "$LOG"
HOME="$UH" "$BIN" key install --runtime opencode --repo "$REPO" --home "$H" --base-url "$BASE" > "$WORK/o.out" 2> "$WORK/o.err"; O=$?
say "missing ~/.config/opencode exit=$O"
say "stdout:"; sed -n '1,5p' "$WORK/o.out" >> "$LOG"
say "stderr:"; sed -n '1,30p' "$WORK/o.err" >> "$LOG"
say "RESULT case-03d done"
exit 0
