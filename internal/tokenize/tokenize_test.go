// Tests for the typed masking layer. Every class gets positive cases and
// the near-misses that must stay literal — over-masking destroys template
// readability just as surely as under-masking splits clusters.
package tokenize

import (
	"reflect"
	"testing"
)

func TestTokensSplitsOnAnyWhitespace(t *testing.T) {
	got := Tokenizer{NoMask: true}.Tokens("a\tb  c")
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestBlankLinesYieldNil(t *testing.T) {
	for _, line := range []string{"", "   \t "} {
		if got := (Tokenizer{}).Tokens(line); got != nil {
			t.Fatalf("Tokens(%q) should be nil, got %v", line, got)
		}
	}
}

func TestNoMaskKeepsVerbatimTokens(t *testing.T) {
	got := Tokenizer{NoMask: true}.Tokens("status=200 latency=12ms")
	want := []string{"status=200", "latency=12ms"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestTokensMasksEachField(t *testing.T) {
	got := Tokenizer{}.Tokens("worker 3 done in 412ms")
	want := []string{"worker", Num, "done", "in", Dur}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// checkMask asserts input token → expected masked token.
func checkMask(t *testing.T, in, want string) {
	t.Helper()
	if got := MaskToken(in); got != want {
		t.Fatalf("MaskToken(%q) = %q, want %q", in, got, want)
	}
}

func TestMaskNumbers(t *testing.T) {
	checkMask(t, "12345", Num)
	checkMask(t, "3.14", Num)
	checkMask(t, "-42", Num)
	checkMask(t, "99.9%", Num)
}

func TestMaskNetworkAddresses(t *testing.T) {
	checkMask(t, "10.0.3.17", IP)
	checkMask(t, "10.0.3.17:8443", IP)
	// Five dotted groups are not an IPv4 address; the digit-run fallback
	// masks the digits instead of pretending the token is an address.
	checkMask(t, "1.2.3.4.5", Num+"."+Num+"."+Num+"."+Num+"."+Num)
}

func TestMaskUUID(t *testing.T) {
	checkMask(t, "6b3f2a10-9c4e-4f6b-8a2d-1e5f7c9b0d42", UUID)
}

func TestMaskTimestamps(t *testing.T) {
	checkMask(t, "2026-02-03", Time)
	checkMask(t, "2026-02-03T10:00:27Z", Time)
	checkMask(t, "2026-02-03T10:00:27.123+09:00", Time)
	checkMask(t, "10:00:27", Time) // bare clock
}

func TestMaskDurationsAndSizes(t *testing.T) {
	checkMask(t, "250ms", Dur)
	checkMask(t, "1.5s", Dur)
	checkMask(t, "-3ms", Dur)
	checkMask(t, "512B", Size)
	checkMask(t, "4KiB", Size)
	checkMask(t, "1.2GB", Size)
}

func TestMaskHex(t *testing.T) {
	checkMask(t, "0x7ffe3b40", Hex)
	checkMask(t, "8f3a2c1d", Hex)
	// English words made of hex letters must stay literal: bare hex
	// (no 0x prefix) requires at least one decimal digit.
	checkMask(t, "deadbeef", "deadbeef")
	checkMask(t, "facade", "facade")
}

func TestMaskEmails(t *testing.T) {
	checkMask(t, "alice@example.test", Email)
	checkMask(t, "<alice@example.test>", "<"+Email+">")
}

func TestMaskOpaqueIDs(t *testing.T) {
	checkMask(t, "req_01H8XGJWBWBAQ4", ID)
	// Too short → digit-run fallback, not <id>.
	checkMask(t, "job123", "job"+Num)
	// No digits at all → literal, however long.
	checkMask(t, "internationalization", "internationalization")
}

func TestKeyValueMasking(t *testing.T) {
	checkMask(t, "status=200", "status="+Num)
	checkMask(t, "latency=12ms", "latency="+Dur)
	checkMask(t, "host=10.0.3.17:8443", "host="+IP)
	checkMask(t, `user="8231"`, `user="`+Num+`"`)
	checkMask(t, "path=/api/v1/accounts/8231", "path=/api/v"+Num+"/accounts/"+Num)
	// Literal values stay put — the clustering layer decides whether they
	// generalize, and then keeps the key (method=<*>).
	checkMask(t, "method=GET", "method=GET")
}

func TestWrappingPunctuationPreserved(t *testing.T) {
	checkMask(t, "(0.5s)", "("+Dur+")")
	checkMask(t, "latency=12ms,", "latency="+Dur+",")
	checkMask(t, "[10.0.3.17]", "["+IP+"]")
}

func TestDigitRunFallback(t *testing.T) {
	checkMask(t, "worker-3", "worker-"+Num)
	checkMask(t, "v1.2.3", "v"+Num+"."+Num+"."+Num)
	checkMask(t, "sda1", "sda"+Num)
}

func TestLiteralTokensUntouched(t *testing.T) {
	for _, w := range []string{"INFO", "request", "finished.", "->", "...", `""`, "()"} {
		checkMask(t, w, w)
	}
}
