#!/bin/bash
# T5-S14 C-41: follow docs/repo-key-client.md as written, in a clean temporary HOME and
# OMC_HOME, and record every command that does not behave as the runbook says. The binary
# path substitutes for the installed `oh-my-cpa`; nothing else is added that the page does
# not tell the reader to do.
set -u
BIN=/evidence/oh-my-cpa
HELPER=/evidence/keytest-helper
PROBE=/evidence/keytest-probe
LOG=/evidence/case-logs/case-05-doc-walk.log
mkdir -p /evidence/case-logs; : > "$LOG"
FAILS=0
say() { echo "$@" | tee -a "$LOG"; }
section() { echo "" | tee -a "$LOG"; echo "===== $* =====" | tee -a "$LOG"; }
assert() { if [ "$2" = "0" ]; then echo "ASSERT $1 PASS" | tee -a "$LOG"; else echo "ASSERT $1 FAIL" | tee -a "$LOG"; FAILS=$((FAILS+1)); fi; }
export TZ=UTC ASSISTANT_NO_SEND=1
WORK=$(mktemp -d /tmp/t5s14-case05.XXXXXX)
export HOME="$WORK/home"; mkdir -p "$HOME"
export OMC_HOME="$WORK/omc-home"          # the runbook's documented way to point the tool
REPO=org/repo; BASE=http://127.0.0.1:8317
SECRET='sk-cpa-0123456789abcdef0123456789abcdef'; printf '%s' "$SECRET" > "$WORK/s.txt"
say "Followed page: docs/repo-key-client.md; OMC_HOME=$OMC_HOME"

section "Step 1: oh-my-cpa key --help"
"$BIN" key --help > "$WORK/help.out" 2> /tmp/e; H=$?
say "exit=$H subcommands=$(grep -E '^  (status|gen|accept|exec|install|purge) ' "$WORK/help.out" | awk '{print $1}' | tr '\n' ' ')"
[ $H -eq 0 ] && [ "$(grep -cE '^  (status|gen|accept|exec|install|purge) ' "$WORK/help.out")" -eq 6 ] && assert "page says six subcommands, help lists six" 0 || assert "page says six subcommands, help lists six" 1

section "Step 1: see what is already here (empty OMC_HOME)"
"$BIN" key status > "$WORK/s0.out" 2> "$WORK/s0.err"; S0=$?
say "status exit=$S0"; sed -n '1,12p' "$WORK/s0.out" >> "$LOG"
[ $S0 -eq 0 ] && grep -qi "no key" "$WORK/s0.out" && grep -q "key gen" "$WORK/s0.out" && assert "the page's empty-home answer matches" 0 || assert "the page's empty-home answer matches" 1

section "Step 2: oh-my-cpa key gen --repo org/repo --base-url ..."
"$BIN" key gen --repo "$REPO" --base-url "$BASE" > "$WORK/gen.out" 2> "$WORK/gen.err"; G=$?
say "exit=$G stdout_lines=$(wc -l < "$WORK/gen.out") fn=$(find "$OMC_HOME/keys" -type f | head -1)"; sed -n '1,5p' "$WORK/gen.err" >> "$LOG"
[ $G -eq 0 ] && [ "$(wc -l < "$WORK/gen.out")" -eq 1 ] && grep -q '^x25519:' "$WORK/gen.out" && assert "stdout is the documented one public-key line" 0 || assert "stdout is the documented one public-key line" 1
grep -q "saved the gateway address" "$WORK/gen.err" && assert "gen remembers the gateway on stderr as documented" 0 || assert "gen remembers the gateway as documented" 1

section "Step 4: associate the application id"
"$BIN" key status --repo "$REPO" --application-id app-doc-1 > "$WORK/s1.out" 2> "$WORK/s1.err"; A=$?
say "exit=$A"; sed -n '1,12p' "$WORK/s1.out" >> "$LOG"
grep -qi "unknown" "$WORK/s1.out" && assert "the id is recorded as unknown, as the page says" 0 || assert "the id is recorded as unknown" 1

section "Step 5: hand the ciphertext result to key accept on stdin (heredoc shape)"
"$HELPER" -mode envelope -public-key-file "$WORK/gen.out" -plaintext-file "$WORK/s.txt" -out "$WORK/result.json" -mcp -key-mask 'sk-cpa-01***cdef' -application-id app-doc-1
"$BIN" key accept --repo "$REPO" --application-id app-doc-1 < "$WORK/result.json" > "$WORK/accept.out" 2> "$WORK/accept.err"; AC=$?
say "exit=$AC stdout=$(cat "$WORK/accept.out")"
[ $AC -eq 0 ] && grep -qE "^accepted .*: key sk-cpa-01\*\*\*cdef at envelopes/" "$WORK/accept.out" && assert "accept prints the documented line" 0 || assert "accept prints the documented line" 1
STEM=$(basename "$(find "$OMC_HOME/keys" -type f | head -1)" .key)
[ -f "$OMC_HOME/envelopes/$STEM.json" ] && [ "$(stat -c '%a' "$OMC_HOME/envelopes/$STEM.json")" = "600" ] && assert "the envelope lands under envelopes/ at 0600" 0 || assert "the envelope lands under envelopes/ at 0600" 1

