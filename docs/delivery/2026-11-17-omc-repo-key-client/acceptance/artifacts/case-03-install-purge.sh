#!/bin/bash
# T5-S14 review-only process cases: key install and key purge (C-26..C-29, C-31..C-33,
# C-35). Real user-level file shapes are seeded under a temporary HOME; the real
# ~/.claude, ~/.codex and ~/.config/opencode are never touched.
set -u
BIN=/evidence/oh-my-cpa
HELPER=/evidence/keytest-helper
LOG=/evidence/case-logs/case-03-install-purge.log
mkdir -p /evidence/case-logs; : > "$LOG"
FAILS=0
say() { echo "$@" | tee -a "$LOG"; }
section() { echo "" | tee -a "$LOG"; echo "===== $* =====" | tee -a "$LOG"; }
assert() { if [ "$2" = "0" ]; then echo "ASSERT $1 PASS" | tee -a "$LOG"; else echo "ASSERT $1 FAIL" | tee -a "$LOG"; FAILS=$((FAILS+1)); fi; }
export TZ=UTC ASSISTANT_NO_SEND=1

WORK=$(mktemp -d /tmp/t5s14-case03.XXXXXX)
UHOME="$WORK/userhome"; mkdir -p "$UHOME/.claude" "$UHOME/.codex" "$UHOME/.config/opencode"
export HOME="$UHOME"
H="$WORK/omc-home"; REPO=org/fixture-repo; BASE=http://127.0.0.1:18317
SECRET='sk-cpa-0123456789abcdef0123456789abcdef'
printf '%s' "$SECRET" > "$WORK/secret.txt"
mkdir -p "$H"
"$BIN" key gen --repo "$REPO" --home "$H" --base-url "$BASE" > "$WORK/pub.txt" 2>/dev/null
KEYFILE=$(find "$H/keys" -type f | head -1); STEM=$(basename "$KEYFILE" .key)
"$HELPER" -mode envelope -public-key-file "$WORK/pub.txt" -plaintext-file "$WORK/secret.txt" -out "$WORK/mcp.json" -mcp -key-mask 'sk-cpa-01***cdef' -application-id a1
"$BIN" key accept --repo "$REPO" --home "$H" < "$WORK/mcp.json" >/dev/null 2>&1
say "WORK=$WORK"

# ---- seed pre-existing user configuration ----------------------------------
SET="$UHOME/.claude/settings.json"
CODEX="$UHOME/.codex/config.toml"
OC="$UHOME/.config/opencode/opencode.json"
printf '{"env":{"OTHER":"1"},"permissions":{"allow":["Bash"]},"model":"opus"}' > "$SET"
cat > "$CODEX" <<'EOF'
model_provider = "other"

[model_providers.other]
name = "Other"
base_url = "http://127.0.0.1:19999/v1"
EOF
printf '{"theme":"dark","model":"gpt-x","provider":{"cpa":{"npm":"@ai-sdk/openai-compatible","options":{"baseURL":"http://127.0.0.1:19998/v1"}}}}' > "$OC"
SET0=$(sha256sum "$SET" | cut -d' ' -f1); CODEX0=$(sha256sum "$CODEX" | cut -d' ' -f1); OC0=$(sha256sum "$OC" | cut -d' ' -f1)

