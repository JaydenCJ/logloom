// Tests for the three report formats. Rendering is the tool's public face:
// alignment, filtering, share arithmetic, and JSON stability all get pinned
// here so refactors cannot silently change what users and scripts parse.
package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/JaydenCJ/logloom/internal/drain"
)

// demoTree builds a tree with known counts: 3× cache, 2× http, 1× error.
func demoTree(t *testing.T) *drain.Tree {
	t.Helper()
	tr := drain.New(drain.DefaultConfig())
	for _, l := range []string{
		"INFO cache hit key=a ttl=x",
		"INFO cache hit key=b ttl=y",
		"INFO cache hit key=c ttl=z",
		"INFO http request served status=ok latency=low",
		"INFO http request served status=bad latency=high",
		"ERROR disk write failed device=sda err=EIO",
	} {
		tr.Feed(l, strings.Fields(l))
	}
	return tr
}

func renderTo(t *testing.T, tr *drain.Tree, opts Options) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Report(&buf, tr, opts); err != nil {
		t.Fatalf("render: %v", err)
	}
	return buf.String()
}

func TestTextHeaderSummarizesLinesAndTemplates(t *testing.T) {
	out := renderTo(t, demoTree(t), Options{Format: "text"})
	if !strings.Contains(out, "logloom — 6 lines → 3 templates") {
		t.Fatalf("header missing:\n%s", out)
	}
}

func TestTextRowsSortedByCountWithShares(t *testing.T) {
	out := renderTo(t, demoTree(t), Options{Format: "text"})
	lines := strings.Split(strings.TrimSpace(out), "\n")
	first := lines[2] // header, blank, column row... rows start at index 3
	if !strings.Contains(first, "count") {
		t.Fatalf("column header missing: %q", first)
	}
	rowCache, rowHTTP := lines[3], lines[4]
	if !strings.Contains(rowCache, "50.0") || !strings.Contains(rowCache, "cache") {
		t.Fatalf("top row should be cache at 50.0%%: %q", rowCache)
	}
	if !strings.Contains(rowHTTP, "33.3") {
		t.Fatalf("second row should be http at 33.3%%: %q", rowHTTP)
	}
}

func TestTextTopLimitsRowsAndSaysSo(t *testing.T) {
	out := renderTo(t, demoTree(t), Options{Format: "text", Top: 1})
	if strings.Contains(out, "ERROR disk") || strings.Contains(out, "http") {
		t.Fatalf("top=1 must keep only the most frequent template:\n%s", out)
	}
	if !strings.Contains(out, "showing 1") {
		t.Fatalf("footer must disclose truncation:\n%s", out)
	}
}

func TestMinCountHidesRareTemplates(t *testing.T) {
	out := renderTo(t, demoTree(t), Options{Format: "text", MinCount: 2})
	if strings.Contains(out, "disk") {
		t.Fatalf("min-count=2 must hide the singleton error row:\n%s", out)
	}
	// The header still reports the true totals.
	if !strings.Contains(out, "3 templates") {
		t.Fatalf("totals must not shrink with filters:\n%s", out)
	}
}

func TestTextMarksNewTemplatesOnlyWithBaseline(t *testing.T) {
	tr := demoTree(t)
	if out := renderTo(t, tr, Options{Format: "text"}); strings.Contains(out, "NEW") {
		t.Fatalf("no baseline → no NEW column:\n%s", out)
	}
	tr.MarkBaseline()
	l := "FATAL kernel panicked reason=cosmic ray=bitflip"
	tr.Feed(l, strings.Fields(l))
	out := renderTo(t, tr, Options{Format: "text"})
	if !strings.Contains(out, "NEW") || !strings.Contains(out, "1 new since baseline") {
		t.Fatalf("baseline run must flag new templates:\n%s", out)
	}
}

func TestJSONIsParseableWithStableEnvelope(t *testing.T) {
	out := renderTo(t, demoTree(t), Options{Format: "json"})
	var rep struct {
		Tool          string `json:"tool"`
		SchemaVersion int    `json:"schema_version"`
		Lines         int64  `json:"lines"`
		Templates     int    `json:"templates"`
		Shown         int    `json:"shown"`
		Clusters      []struct {
			ID       string  `json:"id"`
			Template string  `json:"template"`
			Count    int64   `json:"count"`
			Share    float64 `json:"share"`
		} `json:"clusters"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if rep.Tool != "logloom" || rep.SchemaVersion != 1 {
		t.Fatalf("envelope: %+v", rep)
	}
	if rep.Lines != 6 || rep.Templates != 3 || rep.Shown != 3 {
		t.Fatalf("totals: %+v", rep)
	}
	if rep.Clusters[0].Count != 3 || rep.Clusters[0].Share != 50.0 {
		t.Fatalf("first cluster: %+v", rep.Clusters[0])
	}
}

func TestJSONDoesNotHTMLEscapeMasks(t *testing.T) {
	out := renderTo(t, demoTree(t), Options{Format: "json"})
	if strings.Contains(out, `\u003c`) {
		t.Fatalf("masks must not be HTML-escaped in JSON:\n%s", out)
	}
	if !strings.Contains(out, "key=<*>") {
		t.Fatalf("expected literal generalized key in JSON:\n%s", out)
	}
}

func TestShareIsRoundedToOneDecimal(t *testing.T) {
	if got := share(1, 3); got != 33.3 {
		t.Fatalf("share(1,3) = %v, want 33.3", got)
	}
	if got := share(2, 3); got != 66.7 {
		t.Fatalf("share(2,3) = %v, want 66.7", got)
	}
	if got := share(5, 0); got != 0 {
		t.Fatalf("share with zero lines must be 0, got %v", got)
	}
}

func TestMarkdownTableEscapesPipes(t *testing.T) {
	tr := drain.New(drain.DefaultConfig())
	for _, l := range []string{"proc emitted output a|b now", "proc emitted output c|d now"} {
		tr.Feed(l, strings.Fields(l))
	}
	out := renderTo(t, tr, Options{Format: "markdown"})
	if !strings.Contains(out, "| count | share | id | template |") {
		t.Fatalf("markdown header missing:\n%s", out)
	}
	if strings.Contains(out, " a|b ") {
		t.Fatalf("unescaped pipe would break the table:\n%s", out)
	}
}

func TestUnknownFormatIsAnError(t *testing.T) {
	var buf bytes.Buffer
	if err := Report(&buf, demoTree(t), Options{Format: "yaml"}); err == nil {
		t.Fatal("unknown format must be rejected")
	}
}

func TestCommaSeparatesThousands(t *testing.T) {
	cases := map[int64]string{
		0: "0", 7: "7", 999: "999", 1000: "1,000",
		1234567: "1,234,567", -4200: "-4,200",
	}
	for n, want := range cases {
		if got := comma(n); got != want {
			t.Fatalf("comma(%d) = %q, want %q", n, got, want)
		}
	}
}
