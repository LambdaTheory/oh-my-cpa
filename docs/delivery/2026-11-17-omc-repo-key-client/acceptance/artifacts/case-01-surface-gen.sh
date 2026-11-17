#!/bin/bash
# T5-S14 review-only process cases: command surface, key gen, permissions.
# Runs inside golang:1.27.1 with /evidence mounted; all working dirs are under /tmp so
# Unix permission bits are real (the Windows bind mount cannot carry 0600).
set -u
BIN=/evidence/oh-my-cpa
LOG=/evidence/case-logs/case-01-surface-gen.log
mkdir -p /evidence/case-logs
: > "$LOG"
FAILS=0

say() { echo "$@" | tee -a "$LOG"; }
section() { echo "" | tee -a "$LOG"; echo "===== $* =====" | tee -a "$LOG"; }
run() { # run <label> -- cmd...
  local label="$1"; shift
  echo "--- $label" | tee -a "$LOG"
  echo "CMD: $*" >> "$LOG"
  "$@" > /tmp/c.out 2> /tmp/c.err
  local code=$?
  echo "EXIT: $code" | tee -a "$LOG"
  echo "STDOUT:" >> "$LOG"; sed -n '1,40p' /tmp/c.out >> "$LOG"
  echo "STDERR:" >> "$LOG"; sed -n '1,40p' /tmp/c.err >> "$LOG"
  return $code
}
assert() { # assert <name> <condition-result>
  if [ "$2" = "0" ]; then echo "ASSERT $1 PASS" | tee -a "$LOG"; else echo "ASSERT $1 FAIL" | tee -a "$LOG"; FAILS=$((FAILS+1)); fi
}
expect_code() { # expect_code <name> <want> <got>
  [ "$3" -eq "$2" ] && assert "$1 exit=$3" 0 || assert "$1 exit=$3 want=$2" 1
}

export TZ=UTC ASSISTANT_NO_SEND=1
WORK=$(mktemp -d /tmp/t5s14-case01.XXXXXX)
export HOME="$WORK/home"; mkdir -p "$HOME"
export OMC_HOME="$WORK/omc-home"
say "WORK=$WORK"

section "C-01 command surface"
run "key --help" "$BIN" key --help; expect_code "C-01 key-help" 0 $?
HELP=$(cat /tmp/c.out)
for cmd in gen status exec install purge accept; do
  run "key $cmd --help" "$BIN" key "$cmd" --help; expect_code "C-01 $cmd-help" 0 $?
  echo "$HELP" | grep -q "  $cmd " && assert "C-01 top-help lists $cmd" 0 || assert "C-01 top-help lists $cmd" 1
done
echo "$HELP" | grep -qi "print the private key\|prints the private key\|outputs the private key" && assert "C-01 help has no private-key print claim" 1 || assert "C-01 help has no private-key print claim" 0
run "key nope" "$BIN" key nope; expect_code "C-01 unknown-command" 2 $?
grep -q "unknown command" /tmp/c.err && assert "C-01 unknown command names itself" 0 || assert "C-01 unknown command names itself" 1
grep -q "Usage: oh-my-cpa key" /tmp/c.err && assert "C-01 unknown command shows usage on stderr" 0 || assert "C-01 unknown command shows usage on stderr" 1
[ -s /tmp/c.out ] && assert "C-01 unknown command writes nothing to stdout" 1 || assert "C-01 unknown command writes nothing to stdout" 0

