#!/bin/bash
# T5-S14 R-04: every `key` subcommand must answer a failing run with one JSON object
# {"code","message"} on stderr when --json was requested, keep the exit code, and keep
# stdout empty. `exec` must not let a child's own --json choose the parent's format.
set -u
BIN=/evidence/oh-my-cpa
LOG=/evidence/case-logs/case-04-json.log
JDIR=/evidence/case-logs/json-errors
mkdir -p /evidence/case-logs; rm -rf "$JDIR"; mkdir -p "$JDIR"; : > "$LOG"
FAILS=0
say() { echo "$@" | tee -a "$LOG"; }
section() { echo "" | tee -a "$LOG"; echo "===== $* =====" | tee -a "$LOG"; }
assert() { if [ "$2" = "0" ]; then echo "ASSERT $1 PASS" | tee -a "$LOG"; else echo "ASSERT $1 FAIL" | tee -a "$LOG"; FAILS=$((FAILS+1)); fi; }
export TZ=UTC ASSISTANT_NO_SEND=1
WORK=$(mktemp -d /tmp/t5s14-case04.XXXXXX)
H="$WORK/omc-home"; E="$WORK/empty-home"
check() { # check <name> <want-exit> <want-code> -- command...
  local name="$1" want="$2" code="$3"; shift 3
  echo "--- $name: $*" >> "$LOG"
  "$@" > "$JDIR/$name.out" 2> "$JDIR/$name.err"
  local got=$?
  echo "exit=$got (want $want)" | tee -a "$LOG"
  echo "stdout-bytes=$(wc -c < "$JDIR/$name.out")" >> "$LOG"
  cat "$JDIR/$name.err" >> "$LOG"
  [ "$got" -eq "$want" ] && assert "$name exit is $want" 0 || assert "$name exit is $want (got $got)" 1
  [ -s "$JDIR/$name.out" ] && assert "$name stdout is empty" 1 || assert "$name stdout is empty" 0
  grep -q "\"code\":\"$code\"" "$JDIR/$name.err" && assert "$name carries code $code" 0 || assert "$name carries code $code" 1
  grep -q '"message":' "$JDIR/$name.err" && assert "$name carries a message" 0 || assert "$name carries a message" 1
  [ "$(wc -l < "$JDIR/$name.err")" -eq 1 ] && assert "$name writes one line" 0 || assert "$name writes one line" 1
}

section "R-04 six subcommands, --json, one failure each"
check gen-json 2 repo_required "$BIN" key gen --json
check accept-json 1 envelope_invalid sh -c " : | '$BIN' key accept --json --repo org/fixture-repo --home '$E'"
check exec-json 2 runtime_unknown "$BIN" key exec --json --runtime nope --home "$H" -- true
check install-json 4 key_not_found "$BIN" key install --json --runtime claude --repo org/fixture-repo --home "$E"
check purge-json 2 repo_required "$BIN" key purge --json --home "$H"
check status-json 3 remote_not_configured env -u OMCPA_SERVER_URL -u OMCPA_CPA_MANAGEMENT_KEY "$BIN" key status --remote --json --home "$H"

section "R-04 flag placement and the child's own --json"
"$BIN" key gen --json=false > "$JDIR/gen-json-false.out" 2> "$JDIR/gen-json-false.err"
if grep -q '^{' "$JDIR/gen-json-false.err"; then assert "--json=false keeps the text form" 1; else assert "--json=false keeps the text form" 0; fi
"$BIN" key exec --runtime nope -- some-child --json > "$JDIR/child-json.out" 2> "$JDIR/child-json.err"
grep -q '^{' "$JDIR/child-json.err" && assert "exec lets the child's --json choose the parent format" 1 || assert "a child's --json does not change the parent's format" 0
"$BIN" key gen --json --bogus > "$JDIR/usage.out" 2> "$JDIR/usage.err"
grep -q '"code":"usage"' "$JDIR/usage.err" && assert "an unknown flag still answers in JSON" 0 || assert "an unknown flag still answers in JSON" 1
grep -q 'panic:' "$JDIR"/*.out "$JDIR"/*.err && assert "no panic in any JSON failure" 1 || assert "no panic in any JSON failure" 0

say ""
say "RESULT case-04 FAILS=$FAILS"
exit $FAILS
