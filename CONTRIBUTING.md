# Contributing to logloom

Issues, discussions and pull requests are all welcome.

## Getting started

You need Go ≥1.22; nothing else.

```bash
git clone https://github.com/JaydenCJ/logloom && cd logloom
go build ./...
go test ./...
bash scripts/smoke.sh
```

`scripts/smoke.sh` builds the binary and drives every subcommand against
the bundled sample log — template counts, JSON schema, ID stability,
novelty exit codes, grep round-trips; it must finish by printing `SMOKE OK`.

## Before you open a pull request

1. `gofmt -l .` reports nothing (formatting is enforced).
2. `go vet ./...` passes with no findings.
3. `go test ./...` passes (91 deterministic tests, no network).
4. `bash scripts/smoke.sh` prints `SMOKE OK`.
5. Add tests for behavior changes; keep logic in pure, unit-testable
   modules (tokenize, drain, state, and render never touch files or
   flags — only `internal/cli` does).

## Ground rules

- Keep dependencies at zero — logloom is standard-library-only, and that
  is a feature, not an accident. Adding one needs strong justification.
- No network calls, ever; no telemetry. Input is stdin or named files,
  output is stdout/stderr and the state file the user asked for.
- Determinism first: identical input must produce byte-identical reports,
  including all orderings and template IDs.
- Masking rules are data: a new mask class needs a regex in
  `internal/tokenize`, positive and near-miss tests, and a row in
  `docs/template-mining.md`.
- Code comments and doc comments are written in English.

## Reporting bugs

Include the output of `logloom version`, the exact command line, and a
minimal set of log lines that reproduce the issue (redact payloads, keep
the shape — masking and clustering only see token structure). For state
file problems, attach the offending file's `schema_version` and `config`
sections.

## Security

Please do not open public issues for security problems; use GitHub's
private vulnerability reporting on this repository instead.
