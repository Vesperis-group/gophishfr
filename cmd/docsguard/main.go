// Command docsguard fails if one of this PR's fixed set of canonical
// API-key transport documentation files is missing a required fact, shows
// a forbidden literal deprecated-transport example, or shows a canonical
// example that does not use ****** It backs
// scripts/verify-docs-canonical-examples.sh (the local verify.sh gate) and
// the "docs-guard" CI job. Its diagnostics never print matched document
// text: only the file, an optional line, and a fixed Violation.Message, so
// a forbidden example that happens to contain a real secret is never
// echoed into a CI log. See internal/docsguard for the fixed list of
// documents and their checks, and its package doc comment for why this is
// a narrow, explicit assertion tool rather than a Markdown/HTML parser.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Vesperis-group/gophishfr/internal/docsguard"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("docsguard", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "repository root the canonical documents are read relative to")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if extra := fs.Args(); len(extra) != 0 {
		_, _ = fmt.Fprintf(stderr, "docsguard: unexpected argument(s) %v: this command checks a fixed, internal list of canonical documents and takes no file arguments\n", extra)
		return 2
	}

	violations, scanned, err := docsguard.CheckAll(*root)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "docsguard: %v\n", err)
		return 2
	}

	for _, v := range violations {
		if v.Line != 0 {
			_, _ = fmt.Fprintf(stdout, "FORBIDDEN: %s:%d: %s\n", v.File, v.Line, v.Message)
		} else {
			_, _ = fmt.Fprintf(stdout, "FORBIDDEN: %s: %s\n", v.File, v.Message)
		}
	}

	_, _ = fmt.Fprintln(stdout)
	if len(violations) != 0 {
		_, _ = fmt.Fprintf(stderr, "docsguard: %d violation(s) found across %d canonical document(s)\n", len(violations), scanned)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "docsguard: no violations found across %d canonical document(s)\n", scanned)
	return 0
}