section "C-02 the seven steps have entry points (no approved key yet)"
run "status on an empty home" "$BIN" key status --home "$OMC_HOME"; expect_code "C-02 empty status" 0 $?
if grep -qi "no key\|no keys\|not found" /tmp/c.out || grep -qi "key gen" /tmp/c.out; then assert "C-02 empty status names next step" 0; else assert "C-02 empty status names next step" 1; fi
run "gen example/fixture-repo" "$BIN" key gen --repo example/fixture-repo --home "$OMC_HOME" --base-url http://127.0.0.1:18317
expect_code "C-02 gen" 0 $?
KEYFILE=$(find "$OMC_HOME/keys" -type f | head -1)
STEM=$(basename "$KEYFILE" .key)
mkdir -p "$OMC_HOME/envelopes"
printf 'not a valid envelope' > "$OMC_HOME/envelopes/$STEM.json"
run "status with a corrupted envelope" "$BIN" key status --home "$OMC_HOME"; expect_code "C-02 status corrupt envelope" 0 $?
sed -n '1,60p' /tmp/c.out >> "$LOG"
run "exec with a corrupted envelope" "$BIN" key exec --runtime claude --repo example/fixture-repo --home "$OMC_HOME" -- true
C=$?; [ $C -ne 0 ] && assert "C-02 exec fails readably on a bad envelope (exit=$C)" 0 || assert "C-02 exec fails readably on a bad envelope" 1
grep -qi "envelope" /tmp/c.err && assert "C-02 exec error names the envelope" 0 || assert "C-02 exec error names the envelope" 1
grep -q "panic:" /tmp/c.err /tmp/c.out && assert "C-02 no panic" 1 || assert "C-02 no panic" 0
rm -f "$OMC_HOME/envelopes/$STEM.json"

section "C-03 existing subcommands do not regress"
run "mcp -h" "$BIN" mcp -h; MCPC=$?
if [ $MCPC -eq 0 ] || [ $MCPC -eq 2 ]; then assert "C-03 mcp -h is a usage exit" 0; else assert "C-03 mcp -h is a usage exit" 1; fi
if [ -s /tmp/c.err ]; then assert "C-03 mcp usage goes to stderr" 0; else assert "C-03 mcp usage goes to stderr" 1; fi
if [ -s /tmp/c.out ]; then assert "C-03 mcp stdout stays protocol-only" 1; else assert "C-03 mcp stdout stays protocol-only" 0; fi
run "updater -h" "$BIN" updater -h; UPDC=$?
if [ $UPDC -eq 0 ] || [ $UPDC -eq 2 ]; then assert "C-03 updater -h is a usage exit" 0; else assert "C-03 updater -h is a usage exit" 1; fi
run "no subcommand starts the server path" timeout 2 "$BIN" --help; NOC=$?
if [ $NOC -eq 0 ] || [ $NOC -eq 1 ] || [ $NOC -eq 2 ]; then assert "C-03 bare binary still has its own path (exit=$NOC)" 0; else assert "C-03 bare binary still has its own path" 1; fi

section "C-04 gen: private key on disk, stdout is only the public key"
H2="$WORK/h2"; mkdir -p "$H2"
"$BIN" key gen --repo lambdatheory/fixture-repo --home "$H2" > /tmp/gen.out 2> /tmp/gen.err; C=$?
expect_code "C-04 gen" 0 $C
cp /tmp/gen.out /evidence/case-logs/gen.out; cp /tmp/gen.err /evidence/case-logs/gen.err
LINES=$(wc -l < /tmp/gen.out)
echo "stdout_lines=$LINES" >> "$LOG"
[ "$LINES" -eq 1 ] && assert "C-04 stdout is one line" 0 || assert "C-04 stdout is one line" 1
PUB=$(tr -d '\n' < /tmp/gen.out)
case "$PUB" in x25519:*) assert "C-04 stdout is the public key text" 0;; *) assert "C-04 stdout is the public key text" 1;; esac
BYTES=$(printf '%s' "${PUB#x25519:}" | base64 -d 2>/dev/null | wc -c)
[ "$BYTES" -eq 32 ] && assert "C-04 public key is 32 bytes" 0 || assert "C-04 public key is 32 bytes (got $BYTES)" 1
grep -qi "generated a key" /tmp/gen.err && assert "C-04 stderr names the key file" 0 || assert "C-04 stderr names the key file" 1
K2=$(find "$H2/keys" -type f)
[ -f "$K2" ] && assert "C-04 private key exists" 0 || assert "C-04 private key exists" 1
KSIZE=$(wc -c < "$K2")
[ "$KSIZE" -eq 32 ] && assert "C-04 private key is 32 raw bytes" 0 || assert "C-04 private key is 32 raw bytes (got $KSIZE)" 1

