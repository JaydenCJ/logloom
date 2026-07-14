package cli

import (
	"fmt"
	"io"

	"github.com/JaydenCJ/logloom/internal/state"
	"github.com/JaydenCJ/logloom/internal/tokenize"
)

// runNovel streams input against a learned baseline and prints every line
// that strictly matches no known template. Nothing is learned unless -learn
// is set, in which case novel lines join the state so each new pattern
// alerts exactly once. Exit code 1 signals "novelty found" to scripts.
func runNovel(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := newFlagSet("novel", "novel [flags] [file ...]", stderr)
	statePath := fs.String("state", "", "baseline state file (required)")
	learn := fs.Bool("learn", false, "add novel lines to the baseline and save it back")
	quiet := fs.Bool("quiet", false, "suppress line output; summary and exit code only")
	if code := parseFlags(fs, args); code >= 0 {
		return code
	}
	if *statePath == "" {
		fmt.Fprintln(stderr, "logloom novel: -state is required")
		return exitUsage
	}
	tree, err := state.Load(*statePath)
	if err != nil {
		return fail(stderr, err)
	}
	tree.MarkBaseline()
	known := tree.Len()
	tok := tokenize.Tokenizer{NoMask: tree.Config().NoMask}
	var lines, novel int64
	err = eachLine(stdin, fs.Args(), func(line string) {
		lines++
		tokens := tok.Tokens(line)
		if tree.Match(tokens) != nil {
			return
		}
		novel++
		if !*quiet {
			fmt.Fprintln(stdout, line)
		}
		if *learn {
			tree.Feed(line, tokens)
		}
	})
	if err != nil {
		return fail(stderr, err)
	}
	if *learn {
		if err := state.Save(*statePath, tree); err != nil {
			return fail(stderr, err)
		}
	}
	if novel > 0 {
		fmt.Fprintf(stderr, "logloom novel: %s of %s matched no baseline template (%s known)\n",
			commaCLI(novel), countNoun(lines, "line"), countNoun(int64(known), "template"))
		return exitNovel
	}
	fmt.Fprintf(stderr, "logloom novel: all %s matched the baseline (%s)\n",
		countNoun(lines, "line"), countNoun(int64(known), "template"))
	return exitOK
}

// runGrep prints the raw lines behind one template ID, using a state file
// as the template catalog — the inverse of scan: from the fifty templates
// back to the million lines, one pattern at a time.
func runGrep(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := newFlagSet("grep", "grep [flags] <template-id> [file ...]", stderr)
	statePath := fs.String("state", "", "state file holding the templates (required)")
	invert := fs.Bool("invert", false, "print lines that do NOT belong to the template")
	if code := parseFlags(fs, args); code >= 0 {
		return code
	}
	if *statePath == "" {
		fmt.Fprintln(stderr, "logloom grep: -state is required")
		return exitUsage
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprintln(stderr, "logloom grep: a <template-id> argument is required")
		return exitUsage
	}
	id, files := rest[0], rest[1:]
	tree, err := state.Load(*statePath)
	if err != nil {
		return fail(stderr, err)
	}
	found := false
	for _, c := range tree.Clusters() {
		if c.ID == id {
			found = true
			break
		}
	}
	if !found {
		return fail(stderr, fmt.Errorf("template %q not found in %s (run: logloom scan -state %s)",
			id, *statePath, *statePath))
	}
	tok := tokenize.Tokenizer{NoMask: tree.Config().NoMask}
	var matched int64
	err = eachLine(stdin, files, func(line string) {
		c := tree.Match(tok.Tokens(line))
		hit := c != nil && c.ID == id
		if hit != *invert {
			matched++
			fmt.Fprintln(stdout, line)
		}
	})
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stderr, "logloom grep: %s printed for %s\n", countNoun(matched, "line"), id)
	return exitOK
}
