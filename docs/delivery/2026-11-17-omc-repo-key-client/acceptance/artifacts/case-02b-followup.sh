#!/bin/bash
# T5-S14 follow-up to case-02: re-run the five assertions whose setup or expectation
# needed fixing, and capture clean stdout/stderr for the behaviour questions.
set -u
BIN=/evidence/oh-my-cpa
HELPER=/evidence/keytest-helper
PROBE=/evidence/keytest-probe
LOG=/evidence/case-logs/case-02b-followup.log
mkdir -p /evidence/case-logs; : > "$LOG"
FAILS=0
say() { echo "$@" | tee -a "$LOG"; }
section() { echo "" | tee -a "$LOG"; echo "===== $* =====" | tee -a "$LOG"; }
assert() { if [ "$2" = "0" ]; then echo "ASSERT $1 PASS" | tee -a "$LOG"; else echo "ASSERT $1 FAIL" | tee -a "$LOG"; FAILS=$((FAILS+1)); fi; }
export TZ=UTC ASSISTANT_NO_SEND=1

WORK=$(mktemp -d /tmp/t5s14-case02b.XXXXXX)
UHOME="$WORK/userhome"; mkdir -p "$UHOME"
export HOME="$UHOME"
H="$WORK/omc-home"; REPO=org/fixture-repo; BASE=http://127.0.0.1:18317
SECRET='sk-cpa-0123456789abcdef0123456789abcdef'
printf '%s' "$SECRET" > "$WORK/secret.txt"
SECRET_SHA=$(sha256sum "$WORK/secret.txt" | cut -d' ' -f1)
mkdir -p "$H"
"$BIN" key gen --repo "$REPO" --home "$H" --base-url "$BASE" > "$WORK/pub.txt" 2>/dev/null
KEYFILE=$(find "$H/keys" -type f | head -1); STEM=$(basename "$KEYFILE" .key)
ENVELOPE="$H/envelopes/$STEM.json"
"$HELPER" -mode envelope -public-key-file "$WORK/pub.txt" -plaintext-file "$WORK/secret.txt" -out "$WORK/mcp.json" -mcp -key-mask 'sk-cpa-01***cdef' -application-id app-review-1
"$BIN" key accept --repo "$REPO" --home "$H" < "$WORK/mcp.json" > /dev/null 2>&1

section "C-21b envelope sealed to another key, with a gateway address present"
HM="$WORK/h-mismatch"; mkdir -p "$HM"
"$BIN" key gen --repo org/repo-a --home "$HM" --base-url "$BASE" > /dev/null 2>&1
"$BIN" key gen --repo org/repo-b --home "$HM" --base-url "$BASE" > /dev/null 2>&1
KB=$(find "$HM/keys" -type f | grep -i 'repo-b' | head -1); SB=$(basename "$KB" .key)
cp "$ENVELOPE" "$HM/envelopes/$SB.json"
"$BIN" key exec --runtime claude --repo org/repo-b --home "$HM" -- true > /tmp/o 2> /tmp/e; C=$?
say "wrong-key envelope exit=$C err=$(head -1 /tmp/e)"
[ $C -ne 0 ] && grep -qi "mismatch\|decrypt" /tmp/e && assert "C-21b wrong-key envelope is refused readably" 0 || assert "C-21b wrong-key envelope is refused readably" 1
grep -qF "$SECRET" /tmp/o /tmp/e && assert "C-21b error has no plaintext" 1 || assert "C-21b error has no plaintext" 0

