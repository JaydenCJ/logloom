// In-process integration tests: every subcommand is driven through Run with
// real files in temp dirs and asserted on stdout, stderr, and exit codes —
// exactly what a shell user or script sees. No network, no goroutines.
package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// run executes the CLI in-process and captures both streams.
func run(t *testing.T, stdin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errBuf bytes.Buffer
	code = Run(args, strings.NewReader(stdin), &out, &errBuf)
	return code, out.String(), errBuf.String()
}

// writeLog drops a small deterministic log file into a temp dir.
func writeLog(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "app.log")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

var demoLines = []string{
	"2026-02-03T10:00:01Z INFO cache hit key=user:8231 ttl=300s",
	"2026-02-03T10:00:02Z INFO cache hit key=user:9414 ttl=270s",
	"2026-02-03T10:00:03Z INFO cache hit key=user:1027 ttl=240s",
	"2026-02-03T10:00:04Z ERROR upstream timeout host=10.0.3.17:8443 after=5s",
}

func TestNoArgsAndUnknownCommandExit2(t *testing.T) {
	if code, _, stderr := run(t, ""); code != 2 || !strings.Contains(stderr, "Usage:") {
		t.Fatalf("no args: code=%d stderr=%q", code, stderr)
	}
	if code, _, stderr := run(t, "", "explode"); code != 2 || !strings.Contains(stderr, `unknown command "explode"`) {
		t.Fatalf("unknown command: code=%d stderr=%q", code, stderr)
	}
}

func TestVersionCommand(t *testing.T) {
	code, stdout, _ := run(t, "", "version")
	if code != 0 || stdout != "logloom 0.1.0\n" {
		t.Fatalf("code=%d stdout=%q", code, stdout)
	}
}

func TestHelpExitsZero(t *testing.T) {
	code, stdout, _ := run(t, "", "help")
	if code != 0 || !strings.Contains(stdout, "logloom scan") {
		t.Fatalf("code=%d stdout=%q", code, stdout)
	}
}

// -h on a subcommand is a request for help, not a usage mistake: it must
// print the command's synopsis and exit 0, never 2.
func TestSubcommandHelpExitsZero(t *testing.T) {
	for _, cmd := range []string{"scan", "learn", "novel", "grep"} {
		code, _, stderr := run(t, "", cmd, "-h")
		if code != 0 {
			t.Fatalf("%s -h: want exit 0, got %d", cmd, code)
		}
		if !strings.Contains(stderr, "usage: logloom "+cmd) {
			t.Fatalf("%s -h: synopsis missing: %q", cmd, stderr)
		}
	}
}

func TestScanFileProducesTemplateReport(t *testing.T) {
	log := writeLog(t, demoLines...)
	code, stdout, _ := run(t, "", "scan", log)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stdout, "4 lines → 2 templates") {
		t.Fatalf("summary wrong:\n%s", stdout)
	}
	if !strings.Contains(stdout, "<time> INFO cache hit key=user:<num> ttl=<dur>") {
		t.Fatalf("cache template missing:\n%s", stdout)
	}
	if !strings.Contains(stdout, "host=<ip>") {
		t.Fatalf("ip mask missing:\n%s", stdout)
	}
}

func TestScanReadsStdinWhenNoFiles(t *testing.T) {
	code, stdout, _ := run(t, strings.Join(demoLines, "\n")+"\n", "scan")
	if code != 0 || !strings.Contains(stdout, "2 templates") {
		t.Fatalf("code=%d stdout=%q", code, stdout)
	}
}

func TestScanSkipsBlankAndCRLFLines(t *testing.T) {
	in := demoLines[0] + "\r\n\r\n   \n" + demoLines[1] + "\r\n"
	code, stdout, _ := run(t, in, "scan")
	if code != 0 || !strings.Contains(stdout, "2 lines → 1 template") {
		t.Fatalf("blank/CRLF handling wrong:\n%s", stdout)
	}
}

func TestScanJSONOutputIsParseable(t *testing.T) {
	log := writeLog(t, demoLines...)
	code, stdout, _ := run(t, "", "scan", "-format", "json", log)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var rep struct {
		Tool     string `json:"tool"`
		Lines    int64  `json:"lines"`
		Clusters []struct {
			ID    string `json:"id"`
			Count int64  `json:"count"`
		} `json:"clusters"`
	}
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatalf("bad JSON: %v", err)
	}
	if rep.Tool != "logloom" || rep.Lines != 4 || rep.Clusters[0].Count != 3 {
		t.Fatalf("report: %+v", rep)
	}
}

