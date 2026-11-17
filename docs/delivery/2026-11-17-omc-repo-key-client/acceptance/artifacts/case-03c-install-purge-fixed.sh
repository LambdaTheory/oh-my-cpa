#!/bin/bash
# T5-S14 corrected install/purge cases: case-03 mixed scenarios in one home (a later
# install repointed a runtime slot and orphaned an earlier file), and it compared
# pretty-printed JSON with exact single-line strings. Each scenario here gets a fresh
# key home, and JSON is checked tolerantly.
set -u
BIN=/evidence/oh-my-cpa
HELPER=/evidence/keytest-helper
LOG=/evidence/case-logs/case-03c-install-purge-fixed.log
mkdir -p /evidence/case-logs; : > "$LOG"
FAILS=0
say() { echo "$@" | tee -a "$LOG"; }
section() { echo "" | tee -a "$LOG"; echo "===== $* =====" | tee -a "$LOG"; }
assert() { if [ "$2" = "0" ]; then echo "ASSERT $1 PASS" | tee -a "$LOG"; else echo "ASSERT $1 FAIL" | tee -a "$LOG"; FAILS=$((FAILS+1)); fi; }
export TZ=UTC ASSISTANT_NO_SEND=1
WORK=$(mktemp -d /tmp/t5s14-case03c.XXXXXX); chmod 755 "$WORK"
SECRET='sk-cpa-0123456789abcdef0123456789abcdef'; printf '%s' "$SECRET" > "$WORK/secret.txt"
REPO=org/fixture-repo; BASE=http://127.0.0.1:18317

fresh_home() { # fresh_home <dir> -> prints public key file; key+envelope under <dir>
  local h="$1"; mkdir -p "$h"
  "$BIN" key gen --repo "$REPO" --home "$h" --base-url "$BASE" > "$WORK/pub.txt" 2>/dev/null
  "$HELPER" -mode envelope -public-key-file "$WORK/pub.txt" -plaintext-file "$WORK/secret.txt" -out "$WORK/mcp.json" -mcp -key-mask 'sk-cpa-01***cdef' -application-id a1
  "$BIN" key accept --repo "$REPO" --home "$h" < "$WORK/mcp.json" >/dev/null 2>&1
}