section "C-05 private key permissions 0600 (Unix)"
PERM=$(stat -c '%a' "$K2")
[ "$PERM" = "600" ] && assert "C-05 key mode is 600 (got $PERM)" 0 || assert "C-05 key mode is 600 (got $PERM)" 1
su nobody -s /bin/sh -c "cat '$K2'" > /tmp/nobody.out 2> /tmp/nobody.err; NC=$?
echo "nobody_read_exit=$NC" >> "$LOG"; sed -n '1,3p' /tmp/nobody.err >> "$LOG"
[ $NC -ne 0 ] && assert "C-05 another user cannot read the key" 0 || assert "C-05 another user cannot read the key" 1

section "C-06 repository identity normalization and path safety"
H3="$WORK/h3"; mkdir -p "$H3"
"$BIN" key gen --repo lambdatheory/fixture-repo --home "$H3" > /tmp/g1.out 2>/dev/null
"$BIN" key gen --repo https://github.com/LambdaTheory/Fixture-Repo.git --home "$H3" > /tmp/g2.out 2>/dev/null
"$BIN" key gen --repo git@github.com:lambdatheory/fixture-repo.git --home "$H3" > /tmp/g3.out 2>/dev/null
[ "$(cat /tmp/g1.out)" = "$(cat /tmp/g2.out)" ] && [ "$(cat /tmp/g1.out)" = "$(cat /tmp/g3.out)" ] && assert "C-06 three spellings reuse one key" 0 || assert "C-06 three spellings reuse one key" 1
COUNT=$(find "$H3/keys" -type f | wc -l)
[ "$COUNT" -eq 1 ] && assert "C-06 one key file for three spellings" 0 || assert "C-06 one key file for three spellings ($COUNT)" 1
find "$H3/keys" -type f -printf '%f\n' >> "$LOG"
mk() { local d="$WORK/$1"; rm -rf "$d"; mkdir -p "$d"; "$BIN" key gen --repo "$2" --home "$d" > /tmp/o 2> /tmp/e; echo $?; }
R1=$(mk x1 '../../../etc/passwd'); echo "repo=../../etc/passwd exit=$R1 err=$(cat /tmp/e)" >> "$LOG"
[ "$R1" -ne 0 ] && assert "C-06 ../../etc/passwd rejected" 0 || assert "C-06 ../../etc/passwd rejected (accepted as $(cat /tmp/o))" 1
R2=$(mk x2 'a/../../b'); echo "repo=a/../../b exit=$R2 err=$(cat /tmp/e)" >> "$LOG"
[ "$R2" -ne 0 ] && assert "C-06 a/../../b rejected" 0 || assert "C-06 a/../../b rejected (accepted)" 1
R3=$(mk x3 ''); echo "repo=<empty> exit=$R3 err=$(cat /tmp/e)" >> "$LOG"
[ "$R3" -ne 0 ] && assert "C-06 empty repo rejected" 0 || assert "C-06 empty repo rejected" 1
R4=$(mk x4 $'a/\tb'); echo "repo=<tab> exit=$R4 err=$(head -1 /tmp/e)" >> "$LOG"
[ "$R4" -ne 0 ] && assert "C-06 control character rejected" 0 || assert "C-06 control character rejected (accepted)"
LONG=$(printf 'x%.0s' $(seq 1 300))
R5=$(mk x5 "$LONG"); echo "repo=<300 chars> exit=$R5 err=$(head -1 /tmp/e)" >> "$LOG"
[ "$R5" -ne 0 ] && assert "C-06 overlong repo rejected" 0 || assert "C-06 overlong repo rejected (accepted)"
OUTSIDE=$(find "$WORK" -path "$WORK/home" -prune -o -path '*/omc-home*' -prune -o -path "$WORK/x*" -prune -o -type f -print | grep -v '^/tmp/t5s14-case01' | head -5)
echo "files outside omc-home: $OUTSIDE" >> "$LOG"
[ -z "$OUTSIDE" ] && assert "C-06 nothing written outside OMC_HOME" 0 || assert "C-06 nothing written outside OMC_HOME" 1

