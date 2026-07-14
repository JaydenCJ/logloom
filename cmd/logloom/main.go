// Command logloom clusters raw log lines into templates, counts them, and
// flags lines that match no known pattern.
package main

import (
	"os"

	"github.com/JaydenCJ/logloom/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
