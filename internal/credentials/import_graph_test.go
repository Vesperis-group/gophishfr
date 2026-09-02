package credentials_test

import (
	"bufio"
	"bytes"
	"go/parser"
	"go/token"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// forbiddenStdlibImports are standard-library packages this crypto primitive
// still must never depend on, even though they pass the general "is it part
// of the standard library" check below: net/smtp is a protocol-specific
// client, and importing it here would couple a low-level, generic
// byte-in/byte-out primitive to a specific transport it has no business
// knowing about. Everything else non-application, non-stdlib, or otherwise
// unwanted is caught by the blanket stdlib-only check in
// TestPackageDoesNotImportApplicationLayers, so this list only needs to name
// stdlib exceptions.
var forbiddenStdlibImports = map[string]bool{
	"net/smtp": true,
}

// stdlibPackages returns the set of import paths that make up the Go
// standard library, as reported by the Go toolchain itself ("go list std").
// Deferring to the toolchain's own authoritative package list — rather than
// a hand-written prefix list of "things we know about today" or a naive
// heuristic like "no dot in the first path segment" — is what lets this test
// reject *every* non-stdlib import, including one added in the future that
// nobody thought to add to a hardcoded list. The result only depends on
// which Go toolchain built the test binary, so it is fully deterministic for
// a given `go` installation, exactly like the compiler/vet/build checks this
// test already runs alongside.
func stdlibPackages(t *testing.T) map[string]bool {
	t.Helper()

	out, err := exec.Command("go", "list", "std").Output()
	if err != nil {
		t.Fatalf("go list std: %v", err)
	}

	set := make(map[string]bool)
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			set[line] = true
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("go list std: reading output: %v", err)
	}
	if len(set) == 0 {
		t.Fatal("go list std reported no packages")
	}
	return set
}

// TestPackageDoesNotImportApplicationLayers statically inspects this
// package's own non-test source files and fails if any of them imports
// anything other than the Go standard library, or imports one of the
// specifically forbidden standard-library packages in
// forbiddenStdlibImports. This is the regression test for "the package must
// not import the application version/config package, or persistence,
// controller, ORM, or protocol-client packages, or anything else that isn't
// the standard library" — a passing build alone would not catch a newly
// added, unused-looking import, but this test parses the source directly
// regardless of whether the import is actually referenced, and rejecting
// every non-stdlib import (rather than only a hardcoded list of
// application packages) means a new dependency nobody thought to blocklist
// is caught just as reliably as one that was.
func TestPackageDoesNotImportApplicationLayers(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("unable to determine test file location")
	}
	dir := filepath.Dir(thisFile)

	matches, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("no source files found to inspect")
	}

	stdlib := stdlibPackages(t)

	fset := token.NewFileSet()
	inspected := 0
	for _, path := range matches {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		inspected++

		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("ParseFile(%s): %v", path, err)
		}
		for _, imp := range f.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			if forbiddenStdlibImports[importPath] {
				t.Fatalf("%s imports forbidden standard-library package %q", filepath.Base(path), importPath)
			}
			if !stdlib[importPath] {
				t.Fatalf("%s imports %q, which is not part of the Go standard library: this package must depend only on the standard library", filepath.Base(path), importPath)
			}
		}
	}
	if inspected == 0 {
		t.Fatal("no non-test source files found to inspect")
	}
}
