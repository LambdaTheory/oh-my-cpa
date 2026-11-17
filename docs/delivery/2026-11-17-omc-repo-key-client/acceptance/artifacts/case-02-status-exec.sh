#!/bin/bash
# T5-S14 review-only process cases: key status and key exec (C-11..C-25), with a real
# envelope sealed to the key `key gen` produced. All state is under /tmp; the container
# never reaches the network.
set -u
BIN=/evidence/oh-my-cpa
HELPER=/evidence/keytest-helper
PROBE=/evidence/keytest-probe
LOG=/evidence/case-logs/case-02-status-exec.log
JSONDIR=/evidence/case-logs/status-json
mkdir -p /evidence/case-logs; : > "$LOG"; rm -rf "$JSONDIR"; mkdir -p "$JSONDIR"
FAILS=0
say() { echo "$@" | tee -a "$LOG"; }
section() { echo "" | tee -a "$LOG"; echo "===== $* =====" | tee -a "$LOG"; }
assert() { if [ "$2" = "0" ]; then echo "ASSERT $1 PASS" | tee -a "$LOG"; else echo "ASSERT $1 FAIL" | tee -a "$LOG"; FAILS=$((FAILS+1)); fi; }
show() { echo "--- $1 exit=$2" | tee -a "$LOG"; sed -n "1,${3:-20}p" /tmp/e >> "$LOG"; }

export TZ=UTC ASSISTANT_NO_SEND=1
WORK=$(mktemp -d /tmp/t5s14-case02.XXXXXX)
UHOME="$WORK/userhome"; mkdir -p "$UHOME"
export HOME="$UHOME"
H="$WORK/omc-home"
REPO=org/fixture-repo
BASE=http://127.0.0.1:18317
SECRET='sk-cpa-0123456789abcdef0123456789abcdef'
printf '%s' "$SECRET" > "$WORK/secret.txt"
SECRET_SHA=$(sha256sum "$WORK/secret.txt" | cut -d' ' -f1)
SECRET_LEN=$(wc -c < "$WORK/secret.txt")
say "WORK=$WORK secret_len=$SECRET_LEN"

# ---- fixture: a real key and a real envelope sealed to it -------------------
mkdir -p "$H"
"$BIN" key gen --repo "$REPO" --home "$H" --base-url "$BASE" > "$WORK/pub.txt" 2> "$WORK/gen.err"
KEYFILE=$(find "$H/keys" -type f | head -1)
STEM=$(basename "$KEYFILE" .key)
ENVELOPE="$H/envelopes/$STEM.json"
MASK='sk-cpa-01***cdef'
"$HELPER" -mode envelope -public-key-file "$WORK/pub.txt" -plaintext-file "$WORK/secret.txt" \
  -out "$WORK/envelope-mcp.json" -mcp -key-mask "$MASK" -application-id app-review-1
"$BIN" key accept --repo "$REPO" --home "$H" < "$WORK/envelope-mcp.json" > "$WORK/accept.out" 2> "$WORK/accept.err"
AC=$?
say "fixture accept exit=$AC stdout=$(cat "$WORK/accept.out") stderr=$(cat "$WORK/accept.err")"

section "C-11 three readable states"
"$BIN" key status --home "$WORK/empty-home" > /tmp/o 2> /tmp/e; CA=$?
cp /tmp/o "$JSONDIR/status-A-no-key.txt"
say "A (no key) exit=$CA:"; sed -n '1,20p' /tmp/o >> "$LOG"
grep -qi "no key\|no keys" /tmp/o && assert "C-11 A says there is no key" 0 || assert "C-11 A says there is no key" 1
grep -q "key gen" /tmp/o && assert "C-11 A points at key gen" 0 || assert "C-11 A points at key gen" 1

