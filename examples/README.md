# logloom examples

Everything here is offline and deterministic.

## sample.log

200 lines of a realistic small-service log: HTTP requests, cache hits and
misses, DB queries, worker jobs, sessions, retries, upstream timeouts, and
two rare config warnings. Every variable field (timestamps, IDs, addresses,
durations) differs line to line — exactly the noise logloom exists to
collapse. It mines down to 9 templates:

```bash
logloom scan examples/sample.log
```

## watch-novel.sh

The learn-then-watch workflow end to end: builds a baseline from
`sample.log`, replays known traffic (exit 0), then streams in a line the
service has never logged before and shows `novel` catching it (exit 1).
With `-learn`, the second sighting no longer alerts.

```bash
bash examples/watch-novel.sh
```

The script pins every input line, so its output is identical on every
machine — useful as a copy-paste starting point for a cron job or a
pre-rotation hook on real logs.