section "C-28 three runtimes, other configuration preserved (fresh home)"
UH="$WORK/uhome28"; mkdir -p "$UH/.claude" "$UH/.codex" "$UH/.config/opencode"
SET="$UH/.claude/settings.json"; CODEX="$UH/.codex/config.toml"; OC="$UH/.config/opencode/opencode.json"
printf '{"env":{"OTHER":"1"},"permissions":{"allow":["Bash"]},"model":"opus"}' > "$SET"
printf 'model_provider = "other"\n\n[model_providers.other]\nname = "Other"\nbase_url = "http://127.0.0.1:19999/v1"\n' > "$CODEX"
printf '{"theme":"dark","model":"gpt-x","provider":{"cpa":{"npm":"@ai-sdk/openai-compatible","options":{"baseURL":"http://127.0.0.1:19998/v1"}}}}' > "$OC"
H4="$WORK/h28"; fresh_home "$H4"
HOME="$UH" "$BIN" key install --runtime claude --repo "$REPO" --home "$H4" --base-url "$BASE" > /tmp/o 2> /tmp/e; say "claude exit=$?"
HOME="$UH" "$BIN" key install --runtime codex --repo "$REPO" --home "$H4" --base-url "$BASE" > /tmp/o 2> /tmp/e; say "codex exit=$?"
HOME="$UH" "$BIN" key install --runtime opencode --repo "$REPO" --home "$H4" --base-url "$BASE" > /tmp/o 2> /tmp/e; say "opencode exit=$?"
cp "$SET" /evidence/case-logs/configs/28-settings.json; cp "$CODEX" /evidence/case-logs/configs/28-codex.toml; cp "$OC" /evidence/case-logs/configs/28-opencode.json
grep -qE '"OTHER": *"1"' "$SET" && assert "C-28 claude keeps env.OTHER" 0 || assert "C-28 claude keeps env.OTHER" 1
grep -qE '"permissions"' "$SET" && grep -qE '"model": *"opus"' "$SET" && assert "C-28 claude keeps permissions/model" 0 || assert "C-28 claude keeps permissions/model" 1
grep -qF "$SECRET" "$SET" && assert "C-28 claude persists the plaintext" 0 || assert "C-28 claude persists the plaintext" 1
grep -qE '^\[model_providers\.oh-my-cpa\]' "$CODEX" && assert "C-28 codex managed table present" 0 || assert "C-28 codex managed table present" 1
grep -qE '^\[model_providers\.other\]' "$CODEX" && assert "C-28 codex keeps the other provider" 0 || assert "C-28 codex keeps the other provider" 1
grep -qE '^model_provider = "oh-my-cpa"' "$CODEX" && assert "C-28 codex points model_provider at ours" 0 || assert "C-28 codex points model_provider at ours" 1
grep -qF "$SECRET" "$CODEX" && assert "C-28 codex persists the plaintext" 0 || assert "C-28 codex persists the plaintext" 1
grep -qE '"theme": *"dark"' "$OC" && assert "C-28 opencode keeps the theme" 0 || assert "C-28 opencode keeps the theme" 1
grep -qE '"oh-my-cpa"' "$OC" && grep -qE '"apiKey": *"'"$SECRET"'"' "$OC" && assert "C-28 opencode persists provider.oh-my-cpa.options.apiKey" 0 || assert "C-28 opencode persists provider.oh-my-cpa.options.apiKey" 1
grep -qE '"cpa"' "$OC" && assert "C-28 opencode keeps provider.cpa" 0 || assert "C-28 opencode keeps provider.cpa" 1
grep -qE '"models"' "$OC" && assert "C-28 opencode writes no models map" 1 || assert "C-28 opencode writes no models map" 0
mkdir -p "$WORK/proj/.claude"; printf '{"env":{"SENTINEL":"1"}}' > "$WORK/proj/.claude/settings.json"
( cd "$WORK/proj" && HOME="$UH" "$BIN" key install --runtime claude --repo "$REPO" --home "$H4" --base-url "$BASE" > /dev/null 2>&1 )
grep -qF SENTINEL "$WORK/proj/.claude/settings.json" && assert "C-28 project-level settings untouched" 0 || assert "C-28 project-level settings untouched" 1

section "C-27 repeated install on a fresh slot"
H5="$WORK/h27"; fresh_home "$H5"
UH27="$WORK/uh27"; mkdir -p "$UH27/.claude"; printf '{"env":{"OTHER":"1"}}' > "$UH27/.claude/settings.json"
HOME="$UH27" "$BIN" key install --runtime claude --repo "$REPO" --home "$H5" --base-url "$BASE" > /dev/null 2>&1
S1=$(sha256sum "$UH27/.claude/settings.json" | cut -d' ' -f1)
HOME="$UH27" "$BIN" key install --runtime claude --repo "$REPO" --home "$H5" > /dev/null 2> /tmp/e; C2=$?
S2=$(sha256sum "$UH27/.claude/settings.json" | cut -d' ' -f1)
N=$(grep -o 'ANTHROPIC_AUTH_TOKEN' "$UH27/.claude/settings.json" | wc -l)
B=$(find "$H5/backups" -type f | wc -l)
say "second install exit=$C2 same=$([ "$S1" = "$S2" ] && echo yes || echo no) token_keys=$N backups=$B"
[ $C2 -eq 0 ] && [ "$S1" = "$S2" ] && [ "$N" -eq 1 ] && assert "C-27 repeated install is idempotent (one slot, same content)" 0 || assert "C-27 repeated install is idempotent" 1
[ "$B" -ge 1 ] && assert "C-27 backups are kept (no automatic cap)" 0 || assert "C-27 backups are kept" 1