func TestScanTemplateIDsAreStableAcrossRuns(t *testing.T) {
	log := writeLog(t, demoLines...)
	_, out1, _ := run(t, "", "scan", "-format", "json", log)
	_, out2, _ := run(t, "", "scan", "-format", "json", log)
	if out1 != out2 {
		t.Fatal("two runs over the same file must be byte-identical")
	}
}

func TestScanUsageErrorsExit2(t *testing.T) {
	if code, _, stderr := run(t, "", "scan", "-format", "yaml"); code != 2 || !strings.Contains(stderr, "unknown format") {
		t.Fatalf("bad -format: code=%d stderr=%q", code, stderr)
	}
	if code, _, _ := run(t, "", "scan", "-definitely-not-a-flag"); code != 2 {
		t.Fatalf("unknown flag must exit 2, got %d", code)
	}
	if code, _, stderr := run(t, "", "scan", "-threshold", "2.5"); code != 2 || !strings.Contains(stderr, "threshold") {
		t.Fatalf("bad -threshold: code=%d stderr=%q", code, stderr)
	}
}

func TestScanMissingFileExits3(t *testing.T) {
	code, _, stderr := run(t, "", "scan", filepath.Join(t.TempDir(), "ghost.log"))
	if code != 3 || !strings.Contains(stderr, "logloom:") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}

func TestScanWithStateFlagsNewTemplates(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	log := writeLog(t, demoLines...)
	if code, _, stderr := run(t, "", "learn", "-state", statePath, log); code != 0 {
		t.Fatalf("learn failed: %s", stderr)
	}
	in := demoLines[0] + "\n2026-02-04T00:00:00Z FATAL disk on fire device=sda1 temp=451F\n"
	code, stdout, _ := run(t, in, "scan", "-state", statePath)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stdout, "NEW") || !strings.Contains(stdout, "1 new since baseline") {
		t.Fatalf("NEW flag missing:\n%s", stdout)
	}
}

func TestLearnRequiresStateFlag(t *testing.T) {
	code, _, stderr := run(t, "x y z\n", "learn")
	if code != 2 || !strings.Contains(stderr, "-state is required") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}

func TestLearnWritesStateAndSummarizes(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "baseline.json")
	log := writeLog(t, demoLines...)
	code, stdout, _ := run(t, "", "learn", "-state", statePath, log)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stdout, "learned 4 lines → 2 templates (2 new)") {
		t.Fatalf("summary: %q", stdout)
	}
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("state not written: %v", err)
	}
}

func TestLearnIsIncremental(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "baseline.json")
	run(t, demoLines[0]+"\n", "learn", "-state", statePath)
	code, stdout, _ := run(t, demoLines[3]+"\n", "learn", "-state", statePath)
	if code != 0 || !strings.Contains(stdout, "2 templates (1 new)") {
		t.Fatalf("second learn should add exactly one template: %q", stdout)
	}
}

func TestNovelRequiresStateFlag(t *testing.T) {
	code, _, stderr := run(t, "", "novel")
	if code != 2 || !strings.Contains(stderr, "-state is required") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}

func TestNovelPrintsUnknownLinesAndExits1(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "baseline.json")
	log := writeLog(t, demoLines...)
	run(t, "", "learn", "-state", statePath, log)
	in := demoLines[1] + "\n2026-02-04T00:00:00Z FATAL disk on fire device=sda1 temp=451F\n"
	code, stdout, stderr := run(t, in, "novel", "-state", statePath)
	if code != 1 {
		t.Fatalf("novelty must exit 1, got %d", code)
	}
	if !strings.Contains(stdout, "FATAL disk on fire") || strings.Contains(stdout, "cache hit") {
		t.Fatalf("only novel lines belong on stdout:\n%s", stdout)
	}
	if !strings.Contains(stderr, "1 of 2 lines matched no baseline template") {
		t.Fatalf("summary: %q", stderr)
	}
}

func TestNovelExitsZeroWhenAllKnown(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "baseline.json")
	log := writeLog(t, demoLines...)
	run(t, "", "learn", "-state", statePath, log)
	code, stdout, stderr := run(t, demoLines[2]+"\n", "novel", "-state", statePath)
	if code != 0 || stdout != "" {
		t.Fatalf("code=%d stdout=%q", code, stdout)
	}
	if !strings.Contains(stderr, "all 1 line matched") {
		t.Fatalf("summary: %q", stderr)
	}
}

