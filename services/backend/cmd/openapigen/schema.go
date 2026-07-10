package main

import (
	"fmt"
	"go/types"
	"reflect"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// schemaRegistry reflects Go types into OpenAPI schemas. Named struct types are
// hoisted into components.schemas and referenced by $ref; everything else is
// rendered inline. It records the defining object for every component name so a
// name collision between two distinct types (which would silently merge into an
// invalid schema) is reported as a build error rather than hidden.
type schemaRegistry struct {
	schemas openapi3.Schemas
	owners  map[string]*types.TypeName
	err     error
}

func newSchemaRegistry() *schemaRegistry {
	return &schemaRegistry{
		schemas: make(openapi3.Schemas),
		owners:  make(map[string]*types.TypeName),
	}
}

// fail records the first error encountered while reflecting a type. Reflection
// continues so a single pass surfaces a usable document shape, but the caller
// must treat a non-nil err as fatal.
func (r *schemaRegistry) fail(err error) {
	if r.err == nil {
		r.err = err
	}
}

// schemaFor returns a SchemaRef for t: a $ref for named struct types (which it
// registers as a side effect) and an inline schema for everything else.
func (r *schemaRegistry) schemaFor(t types.Type) *openapi3.SchemaRef {
	switch u := t.(type) {
	case *types.Named:
		return r.namedSchema(u)
	case *types.Pointer:
		return nullable(r.schemaFor(u.Elem()))
	case *types.Slice:
		return r.sliceSchema(u.Elem())
	case *types.Array:
		return r.sliceSchema(u.Elem())
	case *types.Map:
		return r.mapSchema(u)
	case *types.Basic:
		return openapi3.NewSchemaRef("", basicSchema(u))
	case *types.Interface:
		// An open interface (e.g. any) maps to an unconstrained schema, which
		// permits any JSON value.
		return openapi3.NewSchemaRef("", openapi3.NewSchema())
	default:
		return openapi3.NewSchemaRef("", openapi3.NewSchema())
	}
}

// namedSchema handles a defined type: the well-known scalar wrappers time.Time
// and uuid.UUID become formatted strings, struct-backed types become component
// references, and any other wrapper (e.g. `type Platform string`) is reflected
// through its underlying type.
func (r *schemaRegistry) namedSchema(n *types.Named) *openapi3.SchemaRef {
	obj := n.Obj()
	switch {
	case isTime(obj):
		return openapi3.NewSchemaRef("", openapi3.NewDateTimeSchema())
	case isUUID(obj):
		return openapi3.NewSchemaRef("", openapi3.NewUUIDSchema())
	}

	if st, ok := n.Underlying().(*types.Struct); ok {
		name := schemaName(obj)
		if prior, seen := r.owners[name]; seen {
			if prior != obj {
				r.fail(fmt.Errorf("schema name collision %q: %s and %s",
					name, prior.Pkg().Path(), obj.Pkg().Path()))
			}
			return refTo(name)
		}
		// Reserve the name before descending so a self-referential struct
		// resolves to its own $ref instead of recursing forever.
		r.owners[name] = obj
		schema := openapi3.NewObjectSchema()
		r.schemas[name] = openapi3.NewSchemaRef("", schema)
		r.fillStruct(schema, st)
		return refTo(name)
	}

	return r.schemaFor(n.Underlying())
}

// fillStruct populates schema.Properties and schema.Required from the fields of
// st, honoring json tags (rename, omitempty, and "-" skip) and Go's embedded
// field promotion.
func (r *schemaRegistry) fillStruct(schema *openapi3.Schema, st *types.Struct) {
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		name, hasName, omitempty, skip := parseJSONTag(st.Tag(i))
		if skip {
			continue
		}
		// An embedded field with no explicit json name promotes its fields into
		// the parent object, matching encoding/json.
		if f.Anonymous() && !hasName {
			if embedded := structUnder(f.Type()); embedded != nil {
				r.fillStruct(schema, embedded)
				continue
			}
		}
		if !f.Exported() {
			continue
		}
		propName := name
		if !hasName {
			propName = f.Name()
		}
		schema.Properties[propName] = r.schemaFor(f.Type())
		if !omitempty {
			schema.Required = append(schema.Required, propName)
		}
	}
	sort.Strings(schema.Required)
}

