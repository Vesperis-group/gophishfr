package credentials_test

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// forbiddenImportPrefixes are packages the credential crypto primitive must
// never depend on: application version/config, persistence models,
// controllers, the ORM, and protocol-specific clients. Depending on any of
// these would couple a low-level, reusable primitive to business logic it
// has no business knowing about, and — for config/version specifically —
// would risk a GophishFR release number leaking into the AAD by accident.
var forbiddenImportPrefixes = []string{
	"github.com/Vesperis-group/gophishfr/config",
	"github.com/Vesperis-group/gophishfr/models",
	"github.com/Vesperis-group/gophishfr/controllers",
	"github.com/Vesperis-group/gophishfr/middleware",
	"github.com/jinzhu/gorm",
	"github.com/emersion/go-imap",
	"github.com/gophish/gomail",
	"net/smtp",
}

// TestPackageDoesNotImportApplicationLayers statically inspects this
// package's own non-test source files and fails if any of them imports a
// forbidden package. This is the regression test for "the package must not
// even import the application version package" (and, by the same
// reasoning, must not import persistence/controller/protocol packages
// either): a passing build alone would not catch a newly added, unused-looking
// import, but this test parses the source directly regardless of whether the
// import is actually referenced.
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
			for _, forbidden := range forbiddenImportPrefixes {
				if importPath == forbidden || strings.HasPrefix(importPath, forbidden+"/") {
					t.Fatalf("%s imports forbidden package %q", filepath.Base(path), importPath)
				}
			}
		}
	}
	if inspected == 0 {
		t.Fatal("no non-test source files found to inspect")
	}
}
