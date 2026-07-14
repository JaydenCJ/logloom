# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-07-13

### Added

- Streaming template miner: a fixed-depth parse tree in the spirit of the
  Drain algorithm, with length partitioning, prefix-token routing, wildcard
  branches for digit-bearing tokens, a `MaxChildren` cap against tree
  explosion, and inclusive similarity-threshold merging.
- Typed masking before clustering: `<time>`, `<num>`, `<ip>`, `<uuid>`,
  `<dur>`, `<size>`, `<hex>`, `<email>`, `<id>`, plus `key=value`-aware
  value masking, wrapping-punctuation preservation, and a digit-run
  fallback (`worker-3` → `worker-<num>`); `-no-mask` opt-out.
- Key-preserving generalization: merging `status=200` with `status=404`
  yields `status=<*>`, never a bare wildcard.
- Stable template IDs: `t` + 8 hex chars of the birth template's SHA-256,
  preserved through generalization and across runs via the state file.
- `scan` subcommand with aligned text tables, stable JSON
  (`schema_version: 1`), and Markdown output; `-top`, `-min-count`,
  `-threshold`, `-depth`, `-max-children` tuning flags.
- Versioned JSON state files (atomic writes, strict validation on load)
  powering `learn` (build/update a baseline), `novel` (print lines matching
  no known template, exit 1, with `-learn` alert-once mode), and `grep`
  (pull one template's raw lines back out, with `-invert`).
- Baseline-aware reports: templates first seen after the baseline are
  flagged `NEW` in every output format.
- A 200-line deterministic sample log, a runnable `examples/watch-novel.sh`
  walkthrough, and a mining/masking reference (`docs/template-mining.md`).
- 91 deterministic offline tests (unit + in-process CLI integration) and
  `scripts/smoke.sh`.

[0.1.0]: https://github.com/JaydenCJ/logloom/releases/tag/v0.1.0