section "C-26 first claude install: backup, warning, precise edit"
"$BIN" key install --runtime claude --repo "$REPO" --home "$H" --base-url "$BASE" > "$WORK/i1.out" 2> "$WORK/i1.err"; C1=$?
cp "$WORK/i1.out" /evidence/case-logs/install-claude.out; cp "$WORK/i1.err" /evidence/case-logs/install-claude.err
say "install claude exit=$C1"; say "stdout:"; sed -n '1,10p' "$WORK/i1.out" >> "$LOG"; say "stderr:"; sed -n '1,20p' "$WORK/i1.err" >> "$LOG"
[ $C1 -eq 0 ] && assert "C-26 install exits zero" 0 || assert "C-26 install exits zero" 1
grep -qi "plaintext" "$WORK/i1.err" && assert "C-26 warning says plaintext goes to disk" 0 || assert "C-26 warning says plaintext goes to disk" 1
grep -qi "purge" "$WORK/i1.err" && assert "C-26 warning names purge" 0 || assert "C-26 warning names purge" 1
BACKUP=$(find "$H/backups" -type f | head -1)
[ -n "$BACKUP" ] && assert "C-26 a backup exists" 0 || assert "C-26 a backup exists" 1
[ "$(sha256sum "$BACKUP" | cut -d' ' -f1)" = "$SET0" ] && assert "C-26 backup is the original file byte for byte" 0 || assert "C-26 backup is the original file byte for byte" 1
P=$(stat -c '%a' "$BACKUP"); [ "$P" = "600" ] && assert "C-26 backup is 0600" 0 || assert "C-26 backup is 0600 (got $P)" 1
P=$(stat -c '%a' "$SET"); [ "$P" = "600" ] && assert "C-26 target config is 0600" 0 || assert "C-26 target config is 0600 (got $P)" 1
grep -qF "\"ANTHROPIC_AUTH_TOKEN\":\"$SECRET\"" "$SET" && assert "C-26 the two env keys are written" 0 || assert "C-26 the two env keys are written" 1
grep -qF "\"ANTHROPIC_BASE_URL\":\"$BASE\"" "$SET" && assert "C-26 base URL is the gateway root" 0 || assert "C-26 base URL is the gateway root" 1
grep -qF '"OTHER":"1"' "$SET" && assert "C-26 the pre-existing env value is kept" 0 || assert "C-26 the pre-existing env value is kept" 1
grep -qF '"permissions"' "$SET" && grep -qF '"model"' "$SET" && assert "C-26 the other top-level keys are kept" 0 || assert "C-26 the other top-level keys are kept" 1

section "C-27 repeated install is idempotent"
SET1=$(sha256sum "$SET" | cut -d' ' -f1)
"$BIN" key install --runtime claude --repo "$REPO" --home "$H" > /dev/null 2> "$WORK/i2.err"; C2=$?
SET2=$(sha256sum "$SET" | cut -d' ' -f1)
NCOUNT=$(grep -o 'ANTHROPIC_AUTH_TOKEN' "$SET" | wc -l)
BCOUNT=$(find "$H/backups" -type f | wc -l)
say "second install exit=$C2 file_same=$([ "$SET1" = "$SET2" ] && echo yes || echo no) token_keys=$NCOUNT backups=$BCOUNT"
[ $C2 -eq 0 ] && assert "C-27 second install exits zero" 0 || assert "C-27 second install exits zero" 1
[ "$SET1" = "$SET2" ] && assert "C-27 config content unchanged" 0 || assert "C-27 config content unchanged" 1
[ "$NCOUNT" -eq 1 ] && assert "C-27 no duplicate key in the JSON" 0 || assert "C-27 no duplicate key in the JSON" 1
[ "$BCOUNT" -ge 1 ] && assert "C-27 backups are kept (plan: no automatic cap)" 0 || assert "C-27 backups are kept" 1

section "C-28 one slot per runtime, other configuration preserved"
"$BIN" key install --runtime codex --repo "$REPO" --home "$H" --base-url "$BASE" > "$WORK/ic.out" 2> "$WORK/ic.err"; CC=$?
say "install codex exit=$CC"; sed -n '1,12p' "$WORK/ic.out" >> "$LOG"; sed -n '1,12p' "$WORK/ic.err" >> "$LOG"
[ $CC -eq 0 ] && assert "C-28 codex install exits zero" 0 || assert "C-28 codex install exits zero" 1
grep -q '^\[model_providers.oh-my-cpa\]' "$CODEX" && assert "C-28 codex managed table is present" 0 || assert "C-28 codex managed table is present" 1
grep -q '^\[model_providers.other\]' "$CODEX" && assert "C-28 codex keeps the other provider" 0 || assert "C-28 codex keeps the other provider" 1
grep -q '^model_provider = "oh-my-cpa"' "$CODEX" && assert "C-28 codex points model_provider at ours" 0 || assert "C-28 codex points model_provider at ours" 1
grep -qF "experimental_bearer_token = \"$SECRET\"" "$CODEX" && assert "C-28 codex persists the key in the managed table" 0 || assert "C-28 codex persists the key in the managed table" 1

