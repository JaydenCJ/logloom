// Package tokenize turns raw log lines into token sequences with
// variable-looking parts replaced by typed mask tokens.
//
// Masking runs before clustering, so lines that differ only in timestamps,
// numbers, addresses, or identifiers collapse into one template with a
// readable, typed placeholder (<time>, <num>, <ip>, …) instead of a bare
// wildcard. Every rule is token-local and regular — no lookahead across
// whitespace — which keeps the tokenizer O(line length) and streaming-safe.
package tokenize

import (
	"regexp"
	"strings"
	"unicode"
)

// Typed mask tokens emitted by the tokenizer. The clustering layer adds one
// more, the untyped wildcard "<*>", when it merges templates.
const (
	Num   = "<num>"   // integers, floats, signed numbers, percentages
	Hex   = "<hex>"   // 0x… literals, or ≥6 hex chars containing a digit
	IP    = "<ip>"    // dotted IPv4, optionally with a :port suffix
	UUID  = "<uuid>"  // RFC 4122 style 8-4-4-4-12
	Time  = "<time>"  // ISO 8601 dates/datetimes and bare hh:mm:ss clocks
	Dur   = "<dur>"   // Go-style durations: 250ms, 1.5s, 3h
	Size  = "<size>"  // byte sizes: 512B, 4KiB, 1.2GB
	Email = "<email>" // user@host.tld
	ID    = "<id>"    // long opaque identifiers (≥16 alnum chars, ≥4 digits)
)

var (
	reUUID  = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	reIP    = regexp.MustCompile(`^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}(:\d{1,5})?$`)
	reISO   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}([Tt ]\d{2}:\d{2}:\d{2}(\.\d+)?([Zz]|[+-]\d{2}:?\d{2})?)?$`)
	reClock = regexp.MustCompile(`^\d{1,2}:\d{2}:\d{2}([.,]\d{1,9})?$`)
	reDur   = regexp.MustCompile(`^-?\d+(\.\d+)?(ns|us|µs|ms|s|m|h)$`)
	reSize  = regexp.MustCompile(`^\d+(\.\d+)?(B|[KMGT]i?B|[kKMGT]b)$`)
	reNum   = regexp.MustCompile(`^[+-]?\d+(\.\d+)?%?$`)
	reHex0x = regexp.MustCompile(`^0[xX][0-9a-fA-F]{2,}$`)
	reHex   = regexp.MustCompile(`^[0-9a-fA-F]{6,}$`)
	reEmail = regexp.MustCompile(`^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$`)
	reDigit = regexp.MustCompile(`\d+`)
)

// Tokenizer splits lines on whitespace and masks each token. The zero value
// masks; set NoMask to cluster on verbatim tokens instead.
type Tokenizer struct {
	NoMask bool
}

// Tokens returns the token sequence for one raw log line. Empty and
// whitespace-only lines yield a nil slice; callers should skip them.
func (t Tokenizer) Tokens(line string) []string {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return nil
	}
	if t.NoMask {
		return fields
	}
	out := make([]string, len(fields))
	for i, f := range fields {
		out[i] = MaskToken(f)
	}
	return out
}

// MaskToken classifies a single whitespace-delimited token and returns it
// with any variable-looking core replaced by a typed mask. Surrounding
// punctuation ("(", "[", trailing commas, quotes, …) is preserved so that
// `(0.5s)` masks to `(<dur>)` and `latency=12ms,` to `latency=<dur>,`.
func MaskToken(tok string) string {
	prefix, core, suffix := trimPunct(tok)
	if core == "" {
		return tok
	}
	masked := maskCore(core)
	if masked == core {
		return tok
	}
	return prefix + masked + suffix
}

// trimPunct peels common wrapping punctuation off both ends of a token so
// classification sees the payload. It returns the removed prefix and suffix
// verbatim for reassembly.
func trimPunct(tok string) (prefix, core, suffix string) {
	start, end := 0, len(tok)
	for start < end && strings.ContainsRune("([{<\"'`", rune(tok[start])) {
		start++
	}
	for end > start && strings.ContainsRune(")]}>.,;:!?\"'`", rune(tok[end-1])) {
		end--
	}
	return tok[:start], tok[start:end], tok[end:]
}

// maskCore applies the classification pipeline to a punctuation-free core:
// typed classes first, then key=value recursion, then the digit-run
// fallback that turns `worker-3` into `worker-<num>`.
func maskCore(core string) string {
	if m, ok := classify(core); ok {
		return m
	}
	// key=value: keep the key literal, mask the value with the full token
	// pipeline (so quoted or wrapped values still classify).
	if i := strings.IndexByte(core, '='); i > 0 && i < len(core)-1 {
		key, val := core[:i], core[i+1:]
		if masked := MaskToken(val); masked != val {
			return key + "=" + masked
		}
		return core
	}
	// Fallback: any token still carrying digits gets its digit runs masked,
	// so shard names, versions, and counters line up (`v1.2.3` → `v<num>.<num>.<num>`).
	if strings.ContainsFunc(core, unicode.IsDigit) {
		return reDigit.ReplaceAllString(core, Num)
	}
	return core
}

// classify matches the core against the typed classes, most specific first.
// Bare hex (no 0x prefix) requires at least one decimal digit so English
// words that happen to be hex letters ("facade", "deadbeef") stay literal.
func classify(core string) (string, bool) {
	switch {
	case reUUID.MatchString(core):
		return UUID, true
	case reIP.MatchString(core):
		return IP, true
	case reISO.MatchString(core), reClock.MatchString(core):
		return Time, true
	case reDur.MatchString(core):
		return Dur, true
	case reSize.MatchString(core):
		return Size, true
	case reNum.MatchString(core):
		return Num, true
	case reHex0x.MatchString(core):
		return Hex, true
	case reHex.MatchString(core) && strings.ContainsFunc(core, unicode.IsDigit):
		return Hex, true
	case reEmail.MatchString(core):
		return Email, true
	case idLike(core):
		return ID, true
	}
	return "", false
}

// idLike spots long opaque identifiers: at least 16 chars of [A-Za-z0-9_-]
// with at least 4 digits and one letter — session tokens, request IDs,
// container hashes with separators.
func idLike(core string) bool {
	if len(core) < 16 {
		return false
	}
	digits, letters := 0, 0
	for _, r := range core {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
			letters++
		case r == '-' || r == '_':
		default:
			return false
		}
	}
	return digits >= 4 && letters >= 1
}
