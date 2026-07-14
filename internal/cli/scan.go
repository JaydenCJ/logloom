package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/JaydenCJ/logloom/internal/drain"
	"github.com/JaydenCJ/logloom/internal/render"
	"github.com/JaydenCJ/logloom/internal/state"
	"github.com/JaydenCJ/logloom/internal/tokenize"
)

// runScan clusters the input and prints the template report. With -state,
// an existing state file is loaded first (its tuning wins over flags),
// templates created by this run are flagged NEW, and the updated state is
// written back.
func runScan(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := newFlagSet("scan", "scan [flags] [file ...]", stderr)
	cfg := treeFlags(fs)
	format := fs.String("format", "text", "output format: text, json, or markdown")
	top := fs.Int("top", 0, "show only the N most frequent templates (0 = all)")
	minCount := fs.Int64("min-count", 1, "hide templates matched fewer than N times")
	statePath := fs.String("state", "", "state file to load before and save after the run")
	if code := parseFlags(fs, args); code >= 0 {
		return code
	}
	switch *format {
	case "text", "json", "markdown":
	default:
		fmt.Fprintf(stderr, "logloom scan: unknown format %q (want text, json, or markdown)\n", *format)
		return exitUsage
	}
	tree, loaded, code := openTree(*statePath, *cfg, stderr)
	if code != exitOK {
		return code
	}
	if loaded {
		tree.MarkBaseline()
	}
	tok := tokenize.Tokenizer{NoMask: tree.Config().NoMask}
	err := eachLine(stdin, fs.Args(), func(line string) {
		tree.Feed(line, tok.Tokens(line))
	})
	if err != nil {
		return fail(stderr, err)
	}
	opts := render.Options{Format: *format, Top: *top, MinCount: *minCount}
	if err := render.Report(stdout, tree, opts); err != nil {
		return fail(stderr, err)
	}
	if *statePath != "" {
		if err := state.Save(*statePath, tree); err != nil {
			return fail(stderr, err)
		}
	}
	return exitOK
}

// runLearn clusters the input and writes the state file — scan without the
// report, for building baselines in scripts and cron jobs.
func runLearn(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := newFlagSet("learn", "learn [flags] [file ...]", stderr)
	cfg := treeFlags(fs)
	statePath := fs.String("state", "", "state file to create or update (required)")
	if code := parseFlags(fs, args); code >= 0 {
		return code
	}
	if *statePath == "" {
		fmt.Fprintln(stderr, "logloom learn: -state is required")
		return exitUsage
	}
	tree, loaded, code := openTree(*statePath, *cfg, stderr)
	if code != exitOK {
		return code
	}
	if loaded {
		tree.MarkBaseline()
	}
	before := tree.Len()
	tok := tokenize.Tokenizer{NoMask: tree.Config().NoMask}
	var lines int64
	err := eachLine(stdin, fs.Args(), func(line string) {
		tree.Feed(line, tok.Tokens(line))
		lines++
	})
	if err != nil {
		return fail(stderr, err)
	}
	if err := state.Save(*statePath, tree); err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "learned %s → %s (%d new) · state written to %s\n",
		countNoun(lines, "line"), countNoun(int64(tree.Len()), "template"), tree.Len()-before, *statePath)
	return exitOK
}

// openTree loads the state file when it exists (validating tuning conflicts
// away by letting the file win) or builds a fresh tree from flags.
func openTree(path string, cfg drain.Config, stderr io.Writer) (*drain.Tree, bool, int) {
	if path != "" {
		if _, err := os.Stat(path); err == nil {
			tree, err := state.Load(path)
			if err != nil {
				return nil, false, fail(stderr, err)
			}
			return tree, true, exitOK
		} else if !os.IsNotExist(err) {
			return nil, false, fail(stderr, err)
		}
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(stderr, "logloom: %v\n", err)
		return nil, false, exitUsage
	}
	return drain.New(cfg), false, exitOK
}

// countNoun renders a count with its noun so the grammatical number always
// agrees: "1 line", "12,345 lines" — never "1 lines".
func countNoun(n int64, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return commaCLI(n) + " " + noun + "s"
}

// commaCLI matches render's thousands formatting for CLI summaries.
func commaCLI(n int64) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	out := ""
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out += ","
		}
		out += string(r)
	}
	return out
}
