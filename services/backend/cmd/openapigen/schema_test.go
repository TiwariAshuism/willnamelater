package main

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"
)

// checkSource type-checks a single-package Go source string and returns its
// package scope so tests can look up named types by identifier.
func checkPackage(t *testing.T, src string) *types.Package {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "src.go", src, parser.AllErrors)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	conf := types.Config{Importer: importer.ForCompiler(fset, "source", nil)}
	pkg, err := conf.Check("fixture", fset, []*ast.File{file}, nil)
	if err != nil {
		t.Fatalf("type check: %v", err)
	}
	return pkg
}

// lookupType returns the type of the named object in pkg.
func lookupType(t *testing.T, pkg *types.Package, name string) types.Type {
	t.Helper()
	obj := pkg.Scope().Lookup(name)
	if obj == nil {
		t.Fatalf("type %q not found", name)
	}
	return obj.Type()
}

// fieldType returns the type of the named field of a struct type.
func fieldType(t *testing.T, structType types.Type, field string) types.Type {
	t.Helper()
	st, ok := structType.Underlying().(*types.Struct)
	if !ok {
		t.Fatalf("not a struct: %s", structType)
	}
	for i := 0; i < st.NumFields(); i++ {
		if st.Field(i).Name() == field {
			return st.Field(i).Type()
		}
	}
	t.Fatalf("field %q not found", field)
	return nil
}

const mappingSrc = `package fixture

import "time"

type Scalars struct {
	S   string
	I   int
	I64 int64
	U   uint32
	F   float64
	B   bool
	T   time.Time
	PS  *string
	SS  []string
	M   map[string]int
	Any interface{}
	Bytes []byte
}
`

func TestSchemaForScalars(t *testing.T) {
	pkg := checkPackage(t, mappingSrc)
	scalars := lookupType(t, pkg, "Scalars")

	tests := []struct {
		field      string
		wantType   string // "" means no top-level type (interface{})
		wantFormat string
		nullable   bool
		isArray    bool
		isObject   bool
	}{
		{field: "S", wantType: "string"},
		{field: "I", wantType: "integer"},
		{field: "I64", wantType: "integer"},
		{field: "U", wantType: "integer"},
		{field: "F", wantType: "number"},
		{field: "B", wantType: "boolean"},
		{field: "T", wantType: "string", wantFormat: "date-time"},
		{field: "PS", wantType: "string", nullable: true},
		{field: "SS", wantType: "array", isArray: true},
		{field: "M", wantType: "object", isObject: true},
		{field: "Any", wantType: ""},
		{field: "Bytes", wantType: "string", wantFormat: "byte"},
	}

	for _, tc := range tests {
		t.Run(tc.field, func(t *testing.T) {
			r := newSchemaRegistry()
			ref := r.schemaFor(fieldType(t, scalars, tc.field))
			if r.err != nil {
				t.Fatalf("registry error: %v", r.err)
			}
			if ref.Value == nil {
				t.Fatalf("expected inline schema, got ref %q", ref.Ref)
			}
			s := ref.Value
			gotType := ""
			if s.Type != nil && len(s.Type.Slice()) == 1 {
				gotType = s.Type.Slice()[0]
			}
			if gotType != tc.wantType {
				t.Errorf("type = %q, want %q", gotType, tc.wantType)
			}
			if s.Format != tc.wantFormat {
				t.Errorf("format = %q, want %q", s.Format, tc.wantFormat)
			}
			if s.Nullable != tc.nullable {
				t.Errorf("nullable = %v, want %v", s.Nullable, tc.nullable)
			}
			if tc.isArray && s.Items == nil {
				t.Error("array schema missing items")
			}
			if tc.isObject && s.AdditionalProperties.Schema == nil {
				t.Error("object schema missing additionalProperties")
			}
		})
	}
}

const structSrc = `package fixture

type Inner struct {
	Value string ` + "`json:\"value\"`" + `
}

type Outer struct {
	ID       string ` + "`json:\"id\"`" + `
	Optional string ` + "`json:\"optional,omitempty\"`" + `
	Renamed  int    ` + "`json:\"count\"`" + `
	Ignored  string ` + "`json:\"-\"`" + `
	Child    Inner  ` + "`json:\"child\"`" + `
	unexported string
}
`