HB="$WORK/home-b"; mkdir -p "$HB"
"$BIN" key gen --repo "$REPO" --home "$HB" > /dev/null 2>&1
"$BIN" key status --home "$HB" > /tmp/o 2> /tmp/e; CB=$?
cp /tmp/o "$JSONDIR/status-B-key-only.txt"
say "B (key without envelope) exit=$CB:"; sed -n '1,20p' /tmp/o >> "$LOG"
grep -qi "envelope" /tmp/o && assert "C-11 B mentions the envelope" 0 || assert "C-11 B mentions the envelope" 1
grep -qi "accept\|ciphertext" /tmp/o && assert "C-11 B says how to get one" 0 || assert "C-11 B says how to get one" 1
grep -qi "ready" /tmp/o && assert "C-11 B does not claim ready" 1 || assert "C-11 B does not claim ready" 0

"$BIN" key status --home "$H" > /tmp/o 2> /tmp/e; CC=$?
cp /tmp/o "$JSONDIR/status-C-approved.txt"
say "C (key+envelope) exit=$CC:"; sed -n '1,20p' /tmp/o >> "$LOG"
grep -qi "approved" /tmp/o && assert "C-11 C shows approved" 0 || assert "C-11 C shows approved" 1
grep -qF "$SECRET" /tmp/o /tmp/e && assert "C-11 C shows no plaintext" 1 || assert "C-11 C shows no plaintext" 0
grep -qF "$MASK" /tmp/o && assert "C-11 C shows the mask" 0 || assert "C-11 C shows the mask" 1
grep -qF "$(od -An -tx1 "$KEYFILE" | tr -d ' \n')" /tmp/o /tmp/e && assert "C-11 C shows no private key" 1 || assert "C-11 C shows no private key" 0

section "C-12 machine-readable status"
"$BIN" key status --home "$WORK/empty-home" --json > "$JSONDIR/status-A.json" 2> /tmp/e; echo "A exit=$?" >> "$LOG"
"$BIN" key status --home "$HB" --json > "$JSONDIR/status-B.json" 2> /tmp/e; echo "B exit=$?" >> "$LOG"
"$BIN" key status --home "$H" --json > "$JSONDIR/status-C.json" 2> /tmp/e; echo "C exit=$?" >> "$LOG"
"$BIN" key status --home "$H" --json > "$JSONDIR/status-C2.json" 2> /tmp/e; echo "C2 exit=$?" >> "$LOG"
grep -qF "$SECRET" "$JSONDIR"/status-*.json && assert "C-12 no plaintext in JSON" 1 || assert "C-12 no plaintext in JSON" 0

section "C-14 the approval source works offline; --remote is explicit"
START=$(date +%s%N)
"$BIN" key status --home "$H" > /tmp/o 2> /tmp/e; COFF=$?
END=$(date +%s%N)
ELAPSED_MS=$(( (END-START)/1000000 ))
say "offline status exit=$COFF elapsed_ms=$ELAPSED_MS"
grep -qi "approved" /tmp/o && assert "C-14 offline status still shows approved" 0 || assert "C-14 offline status still shows approved" 1
env -u OMCPA_SERVER_URL -u OMCPA_CPA_MANAGEMENT_KEY "$BIN" key status --remote --home "$H" > /tmp/o 2> /tmp/e; CRM=$?
say "remote without config exit=$CRM err=$(head -2 /tmp/e | tr '\n' ' ')"
[ $CRM -eq 3 ] && assert "C-14 --remote without config exits 3" 0 || assert "C-14 --remote without config exits 3 (got $CRM)" 1
grep -q "remote_not_configured" /tmp/e && assert "C-14 --remote names remote_not_configured" 0 || assert "C-14 --remote names remote_not_configured" 1
START=$(date +%s%N)
OMCPA_SERVER_URL=http://127.0.0.1:9 OMCPA_CPA_MANAGEMENT_KEY=local-synthetic-only "$BIN" key status --remote --home "$H" > /tmp/o 2> /tmp/e; CRU=$?
END=$(date +%s%N)
ELAPSED_MS=$(( (END-START)/1000000 ))
say "remote to a closed port exit=$CRU elapsed_ms=$ELAPSED_MS err=$(head -2 /tmp/e | tr '\n' ' ')"
[ $CRU -ne 0 ] && assert "C-14 unreachable remote fails nonzero" 0 || assert "C-14 unreachable remote fails nonzero" 1
grep -qi "remote\|unavailable\|refused" /tmp/e && assert "C-14 unreachable remote error is readable" 0 || assert "C-14 unreachable remote error is readable" 1
grep -qi "approved" /tmp/o && assert "C-14 local view survives an unreachable remote" 0 || assert "C-14 local view survives an unreachable remote" 1

