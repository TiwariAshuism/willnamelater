// Command openapigen derives the OpenAPI 3 contract for the backend from the
// annotated interfaces in every internal/<module>/api/routes.go and writes it to
// packages/contracts/openapi/influaudit.yaml.
//
// apigen's own -layers openapi output is unusable (Gin-style paths and dangling
// $refs into empty components), so the spec is generated here from the same
// source of truth apigen parses: routes.go, cross-checked against the module's
// Go types so request and response structs are reflected into real schemas.
//
// Flags:
//
//	-check  generate in memory and compare to the committed file, exiting
//	        non-zero with a diff on drift. This is the CI gate.
//	-out    override the output path.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/go-cmp/cmp"
)

// defaultOutRel is the contract path relative to the backend module root.
var defaultOutRel = filepath.Join("..", "..", "packages", "contracts", "openapi", "influaudit.yaml")

func main() {
	check := flag.Bool("check", false, "compare generated spec to the committed file and exit non-zero on drift")
	out := flag.String("out", "", "output path (defaults to packages/contracts/openapi/influaudit.yaml)")
	flag.Parse()

	if err := run(*check, *out); err != nil {
		fmt.Fprintln(os.Stderr, "openapigen:", err)
		os.Exit(1)
	}
}

func run(check bool, out string) error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	if out == "" {
		out = filepath.Join(root, defaultOutRel)
	}

	doc, err := Generate(context.Background(), root)
	if err != nil {
		return err
	}
	generated, err := Marshal(doc)
	if err != nil {
		return err
	}

	if check {
		return checkDrift(out, generated)
	}
	return writeSpec(out, generated)
}

// checkDrift compares the freshly generated spec to the committed file and
// returns an error describing the drift when they differ.
func checkDrift(path string, generated []byte) error {
	// #nosec G304 -- path is an operator-supplied CLI flag on a build tool, never
	// derived from request input.
	committed, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no committed spec at %s; run openapigen to create it", path)
		}
		return err
	}
	if string(committed) == string(generated) {
		return nil
	}
	diff := cmp.Diff(strings.Split(string(committed), "\n"), strings.Split(string(generated), "\n"))
	return fmt.Errorf("spec drift at %s (committed vs generated):\n%s\nrun openapigen to regenerate", path, diff)
}

// writeSpec writes the generated spec to path, creating parent directories.
func writeSpec(path string, generated []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return os.WriteFile(path, generated, 0o600)
}
