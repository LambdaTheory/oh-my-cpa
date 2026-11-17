#!/bin/bash
set -u
BIN=/evidence/oh-my-cpa; HELPER=/evidence/keytest-helper
W=$(mktemp -d /tmp/dbg.XXXXXX); UH=$W/uh; H=$W/h; mkdir -p "$UH/.claude" "$H"
printf '{"env":{"OTHER":"1"}}' > "$UH/.claude/settings.json"
printf 'sk-cpa-0123456789abcdef0123456789abcdef' > "$W/s"
"$BIN" key gen --repo org/repo --home "$H" --base-url http://127.0.0.1:18317 > "$W/pub" 2> "$W/generr"; echo "gen=$? err=$(cat "$W/generr")"
"$HELPER" -mode envelope -public-key-file "$W/pub" -plaintext-file "$W/s" -out "$W/m" -mcp -key-mask 'sk-cpa-01***cdef' -application-id a1; echo "helper=$?"
"$BIN" key accept --repo org/repo --home "$H" < "$W/m"; echo "accept=$?"
HOME="$UH" "$BIN" key install --runtime claude --repo org/repo --home "$H" --base-url http://127.0.0.1:18317; echo "install=$?"
echo '--- state:'; cat "$H/state.json"
echo '--- files:'; find "$H" -type f
echo '--- dry purge:'; "$BIN" key purge --repo org/repo --home "$H" --dry-run; echo "dry=$?"