section "Step 6: key exec with the documented variable shapes"
"$BIN" key exec --runtime claude --repo "$REPO" -- "$PROBE" > "$WORK/exec.out" 2> "$WORK/exec.err"; E=$?
say "exit=$E"; grep -E '^(ENV_NAMES|ANTHROPIC_BASE_URL)' "$WORK/exec.out" >> "$LOG"
[ $E -eq 0 ] && grep -q "ANTHROPIC_BASE_URL=$BASE" "$WORK/exec.out" && assert "claude child sees the documented root URL" 0 || assert "claude child sees the documented root URL" 1
grep -q "ANTHROPIC_AUTH_TOKEN_SHA256=$(sha256sum "$WORK/s.txt" | cut -d' ' -f1)" "$WORK/exec.out" && assert "claude child sees the documented token" 0 || assert "claude child sees the documented token" 1
"$BIN" key exec --runtime opencode --repo "$REPO" -- "$PROBE" > "$WORK/oc.out" 2> /tmp/e
grep -qE '^OPENCODE_OH_MY_CPA_BASE_URL='"$BASE"'/v1' "$WORK/oc.out" && assert "opencode child sees root+/v1 as the page promises" 0 || assert "opencode child sees root+/v1" 1
cp "$PROBE" "$WORK/codex"; chmod 755 "$WORK/codex"
"$BIN" key exec --runtime codex --repo "$REPO" -- "$WORK/codex" > "$WORK/cx.out" 2> /tmp/e
grep -qF 'model_providers.oh-my-cpa.base_url="'"$BASE"'/v1"' "$WORK/cx.out" && assert "codex child sees root+/v1 as the page promises" 0 || assert "codex child sees root+/v1" 1

section "Step 7: install (codex) then purge/dry-run/purge --runtime"
mkdir -p "$HOME/.codex"; printf 'model_provider = "other"\n\n[model_providers.other]\nname = "Other"\n' > "$HOME/.codex/config.toml"
"$BIN" key install --runtime codex --repo "$REPO" > "$WORK/install.out" 2> "$WORK/install.err"; I=$?
say "install exit=$I"; sed -n '1,8p' "$WORK/install.err" >> "$LOG"
[ $I -eq 0 ] && grep -qE '^\[model_providers\.oh-my-cpa\]' "$HOME/.codex/config.toml" && assert "install writes the managed Codex table" 0 || assert "install writes the managed Codex table" 1
grep -qi "plaintext" "$WORK/install.err" && assert "install warns about plaintext as the page says" 0 || assert "install warns about plaintext" 1
"$BIN" key purge --repo "$REPO" --dry-run > "$WORK/dry.out" 2> "$WORK/dry.err"; D=$?
BEFORE=$(sha256sum "$HOME/.codex/config.toml" | cut -d' ' -f1)
[ $D -eq 0 ] && grep -qi "would remove\|nothing was changed" "$WORK/dry.out" && [ "$BEFORE" = "$(sha256sum "$HOME/.codex/config.toml" | cut -d' ' -f1)" ] && assert "dry-run changes nothing and says so" 0 || assert "dry-run changes nothing and says so" 1
"$BIN" key purge --repo "$REPO" --runtime codex > "$WORK/pr.out" 2> "$WORK/pr.err"; P=$?
say "purge --runtime exit=$P stdout=$(tr '\n' ' ' < "$WORK/pr.out")"
[ $P -eq 0 ] && [ -f "$(find "$OMC_HOME/keys" -type f | head -1)" ] && assert "purge --runtime keeps the key and envelope, as documented" 0 || assert "purge --runtime keeps the key and envelope" 1
"$BIN" key purge --repo "$REPO" > "$WORK/pf.out" 2> "$WORK/pf.err"; F=$?
[ $F -eq 0 ] && grep -qi "CPA.*still valid" "$WORK/pf.out" && grep -qi "local material cleared" "$WORK/pf.out" && assert "full purge prints the documented local-only line" 0 || assert "full purge prints the documented local-only line" 1
"$BIN" key exec --runtime claude --repo "$REPO" -- true > /tmp/o 2> /tmp/e; X=$?
[ $X -eq 4 ] && grep -q "key_not_found" /tmp/e && assert "the documented proof after purge (exec exits 4)" 0 || assert "the documented proof after purge (exec exits 4)" 1

say ""
say "RESULT case-05 FAILS=$FAILS"
exit $FAILS