// sliceSchema maps a slice or array element type to an array schema, treating a
// byte sequence as a base64-encoded string per JSON convention.
func (r *schemaRegistry) sliceSchema(elem types.Type) *openapi3.SchemaRef {
	if b, ok := elem.Underlying().(*types.Basic); ok && b.Kind() == types.Byte {
		return openapi3.NewSchemaRef("", openapi3.NewBytesSchema())
	}
	arr := openapi3.NewArraySchema()
	arr.Items = r.schemaFor(elem)
	return openapi3.NewSchemaRef("", arr)
}

// mapSchema maps a Go map to a JSON object whose additionalProperties describe
// the value type.
func (r *schemaRegistry) mapSchema(m *types.Map) *openapi3.SchemaRef {
	obj := openapi3.NewObjectSchema()
	obj.Properties = nil
	obj.AdditionalProperties = openapi3.AdditionalProperties{Schema: r.schemaFor(m.Elem())}
	return openapi3.NewSchemaRef("", obj)
}

// basicSchema maps a Go basic (predeclared) type to its JSON Schema equivalent.
// It tests the type's info bits, so it covers every signed and unsigned integer
// width as well as both float widths without enumerating each kind.
func basicSchema(b *types.Basic) *openapi3.Schema {
	info := b.Info()
	switch {
	case info&types.IsBoolean != 0:
		return openapi3.NewBoolSchema()
	case info&types.IsInteger != 0:
		return openapi3.NewIntegerSchema()
	case info&types.IsFloat != 0:
		return openapi3.NewFloat64Schema()
	case info&types.IsString != 0:
		return openapi3.NewStringSchema()
	default:
		return openapi3.NewSchema()
	}
}

// nullable marks an inline schema as nullable. A $ref cannot carry a sibling
// nullable in OpenAPI 3.0, so a pointer to a component type is returned as the
// bare reference.
func nullable(ref *openapi3.SchemaRef) *openapi3.SchemaRef {
	if ref.Ref != "" || ref.Value == nil {
		return ref
	}
	ref.Value.Nullable = true
	return ref
}

func refTo(name string) *openapi3.SchemaRef {
	return openapi3.NewSchemaRef("#/components/schemas/"+name, nil)
}

// structUnder returns the struct underlying t (dereferencing a pointer), or nil
// if t is not struct-backed.
func structUnder(t types.Type) *types.Struct {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	if st, ok := t.Underlying().(*types.Struct); ok {
		return st
	}
	return nil
}

// parseJSONTag extracts the json-tag name and options from a struct tag. hasName
// reports whether the tag supplied an explicit field name; skip reports the "-"
// directive.
func parseJSONTag(tag string) (name string, hasName, omitempty, skip bool) {
	value, ok := lookupTag(tag, "json")
	if !ok {
		return "", false, false, false
	}
	parts := strings.Split(value, ",")
	first := parts[0]
	for _, opt := range parts[1:] {
		if opt == "omitempty" {
			omitempty = true
		}
	}
	if first == "-" && len(parts) == 1 {
		return "", false, false, true
	}
	if first != "" {
		return first, true, omitempty, false
	}
	return "", false, omitempty, false
}

// lookupTag returns the value associated with key in a raw struct tag string.
func lookupTag(tag, key string) (string, bool) {
	return reflect.StructTag(tag).Lookup(key)
}

// schemaName derives a document-unique, human-readable component name for a
// named type: the owning module segment joined to the type name (e.g.
// "audit.Audit"), which disambiguates identically named types across modules.
func schemaName(obj *types.TypeName) string {
	seg := moduleSegment(obj.Pkg().Path())
	if seg == "" {
		seg = obj.Pkg().Name()
	}
	return seg + "." + obj.Name()
}

// moduleSegment returns the module directory name embedded in a backend package
// path (the segment immediately after "/internal/"), or "" when the path has no
// such segment.
func moduleSegment(pkgPath string) string {
	const marker = "/internal/"
	i := strings.Index(pkgPath, marker)
	if i < 0 {
		return ""
	}
	rest := pkgPath[i+len(marker):]
	if j := strings.IndexByte(rest, '/'); j >= 0 {
		return rest[:j]
	}
	return rest
}

func isTime(obj *types.TypeName) bool {
	return obj.Pkg() != nil && obj.Pkg().Path() == "time" && obj.Name() == "Time"
}

func isUUID(obj *types.TypeName) bool {
	return obj.Pkg() != nil && obj.Pkg().Name() == "uuid" && obj.Name() == "UUID"
}