section "C-32 purge removes the installed values (clean sequence)"
H6="$WORK/h32"; fresh_home "$H6"
UH32="$WORK/uh32"; mkdir -p "$UH32/.claude" "$UH32/.codex" "$UH32/.config/opencode"
printf '{"env":{"OTHER":"1"},"permissions":{"allow":["Bash"]},"model":"opus"}' > "$UH32/.claude/settings.json"
printf 'model_provider = "other"\n\n[model_providers.other]\nname = "Other"\n' > "$UH32/.codex/config.toml"
printf '{"theme":"dark","provider":{"cpa":{"npm":"x"}}}' > "$UH32/.config/opencode/opencode.json"
for r in claude codex opencode; do HOME="$UH32" "$BIN" key install --runtime "$r" --repo "$REPO" --home "$H6" --base-url "$BASE" > /dev/null 2>&1; done
HOME="$UH32" "$BIN" key purge --repo "$REPO" --home "$H6" > /tmp/o 2> /tmp/e; C32=$?
cp "$UH32/.claude/settings.json" /evidence/case-logs/configs/32-settings-after.json
cp "$UH32/.codex/config.toml" /evidence/case-logs/configs/32-codex-after.toml
cp "$UH32/.config/opencode/opencode.json" /evidence/case-logs/configs/32-opencode-after.json
say "purge exit=$C32"; sed -n '1,15p' /tmp/o >> "$LOG"; sed -n '1,10p' /tmp/e >> "$LOG"
[ $C32 -eq 0 ] && assert "C-32 purge exits zero" 0 || assert "C-32 purge exits zero" 1
grep -qF "$SECRET" "$UH32/.claude/settings.json" "$UH32/.codex/config.toml" "$UH32/.config/opencode/opencode.json" && assert "C-32 key gone from all three" 1 || assert "C-32 key gone from all three" 0
grep -qE '"OTHER": *"1"' "$UH32/.claude/settings.json" && assert "C-32 claude keeps the other settings" 0 || assert "C-32 claude keeps the other settings" 1
grep -qE '^\[model_providers\.other\]' "$UH32/.codex/config.toml" && assert "C-32 codex keeps the other provider" 0 || assert "C-32 codex keeps the other provider" 1
grep -q 'oh-my-cpa' "$UH32/.codex/config.toml" && assert "C-32 codex managed block is gone" 1 || assert "C-32 codex managed block is gone" 0
grep -qE '"cpa"' "$UH32/.config/opencode/opencode.json" && assert "C-32 opencode keeps provider.cpa" 0 || assert "C-32 opencode keeps provider.cpa" 1
grep -qE '"oh-my-cpa"' "$UH32/.config/opencode/opencode.json" && assert "C-32 opencode provider is gone" 1 || assert "C-32 opencode provider is gone" 0
LEFT=$(grep -rlF "$SECRET" "$H6/backups" 2>/dev/null | wc -l)
say "backups still holding the key: $LEFT"
[ "$LEFT" -eq 0 ] && assert "C-32 no backup still holds the plaintext" 0 || assert "C-32 no backup still holds the plaintext" 1

section "C-29 missing target files (fresh key home)"
H7="$WORK/h29"; fresh_home "$H7"
UH7="$WORK/uh7"; mkdir -p "$UH7"
HOME="$UH7" "$BIN" key install --runtime claude --repo "$REPO" --home "$H7" --base-url "$BASE" > /tmp/o 2> /tmp/e; M1=$?
say "missing ~/.claude exit=$M1 err=$(head -2 /tmp/e | tr '\n' ' ')"
if [ $M1 -eq 0 ] && [ -f "$UH7/.claude/settings.json" ]; then assert "C-29 missing file is created" 0; elif [ $M1 -ne 0 ] && [ -s /tmp/e ]; then assert "C-29 missing file is refused readably (case allows either)" 0; else assert "C-29 missing file handling" 1; fi
UH8="$WORK/uh8"; mkdir -p "$UH8"
HOME="$UH8" "$BIN" key install --runtime opencode --repo "$REPO" --home "$H7" --base-url "$BASE" > /tmp/o 2> /tmp/e; M2=$?
say "missing ~/.config/opencode exit=$M2 err=$(head -2 /tmp/e | tr '\n' ' ')"
if [ $M2 -eq 0 ] && [ -f "$UH8/.config/opencode/opencode.json" ]; then assert "C-29 missing config directory is created" 0; elif [ $M2 -ne 0 ] && [ -s /tmp/e ]; then assert "C-29 missing config directory is refused readably (case allows either)" 0; else assert "C-29 missing config directory handling" 1; fi