section "C-15 damaged files are named, not panicked on"
HC="$WORK/home-corrupt"; cp -a "$H" "$HC"
printf 'not json at all' > "$HC/envelopes/$STEM.json"
"$BIN" key status --home "$HC" > /tmp/o 2> /tmp/e; CD=$?
say "corrupt envelope status exit=$CD:"; sed -n '1,25p' /tmp/o >> "$LOG"
grep -qi "panic:" /tmp/o /tmp/e && assert "C-15 no panic on a corrupt envelope" 1 || assert "C-15 no panic on a corrupt envelope" 0
grep -qi "corrupt\|invalid\|unreadable" /tmp/o /tmp/e && assert "C-15 corrupt envelope is distinguished" 0 || assert "C-15 corrupt envelope is distinguished" 1
HK="$WORK/home-badkey"; cp -a "$H" "$HK"
: > "$HK/keys/$STEM.key"
"$BIN" key status --home "$HK" > /tmp/o 2> /tmp/e; CK=$?
say "empty private key status exit=$CK:"; sed -n '1,25p' /tmp/o >> "$LOG"
grep -qi "panic:" /tmp/o /tmp/e && assert "C-15 no panic on an empty key" 1 || assert "C-15 no panic on an empty key" 0
grep -qi "corrupt\|invalid\|unreadable" /tmp/o /tmp/e && assert "C-15 empty key is distinguished" 0 || assert "C-15 empty key is distinguished" 1

section "C-16 only the child sees the variables"
env | grep -E '^(ANTHROPIC_|OPENCODE_|OMC_REPO_KEY|CODEX_)' > "$WORK/env-before.txt"
"$BIN" key exec --runtime claude --repo "$REPO" --home "$H" -- "$PROBE" > "$WORK/c16.out" 2> "$WORK/c16.err"; C16=$?
env | grep -E '^(ANTHROPIC_|OPENCODE_|OMC_REPO_KEY|CODEX_)' > "$WORK/env-after.txt"
cp "$WORK/c16.out" /evidence/case-logs/exec-claude-probe.out
cp "$WORK/c16.err" /evidence/case-logs/exec-claude-probe.err
diff "$WORK/env-before.txt" "$WORK/env-after.txt" > /tmp/diff 2>&1; assert "C-16 parent environment is unchanged" $?
grep -qF "ANTHROPIC_BASE_URL=$BASE" "$WORK/c16.out" && assert "C-16 child sees the configured base URL" 0 || assert "C-16 child sees the configured base URL" 1
grep -qF "ANTHROPIC_AUTH_TOKEN_SHA256=$SECRET_SHA" "$WORK/c16.out" && assert "C-16 token decrypts to the exact plaintext" 0 || assert "C-16 token decrypts to the exact plaintext" 1
grep -qF "ANTHROPIC_AUTH_TOKEN_LEN=$SECRET_LEN" "$WORK/c16.out" && assert "C-16 token length matches" 0 || assert "C-16 token length matches" 1
grep -qF "ANTHROPIC_API_KEY" "$WORK/c16.out" && assert "C-16 inherited decoy key is gone" 1 || assert "C-16 inherited decoy key is gone" 0

section "C-17 stdout/stderr/disk/proc carry no plaintext"
"$BIN" key exec --runtime claude --repo "$REPO" --home "$H" -- "$PROBE" > "$WORK/c17.out" 2> "$WORK/c17.err"; C17=$?
grep -qF "$SECRET" "$WORK/c17.out" "$WORK/c17.err" && assert "C-17 no plaintext on the exec streams" 1 || assert "C-17 no plaintext on the exec streams" 0
BADFILES=""
while IFS= read -r f; do
  [ "$f" = "$WORK/secret.txt" ] && continue
  grep -qF "$SECRET" "$f" && BADFILES="$BADFILES $f"
