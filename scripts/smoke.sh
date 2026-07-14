#!/usr/bin/env bash
# End-to-end smoke test for logloom: builds the binary, streams the bundled
# sample log through every subcommand, and asserts on real CLI output and
# exit codes. No network, idempotent, finishes in seconds.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

fail() {
  echo "SMOKE FAIL: $*" >&2
  exit 1
}

BIN="$WORKDIR/logloom"
SAMPLE="$ROOT/examples/sample.log"
STATE="$WORKDIR/baseline.json"

echo "1. build"
(cd "$ROOT" && go build -o "$BIN" ./cmd/logloom) || fail "go build failed"

echo "2. version matches manifest"
"$BIN" version | grep -qx "logloom 0.1.0" || fail "version mismatch"

echo "3. scan collapses 200 lines to 9 templates"
OUT="$("$BIN" scan "$SAMPLE")"
echo "$OUT" | grep -q "200 lines → 9 templates" || fail "template count wrong"
echo "$OUT" | grep -q "<time> INFO http request method=<\*> path=<\*> status=<num> latency=<dur>" \
  || fail "http template missing"
echo "$OUT" | grep -q "host=<ip>" || fail "ip mask missing"
echo "$OUT" | grep -q "session=<uuid>" || fail "uuid mask missing"

echo "4. JSON report is machine-readable and correct"
JSON="$("$BIN" scan -format json "$SAMPLE")"
echo "$JSON" | grep -q '"tool": "logloom"' || fail "json envelope missing"
echo "$JSON" | grep -q '"lines": 200' || fail "json line total wrong"
echo "$JSON" | grep -q '"templates": 9' || fail "json template total wrong"
echo "$JSON" | grep -q '"count": 96' || fail "top template count wrong"

echo "5. template IDs are stable across runs"
"$BIN" scan -format json "$SAMPLE" > "$WORKDIR/run1.json"
"$BIN" scan -format json "$SAMPLE" > "$WORKDIR/run2.json"
cmp -s "$WORKDIR/run1.json" "$WORKDIR/run2.json" || fail "runs are not byte-identical"

echo "6. learn writes a baseline state file"
"$BIN" learn -state "$STATE" "$SAMPLE" | grep -q "9 templates (9 new)" \
  || fail "learn summary wrong"
grep -q '"schema_version": 1' "$STATE" || fail "state file malformed"

echo "7. known lines are not novel (exit 0)"
head -20 "$SAMPLE" | "$BIN" novel -state "$STATE" > "$WORKDIR/novel.txt" 2>/dev/null \
  || fail "known lines flagged as novel"
[ -s "$WORKDIR/novel.txt" ] && fail "novel printed known lines"

echo "8. an unseen pattern is flagged and exits 1"
NOVEL_LINE="2026-02-04T09:12:45Z ERROR disk write failed device=sda1 err=EIO"
set +e
echo "$NOVEL_LINE" | "$BIN" novel -state "$STATE" > "$WORKDIR/novel.txt" 2>/dev/null
CODE=$?
set -e
[ "$CODE" -eq 1 ] || fail "novel should exit 1, got $CODE"
grep -qx "$NOVEL_LINE" "$WORKDIR/novel.txt" || fail "novel line not printed"

echo "9. novel -learn alerts once per pattern"
echo "$NOVEL_LINE" | "$BIN" novel -learn -state "$STATE" >/dev/null 2>&1 || true
if ! echo "$NOVEL_LINE" | "$BIN" novel -state "$STATE" >/dev/null 2>&1; then
  fail "second sighting should be known after -learn"
fi

echo "10. grep pulls one template's raw lines back out"
# Clusters are sorted by count, so the first id is the 96-hit http template.
ID="$("$BIN" scan -format json "$SAMPLE" | grep -m1 '"id"' \
  | sed 's/.*"\(t[0-9a-f]\{8\}\)".*/\1/')"
[ -n "$ID" ] || fail "could not extract template id"
"$BIN" grep -state "$STATE" "$ID" "$SAMPLE" > "$WORKDIR/grep.txt" 2>/dev/null
[ "$(wc -l < "$WORKDIR/grep.txt")" -eq 96 ] || fail "grep should print 96 http lines"
grep -q "cache hit" "$WORKDIR/grep.txt" && fail "grep leaked other templates"

echo "11. usage errors exit 2"
set +e
"$BIN" scan -format yaml "$SAMPLE" >/dev/null 2>&1
[ $? -eq 2 ] || fail "bad -format should exit 2"
"$BIN" novel >/dev/null 2>&1
[ $? -eq 2 ] || fail "novel without -state should exit 2"
set -e

echo "SMOKE OK"
