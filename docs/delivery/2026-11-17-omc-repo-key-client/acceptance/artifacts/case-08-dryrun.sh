#!/bin/bash
# T5-S14: --dry-run must not write anything (files, sizes, mtimes, new entries).
set -u
BIN=/evidence/oh-my-cpa
HELPER=/evidence/keytest-helper
LOG=/evidence/case-logs/case-08-dryrun.log
mkdir -p /evidence/case-logs; : > "$LOG"
say() { echo "$@" | tee -a "$LOG"; }
assert() { if [ "$2" = "0" ]; then echo "ASSERT $1 PASS" | tee -a "$LOG"; else echo "ASSERT $1 FAIL" | tee -a "$LOG"; FAILS=$((FAILS+1)); fi; }
FAILS=0
export TZ=UTC ASSISTANT_NO_SEND=1
WORK=$(mktemp -d /tmp/t5s14-case08.XXXXXX); UHOME="$WORK/uh"; mkdir -p "$UHOME/.claude"
H="$WORK/h"; REPO=org/repo; BASE=http://127.0.0.1:18317
printf 'sk-cpa-0123456789abcdef0123456789abcdef' > "$WORK/s.txt"
printf '{"env":{"OTHER":"1"}}' > "$UHOME/.claude/settings.json"
mkdir -p "$H"
"$BIN" key gen --repo "$REPO" --home "$H" --base-url "$BASE" > "$WORK/pub" 2>/dev/null
"$HELPER" -mode envelope -public-key-file "$WORK/pub" -plaintext-file "$WORK/s.txt" -out "$WORK/mcp" -mcp -key-mask 'sk-cpa-01***cdef' -application-id a1
"$BIN" key accept --repo "$REPO" --home "$H" < "$WORK/mcp" >/dev/null 2>&1
HOME="$UHOME" "$BIN" key install --runtime claude --repo "$REPO" --home "$H" --base-url "$BASE" >/dev/null 2>&1
snapshot() { find "$H" "$UHOME" -printf '%p %s %T@\n' | sort; }
snapshot > "$WORK/before"
HOME="$UHOME" "$BIN" key purge --repo "$REPO" --home "$H" --dry-run > "$WORK/out" 2> "$WORK/err"; C=$?
snapshot > "$WORK/after"
say "dry-run exit=$C stdout:"; sed -n '1,20p' "$WORK/out" >> "$LOG"
diff "$WORK/before" "$WORK/after" > "$WORK/diff" 2>&1; assert "dry-run leaves every file and mtime unchanged" $?
grep -q "would remove" "$WORK/out" && assert "dry-run lists what a real purge would remove" 0 || assert "dry-run lists what a real purge would remove" 1
say "RESULT case-08 FAILS=$FAILS"
exit $FAILS