func TestSchemaForStructRegistersComponentAndRef(t *testing.T) {
	pkg := checkPackage(t, structSrc)
	outer := lookupType(t, pkg, "Outer")

	r := newSchemaRegistry()
	ref := r.schemaFor(outer)
	if r.err != nil {
		t.Fatalf("registry error: %v", r.err)
	}
	// A struct must be referenced, not inlined.
	if ref.Ref != "#/components/schemas/fixture.Outer" {
		t.Fatalf("ref = %q, want fixture.Outer ref", ref.Ref)
	}

	outerSchema, ok := r.schemas["fixture.Outer"]
	if !ok {
		t.Fatal("Outer not registered as a component")
	}
	props := outerSchema.Value.Properties

	if _, ok := props["id"]; !ok {
		t.Error("missing property id")
	}
	if _, ok := props["count"]; !ok {
		t.Error("json rename to count not honored")
	}
	if _, ok := props["Ignored"]; ok {
		t.Error("json:\"-\" field should be omitted")
	}
	if _, ok := props["unexported"]; ok {
		t.Error("unexported field should be omitted")
	}
	if _, ok := r.schemas["fixture.Inner"]; !ok {
		t.Error("nested struct Inner should also be registered")
	}
	if child := props["child"]; child == nil || child.Ref != "#/components/schemas/fixture.Inner" {
		t.Error("child should reference Inner component")
	}

	// required excludes omitempty fields and is sorted.
	wantRequired := []string{"child", "count", "id"}
	if got := outerSchema.Value.Required; !equalStrings(got, wantRequired) {
		t.Errorf("required = %v, want %v", got, wantRequired)
	}
}

const embedSrc = `package fixture

type Base struct {
	CreatedAt string ` + "`json:\"created_at\"`" + `
}

type Named struct {
	Extra string ` + "`json:\"extra\"`" + `
}

type Doc struct {
	Base
	Name  string ` + "`json:\"name\"`" + `
	Named Named  ` + "`json:\"named\"`" + `
}
`

func TestSchemaForEmbeddedFlattening(t *testing.T) {
	pkg := checkPackage(t, embedSrc)
	doc := lookupType(t, pkg, "Doc")

	r := newSchemaRegistry()
	r.schemaFor(doc)
	if r.err != nil {
		t.Fatalf("registry error: %v", r.err)
	}
	props := r.schemas["fixture.Doc"].Value.Properties
	if _, ok := props["created_at"]; !ok {
		t.Error("embedded Base field should be promoted into Doc")
	}
	if _, ok := props["name"]; !ok {
		t.Error("missing own field name")
	}
	// A named (non-embedded) struct field is a ref, not flattened.
	if named := props["named"]; named == nil || named.Ref == "" {
		t.Error("named field should be a component ref")
	}
}

func TestSchemaNameCollisionIsReported(t *testing.T) {
	// Two distinct types that would map to the same component name must be a
	// reported error, never a silent merge.
	pkg := checkPackage(t, structSrc)
	outer := lookupType(t, pkg, "Outer").(*types.Named)

	r := newSchemaRegistry()
	r.schemaFor(outer)
	if r.err != nil {
		t.Fatalf("unexpected error: %v", r.err)
	}

	// Model a real collision: a DIFFERENT type already holds the name Outer
	// would map to. owners is only ever assigned a non-nil TypeName, so forging
	// a nil owner would exercise a state the production code cannot reach — and
	// would panic in the error path rather than prove anything.
	intruder := lookupType(t, pkg, "Inner").(*types.Named).Obj()
	r.owners[schemaName(outer.Obj())] = intruder

	if got := r.namedSchema(outer); got.Ref == "" {
		t.Error("expected a ref even on collision")
	}
	if r.err == nil {
		t.Fatal("expected a collision error to be recorded")
	}
	if !strings.Contains(r.err.Error(), "collision") {
		t.Errorf("error %q does not describe a collision", r.err)
	}
}

func TestModuleSegment(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"github.com/getnyx/influaudit/backend/internal/audit/api", "audit"},
		{"github.com/getnyx/influaudit/backend/internal/audit/internal/model", "audit"},
		{"github.com/getnyx/influaudit/backend/internal/connector", "connector"},
		{"time", ""},
	}
	for _, tc := range tests {
		if got := moduleSegment(tc.path); got != tc.want {
			t.Errorf("moduleSegment(%q) = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestParseJSONTag(t *testing.T) {
	tests := []struct {
		tag       string
		name      string
		hasName   bool
		omitempty bool
		skip      bool
	}{
		{tag: `json:"id"`, name: "id", hasName: true},
		{tag: `json:"id,omitempty"`, name: "id", hasName: true, omitempty: true},
		{tag: `json:"-"`, skip: true},
		{tag: `json:",omitempty"`, omitempty: true},
		{tag: ``, hasName: false},
	}
	for _, tc := range tests {
		name, hasName, omitempty, skip := parseJSONTag(tc.tag)
		if name != tc.name || hasName != tc.hasName || omitempty != tc.omitempty || skip != tc.skip {
			t.Errorf("parseJSONTag(%q) = (%q,%v,%v,%v), want (%q,%v,%v,%v)",
				tc.tag, name, hasName, omitempty, skip,
				tc.name, tc.hasName, tc.omitempty, tc.skip)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
