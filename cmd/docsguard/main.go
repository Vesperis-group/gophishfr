// Command docsguard fails if a tracked Markdown file shows a deprecated
// API-key transport example outside an explicit, narrow exemption list. It
// backs scripts/verify-docs-canonical-examples.sh (local verify.sh gate) and
// the "docs-guard" CI job, so canonical documentation cannot silently drift
// back toward recommending a query/form api_key parameter or a raw
// Authorization header. See internal/docsguard for the detection rules and
// their fixture tests.
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
	var exempt stringSliceFlag
	fs.Var(&exempt, "exempt", "path to exempt from scanning (repeatable)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	exemptSet := make(map[string]bool, len(exempt))
	for _, path := range exempt {
		exemptSet[path] = true
	}

	files := fs.Args()
	if len(files) == 0 {
		_, _ = fmt.Fprintln(stderr, "docsguard: no files given")
		return 2
	}

	totalViolations := 0
	scanned := 0
	for _, path := range files {
		if exemptSet[path] {
			continue
		}
		contents, err := os.ReadFile(path) // #nosec G304 -- path comes from this CLI's own argv/-exempt flags (git-tracked repo files chosen by the invoking script), not untrusted network input.
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "docsguard: reading %s: %v\n", path, err)
			return 2
		}
		scanned++
		violations, err := docsguard.ScanText(string(contents))
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "docsguard: scanning %s: %v\n", path, err)
			return 2
		}
		for _, violation := range violations {
			totalViolations++
			_, _ = fmt.Fprintf(stdout, "FORBIDDEN (%s): %s:%d: %s\n", violation.Kind, path, violation.Line, violation.Text)
		}
	}

	_, _ = fmt.Fprintln(stdout)
	if totalViolations != 0 {
		_, _ = fmt.Fprintf(stderr, "docsguard: %d deprecated API-key transport example(s) found across %d scanned file(s)\n", totalViolations, scanned)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "docsguard: no deprecated API-key transport example found across %d scanned file(s)\n", scanned)
	return 0
}

// stringSliceFlag collects a repeatable -exempt flag into a slice.
type stringSliceFlag []string

func (s *stringSliceFlag) String() string {
	if s == nil {
		return ""
	}
	return fmt.Sprint([]string(*s))
}

func (s *stringSliceFlag) Set(value string) error {
	*s = append(*s, value)
	return nil
}
