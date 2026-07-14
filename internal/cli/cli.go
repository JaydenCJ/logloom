// Package cli wires the logloom subcommands: argument parsing, input
// streaming, and exit codes. All logic that deserves unit tests on its own
// lives in the pure packages (tokenize, drain, state, render); this package
// only glues them to files, stdin, and flags.
package cli

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/JaydenCJ/logloom/internal/drain"
	"github.com/JaydenCJ/logloom/internal/version"
)

// Exit codes, documented in the README.
const (
	exitOK      = 0
	exitNovel   = 1
	exitUsage   = 2
	exitRuntime = 3
)

const usage = `logloom — cluster raw log lines into templates, count them, flag novel patterns

Usage:
  logloom scan  [flags] [file ...]                 cluster lines, print the template report
  logloom learn [flags] [file ...]                 build or update a baseline state file
  logloom novel [flags] [file ...]                 print lines matching no known template (exit 1 if any)
  logloom grep  [flags] <template-id> [file ...]   print the raw lines behind one template
  logloom version                                  print the version

Reads stdin when no file is given. Flags go before positional arguments.
Run "logloom <command> -h" for the command's flags.
Exit codes: 0 ok · 1 novel lines found · 2 usage error · 3 runtime error
`

// Run executes the CLI and returns the process exit code.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "scan":
		return runScan(rest, stdin, stdout, stderr)
	case "learn":
		return runLearn(rest, stdin, stdout, stderr)
	case "novel":
		return runNovel(rest, stdin, stdout, stderr)
	case "grep":
		return runGrep(rest, stdin, stdout, stderr)
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "logloom %s\n", version.Version)
		return exitOK
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return exitOK
	default:
		fmt.Fprintf(stderr, "logloom: unknown command %q\n\n%s", cmd, usage)
		return exitUsage
	}
}

// newFlagSet builds a flag set that reports its errors on stderr and never
// os.Exits, so Run stays testable in-process. synopsis is the one-line
// invocation shape shown at the top of the command's -h output.
func newFlagSet(name, synopsis string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "usage: logloom %s\n", synopsis)
		fs.PrintDefaults()
	}
	return fs
}

// parseFlags parses args and maps the outcome to an exit code: -1 means
// "carry on", exitOK means -h printed the usage, anything else is a usage
// error (the flag package already reported it on stderr).
func parseFlags(fs *flag.FlagSet, args []string) int {
	switch err := fs.Parse(args); {
	case err == nil:
		return -1
	case errors.Is(err, flag.ErrHelp):
		return exitOK
	default:
		return exitUsage
	}
}

// treeFlags registers the shared mining knobs on a flag set.
func treeFlags(fs *flag.FlagSet) *drain.Config {
	cfg := drain.DefaultConfig()
	fs.IntVar(&cfg.Depth, "depth", cfg.Depth, "prefix-token levels in the parse tree")
	fs.Float64Var(&cfg.SimThreshold, "threshold", cfg.SimThreshold, "similarity needed to join a template (0-1]")
	fs.IntVar(&cfg.MaxChildren, "max-children", cfg.MaxChildren, "max branches per tree node before wildcarding")
	fs.BoolVar(&cfg.NoMask, "no-mask", cfg.NoMask, "cluster on verbatim tokens (skip typed masking)")
	return &cfg
}

// fail prints a runtime error in the standard shape and returns exit 3.
func fail(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "logloom: %v\n", err)
	return exitRuntime
}

// eachLine streams every line of the named files (or stdin when none are
// given) through fn, with a 1 MiB per-line ceiling. Trailing \r from CRLF
// input is stripped; blank lines are skipped, matching what the miner
// would do with an empty token list.
func eachLine(stdin io.Reader, files []string, fn func(line string)) error {
	if len(files) == 0 {
		return scanReader(stdin, "stdin", fn)
	}
	for _, name := range files {
		f, err := os.Open(name)
		if err != nil {
			return err
		}
		err = scanReader(f, name, fn)
		f.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func scanReader(r io.Reader, name string, fn func(line string)) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		fn(line)
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("read %s: %w", name, err)
	}
	return nil
}
