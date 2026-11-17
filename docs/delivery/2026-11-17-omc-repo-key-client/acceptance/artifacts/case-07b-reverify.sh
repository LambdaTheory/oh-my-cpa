#!/bin/bash
# T5-S14 follow-up for R-02/R-05/P2-4: tolerant JSON checks, an exec that reaches the
# gateway warning, and a --runtime purge on a real installed slot.
set -u
BIN=/evidence/oh-my-cpa
HELPER=/evidence/keytest-helper
LOG=/evidence/case-logs/case-07b-reverify.log
mkdir -p /evidence/case-logs; : > "$LOG"
say() { echo "$@" | tee -a "$LOG"; }
section() { echo "" | tee -a "$LOG"; echo "===== $* =====" | tee -a "$LOG"; }
FAILS=0
assert() { if [ "$2" = "0" ]; then echo "ASSERT $1 PASS" | tee -a "$LOG"; else echo "ASSERT $1 FAIL" | tee -a "$LOG"; FAILS=$((FAILS+1)); fi; }
export TZ=UTC ASSISTANT_NO_SEND=1
WORK=$(mktemp -d /tmp/t5s14-case07b.XXXXXX); UHOME="$WORK/uh"; mkdir -p "$UHOME"
H="$WORK/h"; mkdir -p "$H"; REPO=org/repo; BASE=http://127.0.0.1:18317
printf 'sk-cpa-0123456789abcdef0123456789abcdef' > "$WORK/s.txt"
"$BIN" key gen --repo "$REPO" --home "$H" --base-url "$BASE" > "$WORK/pub" 2>/dev/null
"$HELPER" -mode envelope -public-key-file "$WORK/pub" -plaintext-file "$WORK/s.txt" -out "$WORK/mcp" -mcp -key-mask 'sk-cpa-01***cdef' -application-id a1
"$BIN" key accept --repo "$REPO" --home "$H" < "$WORK/mcp" >/dev/null 2>&1

section "R-02 status --json source=env (tolerant parsing)"
OMCPA_CPA_BASE_URL=http://127.0.0.1:19997 "$BIN" key status --home "$H" --json > /tmp/j 2> /tmp/e
sed -n '1,6p' /tmp/j >> "$LOG"
grep -qE '"base_url_source": *"env"' /tmp/j && assert "R-02 --json says base_url_source=env" 0 || assert "R-02 --json says base_url_source=env" 1
grep -qE '"base_url": *"http://127.0.0.1:19997"' /tmp/j && assert "R-02 --json carries the env address" 0 || assert "R-02 --json carries the env address" 1

section "R-05 the non-loopback plain-http warning, per command"
"$BIN" key gen --repo org/other --home "$WORK/h2" --base-url http://10.9.9.9:8317 > /tmp/o 2> /tmp/e
say "gen stderr: $(tr '\n' ' ' < /tmp/e)"
grep -qi "insecure_base_url" /tmp/e && assert "R-05 gen names insecure_base_url" 0 || assert "R-05 gen names insecure_base_url (it does not)" 1
"$BIN" key exec --runtime claude --repo "$REPO" --home "$H" --base-url http://10.9.9.9:8317 -- true > /tmp/o 2> /tmp/e; E=$?
say "exec exit=$E stderr: $(tr '\n' ' ' < /tmp/e)"
grep -qi "insecure_base_url" /tmp/e && assert "R-05 exec names insecure_base_url" 0 || assert "R-05 exec names insecure_base_url (it does not)" 1
grep -qi "plain http" /tmp/e && assert "R-05 exec warns in plain words" 0 || assert "R-05 exec warns in plain words" 1
mkdir -p "$UHOME/.claude"; printf '{"env":{"OTHER":"1"}}' > "$UHOME/.claude/settings.json"
HOME="$UHOME" "$BIN" key install --runtime claude --repo "$REPO" --home "$H" --base-url http://10.9.9.9:8317 > /tmp/o 2> /tmp/e; I=$?
say "install exit=$I stderr: $(tr '\n' ' ' < /tmp/e)"
grep -qi "insecure_base_url" /tmp/e && assert "R-05 install names insecure_base_url" 0 || assert "R-05 install names insecure_base_url (it does not)" 1
grep -qi "plain http" /tmp/e && assert "R-05 install warns in plain words" 0 || assert "R-05 install warns in plain words" 1

section "P2-4 purge --runtime on a real installed slot"
"$BIN" key purge --repo "$REPO" --runtime claude --home "$H" > /tmp/o 2> /tmp/e; C=$?
say "exit=$C stdout: $(tr '\n' ' ' < /tmp/o) stderr: $(tr '\n' ' ' < /tmp/e)"
grep -qi "installed value" /tmp/o && grep -qi "private key and envelope are still" /tmp/o && assert "P2-4 --runtime wording says the key stays" 0 || assert "P2-4 --runtime wording says the key stays" 1
[ -f "$(find "$H/keys" -type f | head -1)" ] && assert "P2-4 the private key is still present" 0 || assert "P2-4 the private key is still present" 1

say "RESULT case-07b FAILS=$FAILS"
exit $FAILS