// -learn makes each new pattern alert exactly once: the second sighting is
// no longer novel.
func TestNovelLearnAlertsOncePerPattern(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "baseline.json")
	run(t, demoLines[0]+"\n", "learn", "-state", statePath)
	novelLine := "2026-02-04T00:00:00Z FATAL disk on fire device=sda1 temp=451F\n"
	if code, _, _ := run(t, novelLine, "novel", "-learn", "-state", statePath); code != 1 {
		t.Fatalf("first sighting must exit 1, got %d", code)
	}
	if code, _, _ := run(t, novelLine, "novel", "-state", statePath); code != 0 {
		t.Fatalf("second sighting must be known, got %d", code)
	}
}

func TestNovelQuietSuppressesLines(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "baseline.json")
	run(t, demoLines[0]+"\n", "learn", "-state", statePath)
	code, stdout, _ := run(t, "completely different line here\n", "novel", "-quiet", "-state", statePath)
	if code != 1 || stdout != "" {
		t.Fatalf("quiet mode must print nothing: code=%d stdout=%q", code, stdout)
	}
}

func TestGrepPrintsOnlyTheTemplatesLines(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "baseline.json")
	log := writeLog(t, demoLines...)
	run(t, "", "learn", "-state", statePath, log)
	// Find the error template's ID from the state report.
	_, jsonOut, _ := run(t, "", "scan", "-format", "json", log)
	var rep struct {
		Clusters []struct {
			ID       string `json:"id"`
			Template string `json:"template"`
		} `json:"clusters"`
	}
	json.Unmarshal([]byte(jsonOut), &rep)
	var errID string
	for _, c := range rep.Clusters {
		if strings.Contains(c.Template, "upstream") {
			errID = c.ID
		}
	}
	if errID == "" {
		t.Fatal("error template not found in scan output")
	}
	code, stdout, stderr := run(t, "", "grep", "-state", statePath, errID, log)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	if !strings.Contains(stdout, "upstream timeout") || strings.Contains(stdout, "cache hit") {
		t.Fatalf("grep leaked other templates:\n%s", stdout)
	}
	if !strings.Contains(stderr, "1 line printed") {
		t.Fatalf("summary: %q", stderr)
	}
}

func TestGrepInvertPrintsEverythingElse(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "baseline.json")
	log := writeLog(t, demoLines...)
	run(t, "", "learn", "-state", statePath, log)
	_, jsonOut, _ := run(t, "", "scan", "-format", "json", log)
	var rep struct {
		Clusters []struct {
			ID       string `json:"id"`
			Template string `json:"template"`
		} `json:"clusters"`
	}
	json.Unmarshal([]byte(jsonOut), &rep)
	var errID string
	for _, c := range rep.Clusters {
		if strings.Contains(c.Template, "upstream") {
			errID = c.ID
		}
	}
	code, stdout, _ := run(t, "", "grep", "-invert", "-state", statePath, errID, log)
	if code != 0 || strings.Contains(stdout, "upstream") || !strings.Contains(stdout, "cache hit") {
		t.Fatalf("invert wrong:\n%s", stdout)
	}
}

func TestGrepUnknownTemplateIDExits3(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "baseline.json")
	run(t, demoLines[0]+"\n", "learn", "-state", statePath)
	code, _, stderr := run(t, "", "grep", "-state", statePath, "t00000000")
	if code != 3 || !strings.Contains(stderr, "not found") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}

func TestGrepMissingIDArgumentExits2(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "baseline.json")
	run(t, demoLines[0]+"\n", "learn", "-state", statePath)
	code, _, stderr := run(t, "", "grep", "-state", statePath)
	if code != 2 || !strings.Contains(stderr, "template-id") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}

func TestNovelCorruptStateExits3(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	os.WriteFile(statePath, []byte("{broken"), 0o644)
	code, _, stderr := run(t, "x\n", "novel", "-state", statePath)
	if code != 3 || !strings.Contains(stderr, "logloom:") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}

func TestScanMultipleFilesAggregate(t *testing.T) {
	a := writeLog(t, demoLines[0], demoLines[1])
	b := writeLog(t, demoLines[2], demoLines[3])
	code, stdout, _ := run(t, "", "scan", a, b)
	if code != 0 || !strings.Contains(stdout, "4 lines → 2 templates") {
		t.Fatalf("aggregate wrong:\n%s", stdout)
	}
}

func TestScanNoMaskKeepsRawTokens(t *testing.T) {
	code, stdout, _ := run(t, "alpha beta 42\nalpha beta 42\n", "scan", "-no-mask")
	if code != 0 || !strings.Contains(stdout, "alpha beta 42") {
		t.Fatalf("no-mask template wrong:\n%s", stdout)
	}
	if strings.Contains(stdout, "<num>") {
		t.Fatalf("masking ran despite -no-mask:\n%s", stdout)
	}
}
