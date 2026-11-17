#!/bin/bash
# T5-S14 re-verification of review items: R-02 (status reads OMCPA_CPA_BASE_URL), R-05
# (non-loopback http warning wording), P2-5 (purge on a never-used home creates nothing).
set -u
BIN=/evidence/oh-my-cpa
LOG=/evidence/case-logs/case-07-reverify.log
mkdir -p /evidence/case-logs; : > "$LOG"
say() { echo "$@" | tee -a "$LOG"; }
section() { echo "" | tee -a "$LOG"; echo "===== $* =====" | tee -a "$LOG"; }
FAILS=0
assert() { if [ "$2" = "0" ]; then echo "ASSERT $1 PASS" | tee -a "$LOG"; else echo "ASSERT $1 FAIL" | tee -a "$LOG"; FAILS=$((FAILS+1)); fi; }
export TZ=UTC ASSISTANT_NO_SEND=1
WORK=$(mktemp -d /tmp/t5s14-case07.XXXXXX)
H="$WORK/h"; mkdir -p "$H"
"$BIN" key gen --repo org/repo --home "$H" >/dev/null 2>&1

section "R-02 status reports the gateway from the environment"
OMCPA_CPA_BASE_URL=http://127.0.0.1:19997 "$BIN" key status --home "$H" > /tmp/o 2> /tmp/e
say "text: $(head -2 /tmp/o | tr '\n' ' ')"
grep -q "http://127.0.0.1:19997" /tmp/o && assert "R-02 human output shows the env gateway" 0 || assert "R-02 human output shows the env gateway" 1
OMCPA_CPA_BASE_URL=http://127.0.0.1:19997 "$BIN" key status --home "$H" --json > /tmp/j 2> /tmp/e
grep -q '"base_url_source":"env"' /tmp/j && assert "R-02 --json says base_url_source=env" 0 || assert "R-02 --json says base_url_source=env" 1
grep -q '"base_url":"http://127.0.0.1:19997"' /tmp/j && assert "R-02 --json carries the env address" 0 || assert "R-02 --json carries the env address" 1

section "R-05 non-loopback plain-http warning"
"$BIN" key gen --repo org/other --home "$WORK/h2" --base-url http://10.9.9.9:8317 > /tmp/o 2> /tmp/e
say "gen: $(tr '\n' ' ' < /tmp/e)"
grep -qi "insecure_base_url" /tmp/e && assert "R-05 gen names insecure_base_url" 0 || assert "R-05 gen names insecure_base_url (it does not)" 1
grep -qi "plain http\|plaintext http\|not https" /tmp/e && assert "R-05 gen warns about plain http at all" 0 || assert "R-05 gen warns about plain http at all" 1
mkdir -p "$WORK/h3"
"$BIN" key gen --repo org/three --home "$WORK/h3" >/dev/null 2>&1
"$BIN" key exec --runtime claude --repo org/three --home "$WORK/h3" --base-url http://10.9.9.9:8317 -- true > /tmp/o 2> /tmp/e; E=$?
say "exec exit=$E stderr=$(tr '\n' ' ' < /tmp/e)"
grep -qi "insecure_base_url" /tmp/e && assert "R-05 exec names insecure_base_url" 0 || assert "R-05 exec names insecure_base_url (it does not)" 1

section "P2-5 purge on a never-used home"
E2="$WORK/never"; "$BIN" key purge --repo org/never --home "$E2" > /tmp/o 2> /tmp/e
C=$?
say "exit=$C stdout=$(tr '\n' ' ' < /tmp/o) stderr=$(tr '\n' ' ' < /tmp/e)"
CREATED=$(find "$E2" -mindepth 1 2>/dev/null | wc -l)
say "entries created under the never-used home: $CREATED"
[ "$CREATED" -eq 0 ] && assert "P2-5 purge creates nothing on a never-used home" 0 || assert "P2-5 purge creates nothing on a never-used home ($CREATED entries)" 1
grep -qi "already gone\|never existed\|nothing to remove" /tmp/o /tmp/e && assert "P2-5 wording distinguishes never-existed" 0 || assert "P2-5 wording distinguishes never-existed" 1

section "P2-4 purge --runtime wording keeps the key"
"$BIN" key gen --repo org/four --home "$WORK/h4" >/dev/null 2>&1
"$BIN" key purge --repo org/four --runtime claude --home "$WORK/h4" > /tmp/o 2> /tmp/e; C4=$?
say "exit=$C4 stdout=$(tr '\n' ' ' < /tmp/o)"
grep -qi "installed value" /tmp/o && grep -qi "private key and envelope are still" /tmp/o && assert "P2-4 --runtime wording says the key stays" 0 || assert "P2-4 --runtime wording says the key stays" 1
say "RESULT case-07 FAILS=$FAILS"
exit $FAILS
