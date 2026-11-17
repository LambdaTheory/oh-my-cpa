#!/bin/bash
# C-15 clean re-run: the first attempt's grep matched the temporary directory name
# ("h-corrupt"), not the status output. This run uses neutral directory names.
set -u
BIN=/evidence/oh-my-cpa
HELPER=/evidence/keytest-helper
LOG=/evidence/case-logs/case-02c-corrupt.log
mkdir -p /evidence/case-logs; : > "$LOG"
say() { echo "$@" | tee -a "$LOG"; }
assert() { if [ "$2" = "0" ]; then echo "ASSERT $1 PASS" | tee -a "$LOG"; else echo "ASSERT $1 FAIL" | tee -a "$LOG"; fi; }
export TZ=UTC ASSISTANT_NO_SEND=1
WORK=$(mktemp -d /tmp/t5s14-case02c.XXXXXX); UHOME="$WORK/userhome"; mkdir -p "$UHOME"; export HOME="$UHOME"
H="$WORK/h1"; REPO=org/fixture-repo; BASE=http://127.0.0.1:18317
printf 'sk-cpa-0123456789abcdef0123456789abcdef' > "$WORK/s.txt"
mkdir -p "$H"
"$BIN" key gen --repo "$REPO" --home "$H" --base-url "$BASE" > "$WORK/pub.txt" 2>/dev/null
KEYFILE=$(find "$H/keys" -type f | head -1); STEM=$(basename "$KEYFILE" .key)
"$HELPER" -mode envelope -public-key-file "$WORK/pub.txt" -plaintext-file "$WORK/s.txt" -out "$WORK/mcp.json" -mcp -key-mask 'sk-cpa-01***cdef' -application-id a1
"$BIN" key accept --repo "$REPO" --home "$H" < "$WORK/mcp.json" >/dev/null 2>&1

for variant in envbad keybad; do
  HV="$WORK/h-$variant"; cp -a "$H" "$HV"
  if [ "$variant" = envbad ]; then printf 'not json at all' > "$HV/envelopes/$STEM.json"; FILE="envelope"; else : > "$HV/keys/$STEM.key"; FILE="private key"; fi
  "$BIN" key status --home "$HV" > "$WORK/$variant.out" 2> "$WORK/$variant.err"; C=$?
  say "=== $variant ($FILE damaged): status exit=$C"
  say "--- stdout"; sed -n '1,20p' "$WORK/$variant.out" >> "$LOG"
  say "--- stderr"; sed -n '1,10p' "$WORK/$variant.err" >> "$LOG"
  grep -qi "panic:" "$WORK/$variant.out" "$WORK/$variant.err" && assert "C-15c $variant has no panic" 1 || assert "C-15c $variant has no panic" 0
  if grep -qi "corrupt\|invalid\|unreadable\|damaged\|broken" "$WORK/$variant.out" "$WORK/$variant.err"; then
    assert "C-15c status marks the damaged $FILE" 0
  else
    assert "C-15c status marks the damaged $FILE (it does not; plan only checks presence)" 1
  fi
done
say ""
say "RESULT case-02c done"
exit 0