"$BIN" key install --runtime opencode --repo "$REPO" --home "$H" --base-url "$BASE" > "$WORK/io.out" 2> "$WORK/io.err"; COC=$?
say "install opencode exit=$COC"; sed -n '1,12p' "$WORK/io.out" >> "$LOG"; sed -n '1,12p' "$WORK/io.err" >> "$LOG"
[ $COC -eq 0 ] && assert "C-28 opencode install exits zero" 0 || assert "C-28 opencode install exits zero" 1
grep -qF '"oh-my-cpa"' "$OC" && assert "C-28 opencode provider.oh-my-cpa is present" 0 || assert "C-28 opencode provider.oh-my-cpa is present" 1
grep -qF '"cpa"' "$OC" && assert "C-28 opencode keeps provider.cpa" 0 || assert "C-28 opencode keeps provider.cpa" 1
grep -qF '"theme":"dark"' "$OC" && assert "C-28 opencode keeps the theme" 0 || assert "C-28 opencode keeps the theme" 1
grep -qF '"models"' "$OC" && assert "C-28 opencode writes no models map" 1 || assert "C-28 opencode writes no models map" 0
grep -qF "\"apiKey\":\"$SECRET\"" "$OC" && assert "C-28 opencode persists the apiKey" 0 || assert "C-28 opencode persists the apiKey" 1

mkdir -p "$WORK/proj/.claude"; printf '{"env":{"SENTINEL":"1"}}' > "$WORK/proj/.claude/settings.json"
( cd "$WORK/proj" && "$BIN" key install --runtime claude --repo "$REPO" --home "$H" --base-url "$BASE" > /dev/null 2>&1 )
grep -qF 'SENTINEL' "$WORK/proj/.claude/settings.json" && assert "C-28 project-level .claude/settings.json untouched" 0 || assert "C-28 project-level .claude/settings.json untouched" 1

section "C-29 missing and damaged target files"
UH2="$WORK/uhome2"; mkdir -p "$UH2"
HOME="$UH2" "$BIN" key install --runtime claude --repo "$REPO" --home "$H" --base-url "$BASE" > /tmp/o 2> /tmp/e; CM=$?
say "missing claude settings exit=$CM stderr=$(head -1 /tmp/e)"
[ $CM -eq 0 ] && [ -f "$UH2/.claude/settings.json" ] && assert "C-29 a missing target file is created" 0 || assert "C-29 a missing target file is created" 1
printf 'this is [ not toml' > "$UH2/.codex/config.toml" 2>/dev/null || { mkdir -p "$UH2/.codex"; printf 'this is [ not toml' > "$UH2/.codex/config.toml"; }
BAD0=$(sha256sum "$UH2/.codex/config.toml" | cut -d' ' -f1)
HOME="$UH2" "$BIN" key install --runtime codex --repo "$REPO" --home "$H" --base-url "$BASE" > /tmp/o 2> /tmp/e; CX=$?
say "corrupt codex toml exit=$CX err=$(head -1 /tmp/e)"
[ $CX -ne 0 ] && [ "$(sha256sum "$UH2/.codex/config.toml" | cut -d' ' -f1)" = "$BAD0" ] && assert "C-29 a corrupt TOML is refused and left byte-identical" 0 || assert "C-29 a corrupt TOML is refused and left byte-identical" 1
printf '{not json' > "$UH2/.config/opencode/opencode.json" 2>/dev/null || { mkdir -p "$UH2/.config/opencode"; printf '{not json' > "$UH2/.config/opencode/opencode.json"; }
OJ0=$(sha256sum "$UH2/.config/opencode/opencode.json" | cut -d' ' -f1)
HOME="$UH2" "$BIN" key install --runtime opencode --repo "$REPO" --home "$H" --base-url "$BASE" > /tmp/o 2> /tmp/e; CJ=$?
say "corrupt opencode json exit=$CJ err=$(head -1 /tmp/e)"
[ $CJ -ne 0 ] && [ "$(sha256sum "$UH2/.config/opencode/opencode.json" | cut -d' ' -f1)" = "$OJ0" ] && assert "C-29 corrupt JSON is refused and left byte-identical" 0 || assert "C-29 corrupt JSON is refused and left byte-identical" 1
UH3="$WORK/uhome3"; mkdir -p "$UH3"
HOME="$UH3" "$BIN" key install --runtime opencode --repo "$REPO" --home "$H" --base-url "$BASE" > /tmp/o 2> /tmp/e; CD=$?
[ $CD -eq 0 ] && [ -f "$UH3/.config/opencode/opencode.json" ] && assert "C-29 a missing config directory is created" 0 || assert "C-29 a missing config directory is created" 1

