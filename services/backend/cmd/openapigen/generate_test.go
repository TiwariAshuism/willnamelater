package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFixtureModule materializes a self-contained Go module under a temp dir
// that mirrors the real internal/<module>/api + internal/model layout, using
// only stdlib and its own local packages so go/packages can load it offline.
// It returns the module root. This is a test fixture, not fabricated data.
func writeFixtureModule(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	files := map[string]string{
		"go.mod": "module influaudit.test/fixture\n\ngo 1.25\n",

		"uuid/uuid.go": "package uuid\n\ntype UUID [16]byte\n",

		"internal/widget/internal/model/model.go": `package model

import (
	"time"

	"influaudit.test/fixture/uuid"
)

type Widget struct {
	ID        uuid.UUID ` + "`json:\"id\"`" + `
	Name      string    ` + "`json:\"name\"`" + `
	CreatedAt time.Time ` + "`json:\"created_at\"`" + `
	Tags      []string  ` + "`json:\"tags,omitempty\"`" + `
}

type CreateWidget struct {
	Name string ` + "`json:\"name\"`" + `
}
`,

		"internal/widget/api/routes.go": `package api

import (
	"context"

	"influaudit.test/fixture/internal/widget/internal/model"
)

// WidgetAPI is the fixture surface exercised by the generator tests.
type WidgetAPI interface {
	// GET /widgets
	List(ctx context.Context) ([]model.Widget, error)

	// GET /widgets/:id
	Get(ctx context.Context, id string) (model.Widget, error)

	// POST /widgets
	Create(ctx context.Context, req model.CreateWidget) (model.Widget, error)
}
`,
	}

	for rel, content := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return root
}

func TestGenerateFromFixture(t *testing.T) {
	root := writeFixtureModule(t)
	doc, err := Generate(context.Background(), root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if doc.Paths.Value("/widgets") == nil {
		t.Fatal("missing path /widgets")
	}
	if doc.Paths.Value("/widgets/{id}") == nil {
		t.Fatal("missing path /widgets/{id} (Gin :id not rewritten)")
	}

	list := doc.Paths.Value("/widgets").Get
	if list == nil || list.OperationID != "List" {
		t.Fatalf("GET /widgets operationId = %v, want List", list)
	}
	arr := list.Responses.Value("200").Value.Content.Get("application/json").Schema
	if arr.Value == nil || arr.Value.Items == nil {
		t.Fatal("List response should be an array")
	}
	if arr.Value.Items.Ref != "#/components/schemas/widget.Widget" {
		t.Errorf("List items ref = %q, want widget.Widget", arr.Value.Items.Ref)
	}

	create := doc.Paths.Value("/widgets").Post
	if create == nil || create.OperationID != "Create" {
		t.Fatal("POST /widgets should map to Create")
	}
	if create.RequestBody == nil {
		t.Fatal("Create should have a request body")
	}
	bodyRef := create.RequestBody.Value.Content.Get("application/json").Schema
	if bodyRef.Ref != "#/components/schemas/widget.CreateWidget" {
		t.Errorf("Create body ref = %q, want widget.CreateWidget", bodyRef.Ref)
	}

	get := doc.Paths.Value("/widgets/{id}").Get
	if get == nil || len(get.Parameters) != 1 {
		t.Fatalf("GET /widgets/{id} should have one path parameter")
	}
	param := get.Parameters[0].Value
	if param.Name != "id" || param.In != "path" || !param.Required {
		t.Errorf("path param = %+v, want required path param id", param)
	}

	widget := doc.Components.Schemas["widget.Widget"]
	if widget == nil {
		t.Fatal("widget.Widget not registered")
	}
	if f := widget.Value.Properties["id"]; f == nil || f.Value.Format != "uuid" {
		t.Error("Widget.id should be a uuid-format string")
	}
	if f := widget.Value.Properties["created_at"]; f == nil || f.Value.Format != "date-time" {
		t.Error("Widget.created_at should be date-time")
	}
	// tags is omitempty, so it must not be required; id/name/created_at are.
	if contains(widget.Value.Required, "tags") {
		t.Error("omitempty field tags should not be required")
	}
	for _, want := range []string{"created_at", "id", "name"} {
		if !contains(widget.Value.Required, want) {
			t.Errorf("Widget.required missing %q", want)
		}
	}
}

func TestGenerateEmptyWhenNoRoutes(t *testing.T) {
	root := t.TempDir()
	// A module with no internal/ dir at all.
	if err := os.WriteFile(filepath.Join(root, "go.mod"),
		[]byte("module influaudit.test/empty\n\ngo 1.25\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	doc, err := Generate(context.Background(), root)
	if err != nil {
		t.Fatalf("Generate on empty module: %v", err)
	}
	if doc.Paths.Len() != 0 {
		t.Errorf("expected empty paths, got %d", doc.Paths.Len())
	}
	// The empty document must still marshal to valid YAML.
	if _, err := Marshal(doc); err != nil {
		t.Fatalf("Marshal empty doc: %v", err)
	}
}

func TestMarshalDeterministic(t *testing.T) {
	root := writeFixtureModule(t)
	doc, err := Generate(context.Background(), root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	first, err := Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	second, err := Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(first) != string(second) {
		t.Error("Marshal output is not byte-identical across runs")
	}
	// A full regeneration from source must also match, proving the whole
	// pipeline is deterministic, not just repeated marshaling of one doc.
	doc2, err := Generate(context.Background(), root)
	if err != nil {
		t.Fatalf("Generate again: %v", err)
	}
	third, err := Marshal(doc2)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(first) != string(third) {
		t.Error("regenerated spec differs from the first")
	}

	// No Gin-style path should survive into the emitted document.
	if strings.Contains(string(first), "/widgets/:id") {
		t.Error("emitted spec still contains a Gin-style path")
	}
}

func TestCheckDrift(t *testing.T) {
	root := writeFixtureModule(t)
	doc, err := Generate(context.Background(), root)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	spec, err := Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	out := filepath.Join(t.TempDir(), "influaudit.yaml")
	if err := writeSpec(out, spec); err != nil {
		t.Fatalf("writeSpec: %v", err)
	}

	// Identical content: no drift.
	if err := checkDrift(out, spec); err != nil {
		t.Errorf("checkDrift on identical content = %v, want nil", err)
	}
	// Mutated content: drift reported.
	if err := checkDrift(out, append(spec, []byte("drift: true\n")...)); err == nil {
		t.Error("checkDrift should detect drift")
	}
	// Missing file: a clear error, not a nil.
	if err := checkDrift(filepath.Join(t.TempDir(), "missing.yaml"), spec); err == nil {
		t.Error("checkDrift on missing file should error")
	}
}

func TestGinToOpenAPIPath(t *testing.T) {
	tests := []struct{ in, want string }{
		{"/widgets", "/widgets"},
		{"/widgets/:id", "/widgets/{id}"},
		{"/orgs/:org/widgets/:id", "/orgs/{org}/widgets/{id}"},
	}
	for _, tc := range tests {
		if got := ginToOpenAPIPath(tc.in); got != tc.want {
			t.Errorf("ginToOpenAPIPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