section "C-07 repeated gen reuses instead of rotating"
H4="$WORK/h4"; mkdir -p "$H4"
"$BIN" key gen --repo org/repo --home "$H4" > /tmp/a.out 2>/dev/null
SHA1=$(sha256sum "$(find "$H4/keys" -type f)" | cut -d' ' -f1)
"$BIN" key gen --repo org/repo --home "$H4" > /tmp/b.out 2> /tmp/b.err; C=$?
SHA2=$(sha256sum "$(find "$H4/keys" -type f)" | cut -d' ' -f1)
[ $C -eq 0 ] && assert "C-07 second gen exits zero" 0 || assert "C-07 second gen exits zero" 1
[ "$SHA1" = "$SHA2" ] && assert "C-07 private key unchanged" 0 || assert "C-07 private key unchanged" 1
[ "$(cat /tmp/a.out)" = "$(cat /tmp/b.out)" ] && assert "C-07 same public key printed" 0 || assert "C-07 same public key printed" 1
grep -qi "reused" /tmp/b.err && assert "C-07 second run says reused" 0 || assert "C-07 second run says reused" 1

section "C-08 unwritable target and OMC_HOME as a file (as nobody)"
NB="$WORK/nb"; mkdir -p "$NB"; chown -R nobody "$NB"
su nobody -s /bin/sh -c "HOME=$NB/home; mkdir -p \$HOME; $BIN key gen --repo ok/repo --home $NB/home" >/dev/null 2>&1
chmod 500 "$NB/home/keys" 2>/dev/null
su nobody -s /bin/sh -c "$BIN key gen --repo other/repo --home $NB/home" > /tmp/n1.out 2> /tmp/n1.err; C1=$?
chmod 700 "$NB/home/keys" 2>/dev/null
su nobody -s /bin/sh -c "$BIN key gen --repo ok/repo --home $NB/home" > /tmp/n2.out 2> /tmp/n2.err; C2=$?
printf 'plain file' > "$NB/notadir"
su nobody -s /bin/sh -c "$BIN key gen --repo other/repo --home $NB/notadir" > /tmp/n3.out 2> /tmp/n3.err; C3=$?
su nobody -s /bin/sh -c "$BIN key gen --repo recover/repo --home $NB/fresh" > /tmp/n4.out 2> /tmp/n4.err; C4=$?
echo "readonly-dir exit=$C1 err=$(head -1 /tmp/n1.err)" >> "$LOG"
echo "readonly-key exit=$C2 err=$(head -1 /tmp/n2.err)" >> "$LOG"
echo "home-is-file exit=$C3 err=$(head -1 /tmp/n3.err)" >> "$LOG"
echo "recovered exit=$C4" >> "$LOG"
[ $C1 -ne 0 ] && grep -qi "permission\|read-only\|denied" /tmp/n1.err && assert "C-08 read-only keys dir fails readably" 0 || assert "C-08 read-only keys dir fails readably" 1
[ $C3 -ne 0 ] && assert "C-08 OMC_HOME as a regular file fails" 0 || assert "C-08 OMC_HOME as a regular file fails" 1
[ $C4 -eq 0 ] && assert "C-08 generation recovers on a writable home" 0 || assert "C-08 generation recovers on a writable home" 1

section "C-09 only the public key on stdout/stderr; scan every written file"
HEX=$(od -An -tx1 "$K2" | tr -d ' \n')
B64=$(base64 -w0 "$K2")
grep -qF "$HEX" /tmp/gen.out /tmp/gen.err && assert "C-09 private key hex not in gen streams" 1 || assert "C-09 private key hex not in gen streams" 0
grep -qF "$B64" /tmp/gen.out /tmp/gen.err && assert "C-09 private key base64 not in gen streams" 1 || assert "C-09 private key base64 not in gen streams" 0
BAD=0
while IFS= read -r f; do
  [ "$f" = "$K2" ] && continue
  grep -qF "$HEX" "$f" && BAD=1
done < <(find "$WORK" -type f)
[ $BAD -eq 0 ] && assert "C-09 private key only in the key file itself" 0 || assert "C-09 private key only in the key file itself" 1

say ""
say "RESULT case-01 FAILS=$FAILS"
exit $FAILS