section "C-31 purge clears this repository only"
H2="$WORK/home-second"; mkdir -p "$H2"
"$BIN" key gen --repo org/other-repo --home "$H2" --base-url "$BASE" > /dev/null 2>&1
"$BIN" key gen --repo "$REPO" --home "$H2" --base-url "$BASE" > "$WORK/pub2.txt" 2>/dev/null
K2=$(find "$H2/keys" -type f | grep 'fixture-repo' | head -1); S2=$(basename "$K2" .key)
"$HELPER" -mode envelope -public-key-file "$WORK/pub2.txt" -plaintext-file "$WORK/secret.txt" -out "$WORK/mcp2.json" -mcp -key-mask 'sk-cpa-01***cdef' -application-id a2
"$BIN" key accept --repo "$REPO" --home "$H2" < "$WORK/mcp2.json" >/dev/null 2>&1
OTHER=$(sha256sum "$(find "$H2/keys" -type f | grep 'other-repo')" | cut -d' ' -f1)
"$BIN" key purge --repo "$REPO" --home "$H2" > "$WORK/p1.out" 2> "$WORK/p1.err"; CP=$?
say "purge exit=$CP"; sed -n '1,20p' "$WORK/p1.out" >> "$LOG"; sed -n '1,20p' "$WORK/p1.err" >> "$LOG"
[ $CP -eq 0 ] && assert "C-31 purge exits zero" 0 || assert "C-31 purge exits zero" 1
[ ! -f "$K2" ] && [ ! -f "$H2/envelopes/$S2.json" ] && assert "C-31 private key and envelope are gone" 0 || assert "C-31 private key and envelope are gone" 1
grep -q 'fixture-repo' "$H2/state.json" 2>/dev/null && assert "C-31 state record is gone" 1 || assert "C-31 state record is gone" 0
[ "$(sha256sum "$(find "$H2/keys" -type f | grep 'other-repo')" | cut -d' ' -f1)" = "$OTHER" ] && assert "C-31 another repository's key is untouched" 0 || assert "C-31 another repository's key is untouched" 1
grep -q "CPA" "$WORK/p1.out" && assert "C-31 says the CPA key is still valid" 0 || assert "C-31 says the CPA key is still valid" 1

section "C-32 purge removes the installed values from all three runtimes"
"$BIN" key purge --repo "$REPO" --home "$H" > "$WORK/p2.out" 2> "$WORK/p2.err"; CP2=$?
say "purge installed exit=$CP2"; sed -n '1,30p' "$WORK/p2.out" >> "$LOG"; sed -n '1,30p' "$WORK/p2.err" >> "$LOG"
[ $CP2 -eq 0 ] && assert "C-32 purge exits zero" 0 || assert "C-32 purge exits zero" 1
grep -qF "$SECRET" "$SET" "$CODEX" "$OC" && assert "C-32 the key is gone from all three configs" 1 || assert "C-32 the key is gone from all three configs" 0
grep -qF '"OTHER":"1"' "$SET" && grep -qF '"permissions"' "$SET" && assert "C-32 claude keeps the other settings" 0 || assert "C-32 claude keeps the other settings" 1
grep -q '^\[model_providers.other\]' "$CODEX" && assert "C-32 codex keeps the other provider" 0 || assert "C-32 codex keeps the other provider" 1
grep -q 'oh-my-cpa' "$CODEX" && assert "C-32 codex managed table is gone" 1 || assert "C-32 codex managed table is gone" 0
grep -qF '"cpa"' "$OC" && assert "C-32 opencode keeps provider.cpa" 0 || assert "C-32 opencode keeps provider.cpa" 1
grep -qF '"oh-my-cpa"' "$OC" && assert "C-32 opencode provider is gone" 1 || assert "C-32 opencode provider is gone" 0
[ ! -f "$KEYFILE" ] && [ ! -f "$H/envelopes/$STEM.json" ] && assert "C-32 private key and envelope are gone" 0 || assert "C-32 private key and envelope are gone" 1
LEFT=$(grep -rlF "$SECRET" "$H/backups" 2>/dev/null | wc -l)
say "backups still holding the key: $LEFT (plan removes those containing this key)"
[ "$LEFT" -eq 0 ] && assert "C-32 no backup still holds the plaintext" 0 || assert "C-32 no backup still holds the plaintext" 1