section "C-29 corrupted target files are left byte-identical"
UH9="$WORK/uh9"; mkdir -p "$UH9/.codex"
printf 'this is [ not toml\n' > "$UH9/.codex/config.toml"; B0=$(sha256sum "$UH9/.codex/config.toml" | cut -d' ' -f1)
HOME="$UH9" "$BIN" key install --runtime codex --repo "$REPO" --home "$H7" --base-url "$BASE" > /tmp/o 2> /tmp/e; X1=$?
B1=$(sha256sum "$UH9/.codex/config.toml" | cut -d' ' -f1)
say "junk TOML exit=$X1 err=$(head -2 /tmp/e | tr '\n' ' ') unchanged=$([ "$B0" = "$B1" ] && echo yes || echo no)"
cp "$UH9/.codex/config.toml" /evidence/case-logs/configs/29-junk-toml-after.toml
if [ $X1 -ne 0 ] && [ "$B0" = "$B1" ]; then assert "C-29 junk TOML is refused and unchanged" 0; else assert "C-29 junk TOML is refused and unchanged (accepted or modified)" 1; fi
printf '[unterminated\nkey =\n' > "$UH9/.codex/config.toml"; B2=$(sha256sum "$UH9/.codex/config.toml" | cut -d' ' -f1)
HOME="$UH9" "$BIN" key install --runtime codex --repo "$REPO" --home "$H7" --base-url "$BASE" > /tmp/o 2> /tmp/e; X2=$?
B3=$(sha256sum "$UH9/.codex/config.toml" | cut -d' ' -f1)
say "unterminated TOML exit=$X2 err=$(head -2 /tmp/e | tr '\n' ' ') unchanged=$([ "$B2" = "$B3" ] && echo yes || echo no)"
if [ $X2 -ne 0 ] && [ "$B2" = "$B3" ]; then assert "C-29 unterminated TOML is refused and unchanged" 0; else assert "C-29 unterminated TOML is refused and unchanged (accepted or modified)" 1; fi
printf '{not json' > "$UH9/.config/opencode/opencode.json" 2>/dev/null || { mkdir -p "$UH9/.config/opencode"; printf '{not json' > "$UH9/.config/opencode/opencode.json"; }
J0=$(sha256sum "$UH9/.config/opencode/opencode.json" | cut -d' ' -f1)
HOME="$UH9" "$BIN" key install --runtime opencode --repo "$REPO" --home "$H7" --base-url "$BASE" > /tmp/o 2> /tmp/e; X3=$?
J1=$(sha256sum "$UH9/.config/opencode/opencode.json" | cut -d' ' -f1)
say "corrupt JSON exit=$X3 err=$(head -2 /tmp/e | tr '\n' ' ') unchanged=$([ "$J0" = "$J1" ] && echo yes || echo no)"
[ $X3 -ne 0 ] && [ "$J0" = "$J1" ] && assert "C-29 corrupt JSON is refused and unchanged" 0 || assert "C-29 corrupt JSON is refused and unchanged" 1

section "C-35 unwritable keys directory: readable purge_pending"
NB="$WORK/nb"; mkdir -p "$NB"; chown nobody "$NB"
su nobody -s /bin/sh -c "HOME=$NB/home $BIN key gen --repo org/locked --home $NB/home" >/dev/null 2>&1
chown -R nobody "$NB"; chmod 500 "$NB/home/keys"
su nobody -s /bin/sh -c "$BIN key purge --repo org/locked --home $NB/home" > /tmp/o 2> /tmp/e; L=$?
chmod 700 "$NB/home/keys"
say "unwritable purge exit=$L stdout=$(tr '\n' ' ' < /tmp/o) stderr=$(tr '\n' ' ' < /tmp/e)"
[ $L -ne 0 ] && grep -q "purge_pending" /tmp/e && assert "C-35 unwritable target reports purge_pending readably" 0 || assert "C-35 unwritable target reports purge_pending readably" 1
[ -f "$(find "$NB/home/keys" -type f | head -1)" ] && assert "C-35 nothing outside OMC_HOME was removed" 0 || assert "C-35 nothing outside OMC_HOME was removed" 1

say ""
say "RESULT case-03c FAILS=$FAILS"
exit $FAILS