done < <(find "$WORK" -type f)
say "files containing the plaintext: secret.txt${BADFILES:- (none else)}"
[ -z "$BADFILES" ] && assert "C-17 no file other than the test's own secret.txt has it" 0 || assert "C-17 no file other than the test's own secret.txt has it" 1
"$BIN" key exec --runtime claude --repo "$REPO" --home "$H" -- sh -c 'sleep 2' &
EXECPID=$!
sleep 1
ARGVHIT=""
for cmdline in /proc/[0-9]*/cmdline; do
  tr '\0' ' ' < "$cmdline" 2>/dev/null | grep -qF "$SECRET" && ARGVHIT="$ARGVHIT $cmdline"
done
wait $EXECPID
[ -z "$ARGVHIT" ] && assert "C-17 no process argv carries the plaintext" 0 || assert "C-17 no process argv carries the plaintext" 1

section "C-18 child status passes through; own failures are distinct"
for spec in '42:42' '0:0'; do
  want=${spec%%:*}; cmd=${spec##*:}
  "$BIN" key exec --runtime claude --repo "$REPO" --home "$H" -- sh -c "exit $cmd" >/dev/null 2>&1
  got=$?
  [ "$got" -eq "$want" ] && assert "C-18 child exit $cmd passes through" 0 || assert "C-18 child exit $cmd passes through (got $got)" 1
done
"$BIN" key exec --runtime claude --repo "$REPO" --home "$H" -- sh -c 'kill -TERM $$' >/dev/null 2>&1
GOT=$?
[ "$GOT" -eq 143 ] && assert "C-18 SIGTERM child reports 143" 0 || assert "C-18 SIGTERM child reports 143 (got $GOT)" 1
"$BIN" key exec --runtime claude --repo "$REPO" --home "$WORK/empty-home" -- true > /tmp/o 2> /tmp/e
GOT=$?
[ "$GOT" -eq 4 ] && assert "C-18 own failure uses exit 4, not the child's" 0 || assert "C-18 own failure uses exit 4 (got $GOT)" 1

section "C-19 each runtime gets its own variables"
grep -q "ENV_NAMES=" "$WORK/c16.out" && sed -n 's/^ENV_NAMES=//p' "$WORK/c16.out" | tee -a "$LOG"
CLAUDE_NAMES=$(sed -n 's/^ENV_NAMES=//p' "$WORK/c16.out")
echo "$CLAUDE_NAMES" | grep -q "OPENCODE_" && assert "C-19 claude child has no OPENCODE_*" 1 || assert "C-19 claude child has no OPENCODE_*" 0
echo "$CLAUDE_NAMES" | grep -q "OMC_REPO_KEY" && assert "C-19 claude child has no OMC_REPO_KEY" 1 || assert "C-19 claude child has no OMC_REPO_KEY" 0

cp "$PROBE" "$WORK/codex"; chmod 755 "$WORK/codex"
"$BIN" key exec --runtime codex --repo "$REPO" --home "$H" -- "$WORK/codex" arg1 > "$WORK/c19-codex.out" 2> "$WORK/c19-codex.err"; C19C=$?
cp "$WORK/c19-codex.out" /evidence/case-logs/exec-codex-probe.out
say "codex exec exit=$C19C"; sed -n '1,30p' "$WORK/c19-codex.out" >> "$LOG"
for override in 'model_provider="oh-my-cpa"' 'model_providers.oh-my-cpa.name="Oh My CPA"' "model_providers.oh-my-cpa.base_url=\"$BASE/v1\"" 'model_providers.oh-my-cpa.env_key="OMC_REPO_KEY"' 'model_providers.oh-my-cpa.wire_api="responses"'; do
  grep -qF "$override" "$WORK/c19-codex.out" && assert "C-19 codex override $override" 0 || assert "C-19 codex override $override" 1
done
grep -qF "OMC_REPO_KEY_SHA256=$SECRET_SHA" "$WORK/c19-codex.out" && assert "C-19 codex key arrives through OMC_REPO_KEY" 0 || assert "C-19 codex key arrives through OMC_REPO_KEY" 1
sed -n 's/^ARGV=//p' "$WORK/c19-codex.out" | grep -qF "$SECRET" && assert "C-19 codex argv has no plaintext" 1 || assert "C-19 codex argv has no plaintext" 0
sed -n 's/^ENV_NAMES=//p' "$WORK/c19-codex.out" | grep -q "ANTHROPIC_" && assert "C-19 codex child has no ANTHROPIC_*" 1 || assert "C-19 codex child has no ANTHROPIC_*" 0

mkdir -p "$UHOME/.config/opencode"
printf '{"theme":"dark","provider":{"other":{"npm":"@ai-sdk/openai"}}}' > "$UHOME/.config/opencode/opencode.json"
OPENCODE_CONFIG="$UHOME/.config/opencode/opencode.json" "$BIN" key exec --runtime opencode --repo "$REPO" --home "$H" -- "$PROBE" > "$WORK/c19-oc.out" 2> "$WORK/c19-oc.err"; C19O=$?
cp "$WORK/c19-oc.out" /evidence/case-logs/exec-opencode-probe.out
say "opencode exec exit=$C19O"; sed -n '1,30p' "$WORK/c19-oc.out" >> "$LOG"
grep -q "OPENCODE_CONFIG_CONTENT_PRESENT\|OPENCODE_CONFIG_CONTENT_LEN" "$WORK/c19-oc.out" && assert "C-19 opencode content is injected" 0 || assert "C-19 opencode content is injected" 1
grep -q "OPENCODE_CONFIG_DECODES=1" "$WORK/c19-oc.out" && assert "C-19 opencode content is valid JSON" 0 || assert "C-19 opencode content is valid JSON" 1
grep -q "OPENCODE_PROVIDER_OH_MY_CPA=1" "$WORK/c19-oc.out" && assert "C-19 opencode provider.oh-my-cpa is present" 0 || assert "C-19 opencode provider.oh-my-cpa is present" 1
grep -q "OPENCODE_PROVIDER_OTHER=1" "$WORK/c19-oc.out" && assert "C-19 opencode keeps the other provider" 0 || assert "C-19 opencode keeps the other provider" 1
grep -q "OPENCODE_THEME=dark" "$WORK/c19-oc.out" && assert "C-19 opencode keeps the theme" 0 || assert "C-19 opencode keeps the theme" 1
grep -qF "OPENCODE_OH_MY_CPA_BASE_URL=$BASE/v1" "$WORK/c19-oc.out" && assert "C-19 opencode base URL gets /v1" 0 || assert "C-19 opencode base URL gets /v1" 1
grep -qF "OPENCODE_OH_MY_CPA_APIKEY_SHA256=$SECRET_SHA" "$WORK/c19-oc.out" && assert "C-19 opencode apiKey is the exact plaintext" 0 || assert "C-19 opencode apiKey is the exact plaintext" 1
sed -n 's/^ENV_NAMES=//p' "$WORK/c19-oc.out" | grep -q "ANTHROPIC_" && assert "C-19 opencode child has no ANTHROPIC_*" 1 || assert "C-19 opencode child has no ANTHROPIC_*" 0
"$BIN" key exec --runtime nope --repo "$REPO" --home "$H" -- true > /tmp/o 2> /tmp/e; CU=$?
[ $CU -eq 2 ] && assert "C-19 unknown runtime exits 2" 0 || assert "C-19 unknown runtime exits 2 (got $CU)" 1
grep -q "claude" /tmp/e && grep -q "codex" /tmp/e && grep -q "opencode" /tmp/e && assert "C-19 unknown runtime lists the choices" 0 || assert "C-19 unknown runtime lists the choices" 1

section "C-20 exec writes nothing"
find "$H" "$UHOME" -type f -printf '%p %s %T@\n' | sort > "$WORK/before.txt"
"$BIN" key exec --runtime claude --repo "$REPO" --home "$H" -- true >/dev/null 2>&1
find "$H" "$UHOME" -type f -printf '%p %s %T@\n' | sort > "$WORK/after.txt"
diff "$WORK/before.txt" "$WORK/after.txt" > /tmp/diff 2>&1; assert "C-20 exec leaves the filesystem untouched" $?

section "C-21 error paths are readable and distinct"
H1="$WORK/h-noenv"; cp -a "$H" "$H1"; rm -f "$H1/envelopes/$STEM.json"
"$BIN" key exec --runtime claude --repo "$REPO" --home "$H1" -- true > /tmp/o 2> /tmp/e; C1=$?
say "no envelope exit=$C1 err=$(head -1 /tmp/e)"
[ $C1 -eq 4 ] && grep -q "envelope_missing" /tmp/e && assert "C-21 missing envelope is envelope_missing/4" 0 || assert "C-21 missing envelope is envelope_missing/4" 1
"$BIN" key exec --runtime claude --repo org/other-repo --home "$H" -- true > /tmp/o 2> /tmp/e; C2=$?
say "unknown repo exit=$C2 err=$(head -1 /tmp/e)"
[ $C2 -eq 4 ] && grep -q "key_not_found" /tmp/e && grep -q "key gen" /tmp/e && assert "C-21 unknown repo names key_not_found and the next step" 0 || assert "C-21 unknown repo names key_not_found and the next step" 1

HM="$WORK/h-mismatch"; mkdir -p "$HM"
"$BIN" key gen --repo org/repo-a --home "$HM" > /dev/null 2>&1
"$BIN" key gen --repo org/repo-b --home "$HM" > /dev/null 2>&1
KA=$(find "$HM/keys" -type f | grep -i 'repo-a' | head -1)
KB=$(find "$HM/keys" -type f | grep -i 'repo-b' | head -1)
SA=$(basename "$KA" .key); SB=$(basename "$KB" .key)
cp "$ENVELOPE" "$HM/envelopes/$SB.json"
"$BIN" key exec --runtime claude --repo org/repo-b --home "$HM" -- true > /tmp/o 2> /tmp/e; C3=$?
say "envelope for the wrong key exit=$C3 err=$(head -1 /tmp/e)"
[ $C3 -ne 0 ] && grep -qi "mismatch\|decrypt\|envelope" /tmp/e && assert "C-21 wrong-key envelope is refused readably" 0 || assert "C-21 wrong-key envelope is refused readably" 1
grep -qF "$SECRET" /tmp/o /tmp/e && assert "C-21 wrong-key error has no plaintext" 1 || assert "C-21 wrong-key error has no plaintext" 0

H4="$WORK/h-badenv"; cp -a "$H" "$H4"; printf 'not json' > "$H4/envelopes/$STEM.json"
"$BIN" key exec --runtime claude --repo "$REPO" --home "$H4" -- true > /tmp/o 2> /tmp/e; C4=$?
say "invalid envelope JSON exit=$C4 err=$(head -1 /tmp/e)"
[ $C4 -ne 0 ] && grep -q "envelope" /tmp/e && assert "C-21 invalid envelope JSON is envelope_*" 0 || assert "C-21 invalid envelope JSON is envelope_*" 1
H5="$WORK/h-badalg"; cp -a "$H" "$H5"
sed 's/"alg":"x25519-hkdf-sha256-aes256gcm"/"alg":"nope"/' "$ENVELOPE" > "$H5/envelopes/$STEM.json"
"$BIN" key exec --runtime claude --repo "$REPO" --home "$H5" -- true > /tmp/o 2> /tmp/e; C5=$?
say "altered alg exit=$C5 err=$(head -1 /tmp/e)"
[ $C5 -ne 0 ] && assert "C-21 altered alg is refused" 0 || assert "C-21 altered alg is refused" 1
H6="$WORK/h-badv"; cp -a "$H" "$H6"
sed 's/"v":1/"v":99/' "$ENVELOPE" > "$H6/envelopes/$STEM.json"
"$BIN" key exec --runtime claude --repo "$REPO" --home "$H6" -- true > /tmp/o 2> /tmp/e; C6=$?
say "altered version exit=$C6 err=$(head -1 /tmp/e)"
[ $C6 -ne 0 ] && assert "C-21 altered version is refused" 0 || assert "C-21 altered version is refused" 1
H7="$WORK/h-shortkey"; cp -a "$H" "$H7"; head -c 31 "$KEYFILE" > "$H7/keys/$STEM.key"
"$BIN" key exec --runtime claude --repo "$REPO" --home "$H7" -- true > /tmp/o 2> /tmp/e; C7=$?
say "31-byte private key exit=$C7 err=$(head -1 /tmp/e)"
[ $C7 -ne 0 ] && grep -q "key_unreadable" /tmp/e && assert "C-21 31-byte key is key_unreadable" 0 || assert "C-21 31-byte key is key_unreadable" 1
[ $C1 -ne $C3 ] && assert "C-21 'no envelope' and 'cannot open' use different exits" 0 || assert "C-21 'no envelope' and 'cannot open' use different exits" 1

section "C-22 -- passes arguments through unchanged"
"$BIN" key exec --runtime claude --repo "$REPO" --home "$H" -- "$PROBE" '%s' 'a b' '--flag' > "$WORK/c22.out" 2> /tmp/e
sed -n 's/^ARGV=//p' "$WORK/c22.out" > "$WORK/c22.argv"
grep -qF "'a b' --flag" "$WORK/c22.argv" && assert "C-22 spaces and --flag survive" 0 || assert "C-22 spaces and --flag survive" 1
say "argv=$(cat "$WORK/c22.argv")"
"$BIN" key exec --runtime claude --repo "$REPO" --home "$H" echo > /tmp/o 2> /tmp/e; C22=$?
say "missing -- exit=$C22 err=$(head -1 /tmp/e)"
[ $C22 -eq 2 ] && assert "C-22 a command without -- is a usage error" 0 || assert "C-22 a command without -- is a usage error (got $C22)" 1

section "C-23 permissions of state, key, envelope; over-wide envelope"
for f in "$KEYFILE" "$ENVELOPE" "$H/state.json" "$H/config.json"; do
  P=$(stat -c '%a' "$f"); say "$(basename "$f") mode=$P"
  [ "$P" = "600" ] && assert "C-23 $(basename "$f") is 600" 0 || assert "C-23 $(basename "$f") is 600 (got $P)" 1
done
H8="$WORK/h-env644"; cp -a "$H" "$H8"; chmod 644 "$H8/envelopes/$STEM.json"
"$BIN" key exec --runtime claude --repo "$REPO" --home "$H8" -- "$PROBE" > "$WORK/c23.out" 2> "$WORK/c23.err"; C23=$?
say "wide envelope exit=$C23 warn=$(grep -i 'permission\|mode\|world\|group' "$WORK/c23.err" | head -2 | tr '\n' ' ')"
if [ $C23 -eq 0 ] && grep -qi "permission\|mode\|world\|group" "$WORK/c23.err"; then assert "C-23 a world-readable envelope is warned about" 0; else assert "C-23 a world-readable envelope is warned about" 1; fi

section "C-24 concurrent execs"
for i in 1 2 3 4 5; do
  ( "$BIN" key exec --runtime claude --repo "$REPO" --home "$H" -- true >/dev/null 2>"$WORK/c24.$i.err"; echo $? > "$WORK/c24.$i.code" ) &
done
wait
CODES=$(cat "$WORK"/c24.*.code | tr '\n' ' ')
say "concurrent exit codes: $CODES"
[ "$(cat "$WORK"/c24.*.code | sort -u)" = "0" ] && assert "C-24 five concurrent execs all succeed" 0 || assert "C-24 five concurrent execs all succeed" 1
grep -h . "$WORK"/c24.*.err | grep -qi "panic\|half\|corrupt" && assert "C-24 no half-read envelope" 1 || assert "C-24 no half-read envelope" 0

section "C-25 log-surface scan"
HITS=$(grep -RF "$SECRET" "$WORK" 2>/dev/null | grep -v "^$WORK/secret.txt:" | head -5)
say "plaintext hits outside secret.txt: ${HITS:-none}"
[ -z "$HITS" ] && assert "C-25 no plaintext anywhere in the run directory" 0 || assert "C-25 no plaintext anywhere in the run directory" 1

say ""
say "RESULT case-02 FAILS=$FAILS"
exit $FAILS