section "C-33 exec after purge fails readably; purge is idempotent"
"$BIN" key exec --runtime claude --repo "$REPO" --home "$H" -- true > /tmp/o 2> /tmp/e; C33=$?
say "exec after purge exit=$C33 err=$(head -1 /tmp/e)"
[ $C33 -eq 4 ] && grep -q "key_not_found" /tmp/e && assert "C-33 exec after purge exits 4 with key_not_found" 0 || assert "C-33 exec after purge exits 4 with key_not_found" 1
"$BIN" key purge --repo "$REPO" --home "$H" > "$WORK/p3.out" 2> "$WORK/p3.err"; C33R=$?
say "repeat purge exit=$C33R out=$(tr '\n' ' ' < "$WORK/p3.out")"
[ $C33R -eq 0 ] && assert "C-33 repeated purge is idempotent" 0 || assert "C-33 repeated purge is idempotent (got $C33R)" 1

section "C-35 purge path safety and an unwritable target"
H3="$WORK/home-safe"; mkdir -p "$H3"
"$BIN" key gen --repo org/safe-repo --home "$H3" --base-url "$BASE" > /dev/null 2>&1
KEEP=$(find "$H3/keys" -type f | head -1); KEEP0=$(sha256sum "$KEEP" | cut -d' ' -f1)
"$BIN" key purge --repo '../../../etc/passwd' --home "$H3" > /tmp/o 2> /tmp/e; C35A=$?
say "purge ../ exit=$C35A err=$(head -1 /tmp/e)"
[ $C35A -ne 0 ] && [ "$(sha256sum "$KEEP" | cut -d' ' -f1)" = "$KEEP0" ] && assert "C-35 a traversal repo is refused and deletes nothing" 0 || assert "C-35 a traversal repo is refused and deletes nothing" 1
"$BIN" key purge --repo '' --home "$H3" > /tmp/o 2> /tmp/e; C35B=$?
[ $C35B -eq 2 ] && assert "C-35 an empty repo is a usage error" 0 || assert "C-35 an empty repo is a usage error (got $C35B)" 1
[ -f "$KEEP" ] && assert "C-35 the kept key survives both rejected purges" 0 || assert "C-35 the kept key survives both rejected purges" 1

# Unwritable keys directory: run as nobody so the permission bits bind.
NB="$WORK/nb"; mkdir -p "$NB"; chmod 755 "$WORK"; chown nobody "$NB"
asnobody() { su nobody -s /bin/sh -c "$1"; }
asnobody "HOME=$NB/home $BIN key gen --repo org/locked --home $NB/home" >/dev/null 2>&1
chown -R nobody "$NB"
chmod 500 "$NB/home/keys"
asnobody "$BIN key purge --repo org/locked --home $NB/home" > /tmp/o 2> /tmp/e; C35C=$?
chmod 700 "$NB/home/keys"
say "purge with an unwritable keys dir exit=$C35C err=$(head -2 /tmp/e | tr '\n' ' ')"
[ $C35C -ne 0 ] && grep -qi "permission\|denied\|unavailable\|failed" /tmp/e && assert "C-35 an unwritable target fails readably" 0 || assert "C-35 an unwritable target fails readably" 1
[ -f "$(find "$NB/home/keys" -type f | head -1)" ] && assert "C-35 nothing outside OMC_HOME was removed" 0 || assert "C-35 nothing outside OMC_HOME was removed" 1

say ""
say "RESULT case-03 FAILS=$FAILS"
exit $FAILS
