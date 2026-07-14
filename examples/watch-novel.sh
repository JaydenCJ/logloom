#!/usr/bin/env bash
# The learn-then-watch workflow, end to end and fully offline:
#   1. learn a baseline from the sample log
#   2. replay known traffic  → exit 0, silence
#   3. stream a never-seen pattern → exit 1, the line is printed
#   4. -learn makes each new pattern alert exactly once
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

BIN="$WORKDIR/logloom"
STATE="$WORKDIR/baseline.json"
(cd "$ROOT" && go build -o "$BIN" ./cmd/logloom)

echo "== 1. learn a baseline from 200 lines of normal traffic"
"$BIN" learn -state "$STATE" "$ROOT/examples/sample.log"

echo
echo "== 2. known traffic is quiet (exit \$?)"
head -5 "$ROOT/examples/sample.log" | "$BIN" novel -state "$STATE" || true

echo
echo "== 3. a pattern the service never logged before"
NOVEL="2026-02-04T09:12:45Z ERROR disk write failed device=sda1 err=EIO"
echo "$NOVEL" | "$BIN" novel -learn -state "$STATE" || echo "(exit 1 — alert your pager here)"

echo
echo "== 4. the second sighting is already known"
echo "$NOVEL" | "$BIN" novel -state "$STATE" && echo "(exit 0 — alerted once, not forever)"
