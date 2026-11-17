#!/bin/bash
# C-08 corrected: the first attempt ran as nobody under a 700 parent and tested the
# parent instead of the scenario. This one makes the parent world-traversable first.
set -u
BIN=/evidence/oh-my-cpa
LOG=/evidence/case-logs/case-01b-c08.log
mkdir -p /evidence/case-logs
: > "$LOG"
FAILS=0
say() { echo "$@" | tee -a "$LOG"; }
assert() { if [ "$2" = "0" ]; then echo "ASSERT $1 PASS" | tee -a "$LOG"; else echo "ASSERT $1 FAIL" | tee -a "$LOG"; FAILS=$((FAILS+1)); fi; }
export TZ=UTC ASSISTANT_NO_SEND=1

WORK=$(mktemp -d /tmp/t5s14-case01b.XXXXXX); chmod 755 "$WORK"
NB="$WORK/nb"; mkdir -p "$NB"; chown nobody "$NB"
chmod 755 /evidence /evidence/oh-my-cpa

asnobody() { su nobody -s /bin/sh -c "$1"; }

asnobody "HOME=$NB/home $BIN key gen --repo ok/repo --home $NB/home" > /tmp/s0.out 2> /tmp/s0.err
say "baseline gen exit=$? err=$(cat /tmp/s0.err)"
chmod 755 "$WORK" "$NB"; chown -R nobody "$NB"

chmod 500 "$NB/home/keys"
asnobody "$BIN key gen --repo other/repo --home $NB/home" > /tmp/s1.out 2> /tmp/s1.err; C1=$?
chmod 700 "$NB/home/keys"
say "read-only keys dir: exit=$C1 err=$(head -2 /tmp/s1.err | tr '\n' ' ')"

asnobody "$BIN key gen --repo ok/repo --home $NB/home" > /tmp/s2.out 2> /tmp/s2.err; C2=$?
say "existing key (0600): exit=$C2 out=$(cat /tmp/s2.out | cut -c1-24) err=$(head -1 /tmp/s2.err)"
KEY=$(asnobody "ls $NB/home/keys/*.key")
chmod 400 "$KEY"
asnobody "$BIN key gen --repo ok/repo --home $NB/home" > /tmp/s3.out 2> /tmp/s3.err; C3=$?
chmod 600 "$KEY"
say "read-only key file, same repo: exit=$C3 err=$(head -1 /tmp/s3.err)"

printf 'plain file' > "$NB/notadir"; chown nobody "$NB/notadir"
asnobody "$BIN key gen --repo other/repo --home $NB/notadir" > /tmp/s4.out 2> /tmp/s4.err; C4=$?
say "OMC_HOME is a regular file: exit=$C4 err=$(head -2 /tmp/s4.err | tr '\n' ' ')"

asnobody "HOME=$NB/home $BIN key gen --repo recover/repo --home $NB/fresh" > /tmp/s5.out 2> /tmp/s5.err; C5=$?
say "recovered on a fresh home: exit=$C5 err=$(cat /tmp/s5.err)"

LEFTOVER=$(find "$NB" -name '*.key' -size 0 | wc -l)
say "zero-byte key leftovers: $LEFTOVER"

if [ $C1 -ne 0 ] && grep -qi "permission\|read-only\|denied" /tmp/s1.err; then assert "C-08 read-only keys dir fails readably" 0; else assert "C-08 read-only keys dir fails readably" 1; fi
if [ $C3 -eq 0 ] && grep -qi "reused" /tmp/s3.err; then assert "C-08 read-only existing key is reused per plan 5.3 (not a failure)" 0; else assert "C-08 read-only existing key is reused per plan 5.3 (not a failure)" 1; fi
if [ $C4 -ne 0 ] && grep -qi "not a directory\|regular file\|home_unavailable" /tmp/s4.err; then assert "C-08 OMC_HOME as a regular file fails readably" 0; else assert "C-08 OMC_HOME as a regular file fails readably" 1; fi
if [ $C5 -eq 0 ]; then assert "C-08 generation recovers on a writable home" 0; else assert "C-08 generation recovers on a writable home" 1; fi
if [ "$LEFTOVER" -eq 0 ]; then assert "C-08 no zero-byte key left behind" 0; else assert "C-08 no zero-byte key left behind" 1; fi

say "RESULT case-01b FAILS=$FAILS"
exit $FAILS
