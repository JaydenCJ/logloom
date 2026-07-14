# How logloom mines templates

logloom turns a stream of raw log lines into a small set of *templates* —
the constant skeleton of each message with the variable parts replaced by
placeholders — in a single pass, O(1) per line, with no training phase.
The design follows the Drain algorithm (He et al., *Drain: An Online Log
Parsing Approach with Fixed Depth Tree*, ICWS 2017), with two additions:
typed masking and content-hash template IDs.

## Stage 1 — typed masking (`internal/tokenize`)

Each line is split on whitespace and every token is classified. Tokens that
look variable are replaced by a *typed* mask, so templates read like
documentation instead of wildcard soup:

| Mask | Matches | Example |
|---|---|---|
| `<time>` | ISO 8601 dates/datetimes, bare `hh:mm:ss` clocks | `2026-02-03T10:00:27Z` |
| `<num>` | integers, floats, signed numbers, percentages | `-42`, `99.9%` |
| `<ip>` | dotted IPv4, optional `:port` | `10.0.3.17:8443` |
| `<uuid>` | RFC 4122 8-4-4-4-12 | `6b3f2a10-9c4e-…` |
| `<dur>` | Go-style durations | `250ms`, `1.5s` |
| `<size>` | byte sizes | `4KiB`, `1.2GB` |
| `<hex>` | `0x…`, or ≥6 hex chars containing a digit | `8f3a2c1d` |
| `<email>` | `user@host.tld` | `alice@example.test` |
| `<id>` | ≥16 alnum chars with ≥4 digits | `req_01H8XGJWBWBAQ4` |

Three refinements make the rules robust on real logs:

- **Wrapping punctuation is preserved**: `(0.5s)` → `(<dur>)`,
  `latency=12ms,` → `latency=<dur>,`.
- **`key=value` tokens keep their key** and mask only the value:
  `status=200` → `status=<num>`, `host=10.0.3.17:8443` → `host=<ip>`.
- **Digit-run fallback**: any leftover digit-bearing token has its digit
  runs masked — `worker-3` → `worker-<num>`, `v1.2.3` →
  `v<num>.<num>.<num>` — so counters never split clusters.

Near-misses stay literal on purpose: `deadbeef` and `facade` are English
words, not hex (bare hex requires a decimal digit), and a long word without
digits is never an `<id>`. `-no-mask` disables the whole stage.

## Stage 2 — the parse tree (`internal/drain`)

Masked token sequences descend a fixed-depth tree:

1. **Length level** — lines with different token counts never share a
   template.
2. **Prefix levels** (default `-depth 3`) — the first tokens route the
   line. Digit-bearing tokens and wildcard-bearing tokens go through a
   shared `<*>` branch so identifiers cannot mint branches; when a node
   already has `-max-children` (default 64) children, unseen literals
   overflow into that branch too.
3. **Leaf** — a short list of candidate clusters.

Inside the leaf, the line is scored against each cluster: matching tokens
count 1, wildcard positions count 1 (the template already gave them up),
everything else 0, divided by the token count. The best cluster at or above
`-threshold` (default 0.5) absorbs the line; disagreeing positions
generalize to `<*>` — or to `key=<*>` when both sides carry the same key.
Otherwise the line founds a new cluster.

Consequences worth knowing:

- Prefix tokens (`cache hit` vs `cache miss`) partition by design — words
  in the first `-depth` positions carry message identity.
- Everything is deterministic: same input, byte-identical report.

## Stable template IDs

A cluster's ID is `t` + the first 8 hex chars of the SHA-256 of its token
sequence **at birth**, and it never changes afterwards — not when the
template generalizes, not across runs. Two properties follow:

1. **Reproducibility without state**: the same input stream always mints
   the same IDs, so two machines scanning the same file agree.
2. **Continuity with state**: `-state` persists clusters (with their IDs,
   counts, and examples) to a versioned JSON file; later runs restore the
   tree and keep counting under the same IDs, which is what makes
   `novel`'s alert-once mode and dashboards keyed on template IDs work.

The state file is written atomically (temp file + rename) and validated
strictly on load: wrong tool marker, unknown `schema_version`, or an
invalid config is an error, never a guess. Tuning stored in the state wins
over command-line flags, because the templates were mined under it.

## Tuning

| Flag | Default | Raise it when… | Lower it when… |
|---|---|---|---|
| `-threshold` | 0.5 | templates over-merge into wildcard soup | one message splits into many templates |
| `-depth` | 3 | unrelated messages share a prefix | messages vary early (e.g. no timestamp column) |
| `-max-children` | 64 | logs have many distinct message heads | memory is tight on hostile input |

Start with the defaults; they were chosen for the common
`timestamp level subsystem message…` shape.