section "C-22b -- argument boundaries (JSON argv)"
"$BIN" key exec --runtime claude --repo "$REPO" --home "$H" -- "$PROBE" '%s' 'a b' '--flag' > "$WORK/c22.out" 2> /tmp/e
sed -n 's/^ARGV_JSON=//p' "$WORK/c22.out" > "$WORK/argv.json"
say "argv_json=$(cat "$WORK/argv.json")"
node_json() { :; }
grep -qF '"a b"' "$WORK/argv.json" && assert "C-22b the spaced argument stayed one argument" 0 || assert "C-22b the spaced argument stayed one argument" 1
grep -qF '"--flag"' "$WORK/argv.json" && assert "C-22b the --flag argument survived" 0 || assert "C-22b the --flag argument survived" 1
grep -qF '"%s"' "$WORK/argv.json" && assert "C-22b the format argument survived" 0 || assert "C-22b the format argument survived" 1

section "C-15b clean captures: damaged files in status"
HC="$WORK/h-corrupt"; cp -a "$H" "$HC"; printf 'not json at all' > "$HC/envelopes/$STEM.json"
"$BIN" key status --home "$HC" > /tmp/o 2> /tmp/e; CD=$?
say "corrupt envelope status exit=$CD"; say "stdout:"; sed -n '1,20p' /tmp/o >> "$LOG"; say "stderr:"; sed -n '1,10p' /tmp/e >> "$LOG"
grep -qi "panic:" /tmp/o /tmp/e && assert "C-15b no panic on a corrupt envelope" 1 || assert "C-15b no panic on a corrupt envelope" 0
if grep -qi "corrupt\|invalid\|unreadable\|damaged" /tmp/o /tmp/e; then assert "C-15b status marks the corrupt envelope" 0; else assert "C-15b status marks the corrupt envelope (it does not; plan does not require it)" 1; fi
HK="$WORK/home-badkey"; cp -a "$H" "$HK"; : > "$HK/keys/$STEM.key"
"$BIN" key status --home "$HK" > /tmp/o 2> /tmp/e; CK=$?
say "empty key status exit=$CK"; say "stdout:"; sed -n '1,20p' /tmp/o >> "$LOG"; say "stderr:"; sed -n '1,10p' /tmp/e >> "$LOG"
grep -qi "panic:" /tmp/o /tmp/e && assert "C-15b no panic on an empty key" 1 || assert "C-15b no panic on an empty key" 0
if grep -qi "corrupt\|invalid\|unreadable\|damaged" /tmp/o /tmp/e; then assert "C-15b status marks the empty key" 0; else assert "C-15b status marks the empty key (it does not; plan does not require it)" 1; fi
"$BIN" key exec --runtime claude --repo "$REPO" --home "$HK" -- true > /tmp/o 2> /tmp/e; CE=$?
say "exec with an empty key exit=$CE err=$(head -1 /tmp/e)"
grep -q "key_unreadable" /tmp/e && assert "C-15b exec still refuses an empty key readably" 0 || assert "C-15b exec still refuses an empty key readably" 1

section "C-18b signal-terminated child"
"$BIN" key exec --runtime claude --repo "$REPO" --home "$H" -- sh -c 'kill -TERM $$' > /tmp/o 2> /tmp/e; CS=$?
say "SIGTERM child exit=$CS stderr=$(head -1 /tmp/e)"
[ "$CS" -eq 1 ] && assert "C-18b signal child reports 1 (Go ExitCode -1 -> 1; plan says no translation)" 0 || assert "C-18b signal child reports $CS" 1

section "C-23b over-wide envelope permissions"
H8="$WORK/h-env644"; cp -a "$H" "$H8"; chmod 644 "$H8/envelopes/$STEM.json"
"$BIN" key exec --runtime claude --repo "$REPO" --home "$H8" -- "$PROBE" > /tmp/o 2> /tmp/e; CW=$?
say "wide envelope exit=$CW stderr=$(head -3 /tmp/e | tr '\n' ' ')"
say "implementation permission checks mentioning envelope:"
grep -rn 'Mode()\|Perm()\|0o077\|chmod' /src/internal/keyclient/accept.go /src/internal/keyclient/exec.go /src/internal/keyclient/keystore.go 2>/dev/null >> "$LOG" || true

say ""
say "RESULT case-02b FAILS=$FAILS"
exit $FAILS
