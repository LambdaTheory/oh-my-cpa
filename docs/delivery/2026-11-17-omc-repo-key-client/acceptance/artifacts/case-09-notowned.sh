#!/bin/bash
# T5-S14 P2-6 safety check: when a runtime value was changed after install, purge must
# leave it alone, say install_not_owned, and not delete the key as a side effect.
set -u
BIN=/evidence/oh-my-cpa
HELPER=/evidence/keytest-helper
LOG=/evidence/case-logs/case-09-notowned.log
mkdir -p /evidence/case-logs; : > "$LOG"
say() { echo "$@" | tee -a "$LOG"; }
assert() { if [ "$2" = "0" ]; then echo "ASSERT $1 PASS" | tee -a "$LOG"; else echo "ASSERT $1 FAIL" | tee -a "$LOG"; fi; }
export TZ=UTC ASSISTANT_NO_SEND=1
W=$(mktemp -d /tmp/t5s14-case09.XXXXXX); UH=$W/uh; H=$W/h; mkdir -p "$UH/.claude" "$H"
printf 'sk-cpa-0123456789abcdef0123456789abcdef' > "$W/s"
printf '{"env":{"OTHER":"1"}}' > "$UH/.claude/settings.json"
"$BIN" key gen --repo org/repo --home "$H" --base-url http://127.0.0.1:18317 > "$W/pub" 2>/dev/null
"$HELPER" -mode envelope -public-key-file "$W/pub" -plaintext-file "$W/s" -out "$W/m" -mcp -key-mask 'sk-cpa-01***cdef' -application-id a1
"$BIN" key accept --repo org/repo --home "$H" < "$W/m" >/dev/null 2>&1
HOME=$UH "$BIN" key install --runtime claude --repo org/repo --home "$H" --base-url http://127.0.0.1:18317 >/dev/null 2>&1
# Someone edits the value after the install.
sed -i 's/sk-cpa-0123456789abcdef0123456789abcdef/sk-cpa-edited-by-a-person/' "$UH/.claude/settings.json"
HOME=$UH "$BIN" key purge --repo org/repo --home "$H" > "$W/out" 2> "$W/err"; C=$?
say "purge exit=$C stdout=$(tr '\n' ' ' < "$W/out") stderr=$(tr '\n' ' ' < "$W/err")"
grep -q "install_not_owned" "$W/err" && assert "P2-6 changed value is reported as install_not_owned" 0 || assert "P2-6 changed value is reported as install_not_owned" 1
grep -q "sk-cpa-edited-by-a-person" "$UH/.claude/settings.json" && assert "P2-6 the changed value is left in place" 0 || assert "P2-6 the changed value is left in place" 1
[ -f "$(find "$H/keys" -type f | head -1)" ] && assert "P2-6 the private key survives an aborted purge" 0 || assert "P2-6 the private key survives an aborted purge" 1
say "RESULT case-09 done"
exit 0
