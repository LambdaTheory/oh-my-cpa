#!/bin/bash
# T5-S14 focused re-run of install/purge behaviour questions raised by case-03.
set -u
BIN=/evidence/oh-my-cpa
HELPER=/evidence/keytest-helper
LOG=/evidence/case-logs/case-03b-focus.log
CFG=/evidence/case-logs/configs
mkdir -p /evidence/case-logs; rm -rf "$CFG"; mkdir -p "$CFG"; : > "$LOG"
say() { echo "$@" | tee -a "$LOG"; }
section() { echo "" | tee -a "$LOG"; echo "===== $* =====" | tee -a "$LOG"; }
export TZ=UTC ASSISTANT_NO_SEND=1
WORK=$(mktemp -d /tmp/t5s14-case03b.XXXXXX)
UHOME="$WORK/userhome"; mkdir -p "$UHOME/.claude" "$UHOME/.codex" "$UHOME/.config/opencode"
export HOME="$UHOME"
H="$WORK/omc-home"; REPO=org/fixture-repo; BASE=http://127.0.0.1:18317
SECRET='sk-cpa-0123456789abcdef0123456789abcdef'; printf '%s' "$SECRET" > "$WORK/secret.txt"
mkdir -p "$H"
"$BIN" key gen --repo "$REPO" --home "$H" --base-url "$BASE" > "$WORK/pub.txt" 2>/dev/null
KEYFILE=$(find "$H/keys" -type f | head -1); STEM=$(basename "$KEYFILE" .key)
"$HELPER" -mode envelope -public-key-file "$WORK/pub.txt" -plaintext-file "$WORK/secret.txt" -out "$WORK/mcp.json" -mcp -key-mask 'sk-cpa-01***cdef' -application-id a1
"$BIN" key accept --repo "$REPO" --home "$H" < "$WORK/mcp.json" >/dev/null 2>&1
SET="$UHOME/.claude/settings.json"; CODEX="$UHOME/.codex/config.toml"; OC="$UHOME/.config/opencode/opencode.json"
printf '{"env":{"OTHER":"1"},"permissions":{"allow":["Bash"]},"model":"opus"}' > "$SET"
cat > "$CODEX" <<'EOF'
model_provider = "other"

[model_providers.other]
name = "Other"
base_url = "http://127.0.0.1:19999/v1"
EOF
printf '{"theme":"dark","model":"gpt-x","provider":{"cpa":{"npm":"@ai-sdk/openai-compatible","options":{"baseURL":"http://127.0.0.1:19998/v1"}}}}' > "$OC"

section "after installs"
"$BIN" key install --runtime claude --repo "$REPO" --home "$H" --base-url "$BASE" > "$CFG/i-claude.out" 2> "$CFG/i-claude.err"; say "claude exit=$?"
"$BIN" key install --runtime codex --repo "$REPO" --home "$H" --base-url "$BASE" > "$CFG/i-codex.out" 2> "$CFG/i-codex.err"; say "codex exit=$?"
"$BIN" key install --runtime opencode --repo "$REPO" --home "$H" --base-url "$BASE" > "$CFG/i-opencode.out" 2> "$CFG/i-opencode.err"; say "opencode exit=$?"
cp "$SET" "$CFG/settings.after-install.json"; cp "$CODEX" "$CFG/config.after-install.toml"; cp "$OC" "$CFG/opencode.after-install.json"

section "after purge"
"$BIN" key purge --repo "$REPO" --home "$H" > "$CFG/purge.out" 2> "$CFG/purge.err"; say "purge exit=$?"
cp "$SET" "$CFG/settings.after-purge.json"; cp "$CODEX" "$CFG/config.after-purge.toml"; cp "$OC" "$CFG/opencode.after-purge.json"
say "--- settings.after-purge.json"; sed -n '1,10p' "$SET" >> "$LOG"
say "--- config.after-purge.toml"; sed -n '1,30p' "$CODEX" >> "$LOG"
say "--- opencode.after-purge.json"; sed -n '1,10p' "$OC" >> "$LOG"

section "missing parent directory"
UH2="$WORK/uhome2"; mkdir -p "$UH2"
HOME="$UH2" "$BIN" key install --runtime claude --repo "$REPO" --home "$H" --base-url "$BASE" > "$CFG/missing-claude.out" 2> "$CFG/missing-claude.err"; M1=$?
say "missing ~/.claude exit=$M1 stderr:"; sed -n '1,10p' "$CFG/missing-claude.err" >> "$LOG"
HOME="$UH2" "$BIN" key install --runtime opencode --repo "$REPO" --home "$H" --base-url "$BASE" > "$CFG/missing-oc.out" 2> "$CFG/missing-oc.err"; M2=$?
say "missing ~/.config/opencode exit=$M2 stderr:"; sed -n '1,10p' "$CFG/missing-oc.err" >> "$LOG"

section "corrupt codex TOML"
UH3="$WORK/uhome3"; mkdir -p "$UH3/.codex"
printf 'this is [ not toml\n' > "$UH3/.codex/config.toml"
cp "$UH3/.codex/config.toml" "$CFG/corrupt-before.toml"
HOME="$UH3" "$BIN" key install --runtime codex --repo "$REPO" --home "$H" --base-url "$BASE" > "$CFG/corrupt-codex.out" 2> "$CFG/corrupt-codex.err"; X=$?
say "corrupt toml exit=$X stderr:"; sed -n '1,15p' "$CFG/corrupt-codex.err" >> "$LOG"
say "--- file after:"; sed -n '1,30p' "$UH3/.codex/config.toml" >> "$LOG"
cp "$UH3/.codex/config.toml" "$CFG/corrupt-after.toml"
printf '[unterminated\nkey =\n' > "$UH3/.codex/config.toml"
HOME="$UH3" "$BIN" key install --runtime codex --repo "$REPO" --home "$H" --base-url "$BASE" > "$CFG/corrupt2-codex.out" 2> "$CFG/corrupt2-codex.err"; X2=$?
say "clearly invalid toml exit=$X2 stderr:"; sed -n '1,15p' "$CFG/corrupt2-codex.err" >> "$LOG"
cp "$UH3/.codex/config.toml" "$CFG/corrupt2-after.toml"

section "unwritable keys dir purge message"
NB="$WORK/nb"; mkdir -p "$NB"; chmod 755 "$WORK"; chown nobody "$NB"
su nobody -s /bin/sh -c "HOME=$NB/home $BIN key gen --repo org/locked --home $NB/home" >/dev/null 2>&1
chown -R nobody "$NB"; chmod 500 "$NB/home/keys"
su nobody -s /bin/sh -c "$BIN key purge --repo org/locked --home $NB/home" > "$CFG/locked.out" 2> "$CFG/locked.err"; L=$?
chmod 700 "$NB/home/keys"
say "unwritable purge exit=$L stdout:"; sed -n '1,10p' "$CFG/locked.out" >> "$LOG"; say "stderr:"; sed -n '1,10p' "$CFG/locked.err" >> "$LOG"
say ""
say "RESULT case-03b done"
exit 0
