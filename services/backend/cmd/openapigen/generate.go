package main

import (
	"context"
	"fmt"
	"go/ast"
	"go/types"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"golang.org/x/tools/go/packages"
)

// specTitle and specVersion are document metadata, not business data. They are
// fixed so the generated contract is byte-stable across runs.
const (
	specTitle   = "InfluAudit API"
	specVersion = "0.1.0"
	openapiVer  = "3.0.3"

	// apiBasePath is the router group every module is mounted under. It is
	// duplicated in internal/app/router.go; the route-coverage test in that
	// package fails if the two ever disagree.
	apiBasePath = "/v1"
)

// loadMode is the set of facts go/packages must compute for the API packages:
// their syntax (for the HTTP doc comments) and full type information for the
// packages and their dependencies (to reflect request/response structs, which
// live in each module's internal/model package).
const loadMode = packages.NeedName |
	packages.NeedFiles |
	packages.NeedSyntax |
	packages.NeedTypes |
	packages.NeedTypesInfo |
	packages.NeedImports |
	packages.NeedDeps

// route is one parsed interface method: its HTTP binding and the Go types of its
// request body and response, resolved by the type checker.
type route struct {
	operationID    string
	verb           string
	ginPath        string   // path as written in the doc comment, with :params
	pathParams     []string // in path order
	requestSchema  *openapi3.SchemaRef
	responseSchema *openapi3.SchemaRef
}

// Generate discovers every internal/*/api/routes.go under root, reflects their
// annotated interfaces into an OpenAPI 3 document, validates it, and returns it.
// With no routes.go present it returns a valid document with an empty paths
// object rather than failing.
func Generate(ctx context.Context, root string) (*openapi3.T, error) {
	patterns, err := discoverAPIPatterns(root)
	if err != nil {
		return nil, err
	}

	registry := newSchemaRegistry()
	var routes []route
	if len(patterns) > 0 {
		routes, err = loadRoutes(root, patterns, registry)
		if err != nil {
			return nil, err
		}
	}
	if registry.err != nil {
		return nil, registry.err
	}

	doc, err := assemble(routes, registry)
	if err != nil {
		return nil, err
	}
	if err := validate(ctx, doc); err != nil {
		return nil, fmt.Errorf("generated spec is invalid: %w", err)
	}
	return doc, nil
}

// discoverAPIPatterns walks root/internal for */api/routes.go files and returns
// a sorted list of go/packages load patterns (e.g. "./internal/audit/api"), one
// per API package.
func discoverAPIPatterns(root string) ([]string, error) {
	internalDir := filepath.Join(root, "internal")
	seen := make(map[string]struct{})
	err := filepath.WalkDir(internalDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// A missing internal/ directory is not an error: it just means no
			// routes exist yet.
			if filepath.Clean(path) == filepath.Clean(internalDir) {
				return filepath.SkipDir
			}
			return err
		}
		if d.IsDir() || d.Name() != "routes.go" {
			return nil
		}
		dir := filepath.Dir(path)
		if filepath.Base(dir) != "api" {
			return nil
		}
		rel, err := filepath.Rel(root, dir)
		if err != nil {
			return err
		}
		seen["./"+filepath.ToSlash(rel)] = struct{}{}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("discover routes: %w", err)
	}

	patterns := make([]string, 0, len(seen))
	for p := range seen {
		patterns = append(patterns, p)
	}
	sort.Strings(patterns)
	return patterns, nil
}

// loadRoutes type-checks the API packages and extracts every annotated route,
// reflecting request/response types into registry as a side effect.
func loadRoutes(root string, patterns []string, registry *schemaRegistry) ([]route, error) {
	cfg := &packages.Config{Mode: loadMode, Dir: root}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, fmt.Errorf("load packages: %w", err)
	}
	var loadErr error
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		for _, e := range p.Errors {
			if loadErr == nil {
				loadErr = fmt.Errorf("package %s: %w", p.PkgPath, e)
			}
		}
	})
	if loadErr != nil {
		return nil, loadErr
	}

	var routes []route
	for _, pkg := range pkgs {
		for _, file := range pkg.Syntax {
			rs, err := routesInFile(pkg, file, registry)
			if err != nil {
				return nil, err
			}
			routes = append(routes, rs...)
		}
	}
	return routes, nil
}

// routesInFile extracts routes from every annotated interface declared in file.
func routesInFile(pkg *packages.Package, file *ast.File, registry *schemaRegistry) ([]route, error) {
	var routes []route
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			if _, ok := ts.Type.(*ast.InterfaceType); !ok {
				continue
			}
			iface, err := interfaceType(pkg, ts.Name.Name)
			if err != nil {
				return nil, err
			}
			rs, err := routesInInterface(ts.Type.(*ast.InterfaceType), iface, registry)
			if err != nil {
				return nil, err
			}
			routes = append(routes, rs...)
		}
	}
	return routes, nil
}

// interfaceType resolves the named interface in the package's type scope.
func interfaceType(pkg *packages.Package, name string) (*types.Interface, error) {
	obj := pkg.Types.Scope().Lookup(name)
	if obj == nil {
		return nil, fmt.Errorf("interface %s not found in package %s", name, pkg.PkgPath)
	}
	iface, ok := obj.Type().Underlying().(*types.Interface)
	if !ok {
		return nil, fmt.Errorf("%s is not an interface", name)
	}
	return iface, nil
}

// routesInInterface pairs each AST method (which carries the HTTP doc comment)
// with its type-checked signature to build routes. Methods without a recognized
// HTTP annotation are skipped, matching apigen's own tolerance.
func routesInInterface(node *ast.InterfaceType, iface *types.Interface, registry *schemaRegistry) ([]route, error) {
	sigs := make(map[string]*types.Signature, iface.NumMethods())
	for i := 0; i < iface.NumMethods(); i++ {
		m := iface.Method(i)
		sigs[m.Name()] = m.Type().(*types.Signature)
	}

	var routes []route
	for _, field := range node.Methods.List {
		if len(field.Names) == 0 {
			continue // embedded interface
		}
		name := field.Names[0].Name
		verb, path, ok := httpAnnotation(field.Doc)
		if !ok {
			continue
		}
		sig, ok := sigs[name]
		if !ok {
			return nil, fmt.Errorf("method %s has no signature", name)
		}
		r, err := buildRoute(name, verb, path, sig, registry)
		if err != nil {
			return nil, err
		}
		routes = append(routes, r)
	}
	return routes, nil
}

// buildRoute classifies a signature's parameters into path params and a request
// body and picks the first non-error result as the response, reflecting the
// body and response types into the registry.
func buildRoute(name, verb, path string, sig *types.Signature, registry *schemaRegistry) (route, error) {
	pathParams := extractPathParams(path)
	paramSet := make(map[string]struct{}, len(pathParams))
	for _, p := range pathParams {
		paramSet[p] = struct{}{}
	}

	r := route{operationID: name, verb: verb, ginPath: path, pathParams: pathParams}

	var request types.Type
	params := sig.Params()
	for i := 0; i < params.Len(); i++ {
		if i == 0 {
			continue // context.Context
		}
		p := params.At(i)
		if _, isPath := paramSet[p.Name()]; isPath {
			continue
		}
		if request != nil {
			return route{}, fmt.Errorf("method %s has more than one request body parameter", name)
		}
		request = p.Type()
	}

	var response types.Type
	results := sig.Results()
	for i := 0; i < results.Len(); i++ {
		rt := results.At(i).Type()
		if isError(rt) {
			continue
		}
		response = rt
		break
	}

	if request != nil {
		r.requestSchema = registry.schemaFor(request)
	}
	if response != nil {
		r.responseSchema = registry.schemaFor(response)
	}
	return r, nil
}

// assemble builds the OpenAPI document from the parsed routes and reflected
// schemas. Path items merge routes that share a path under different verbs, and
// duplicate paths+verb or operationIds are rejected.
func assemble(routes []route, registry *schemaRegistry) (*openapi3.T, error) {
	doc := &openapi3.T{
		OpenAPI: openapiVer,
		Info:    &openapi3.Info{Title: specTitle, Version: specVersion},
		// Every module mounts under the API version group, so the spec's paths
		// stay version-agnostic and the base path is declared once here. Without
		// this, a client generated from the spec would call /auth/login instead
		// of /v1/auth/login.
		Servers: openapi3.Servers{{URL: apiBasePath}},
		Paths:   openapi3.NewPaths(),
	}
	if len(registry.schemas) > 0 {
		doc.Components = &openapi3.Components{Schemas: registry.schemas}
	}

	items := make(map[string]*openapi3.PathItem)
	seenOps := make(map[string]struct{})

	// Sort for stable assignment: paths that share an item are visited together
	// and operationId collisions are reported deterministically.
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].ginPath != routes[j].ginPath {
			return routes[i].ginPath < routes[j].ginPath
		}
		return routes[i].verb < routes[j].verb
	})

	for _, r := range routes {
		if _, dup := seenOps[r.operationID]; dup {
			return nil, fmt.Errorf("duplicate operationId %q", r.operationID)
		}
		seenOps[r.operationID] = struct{}{}

		oaPath := ginToOpenAPIPath(r.ginPath)
		item := items[oaPath]
		if item == nil {
			item = &openapi3.PathItem{}
			items[oaPath] = item
		}
		if err := setOperation(item, r); err != nil {
			return nil, err
		}
	}

	for path, item := range items {
		doc.Paths.Set(path, item)
	}
	return doc, nil
}

// setOperation attaches the operation for r to the correct verb slot of item,
// rejecting a second route on the same path and verb.
func setOperation(item *openapi3.PathItem, r route) error {
	op := &openapi3.Operation{
		OperationID: r.operationID,
		Responses:   responsesFor(r),
	}
	for _, name := range r.pathParams {
		p := openapi3.NewPathParameter(name)
		p.Schema = openapi3.NewSchemaRef("", openapi3.NewStringSchema())
		op.Parameters = append(op.Parameters, &openapi3.ParameterRef{Value: p})
	}
	if r.requestSchema != nil {
		body := openapi3.NewRequestBody().
			WithRequired(true).
			WithJSONSchemaRef(r.requestSchema)
		op.RequestBody = &openapi3.RequestBodyRef{Value: body}
	}

	switch r.verb {
	case "GET":
		if item.Get != nil {
			return dupVerbErr(r)
		}
		item.Get = op
	case "POST":
		if item.Post != nil {
			return dupVerbErr(r)
		}
		item.Post = op
	case "PUT":
		if item.Put != nil {
			return dupVerbErr(r)
		}
		item.Put = op
	case "DELETE":
		if item.Delete != nil {
			return dupVerbErr(r)
		}
		item.Delete = op
	case "PATCH":
		if item.Patch != nil {
			return dupVerbErr(r)
		}
		item.Patch = op
	default:
		return fmt.Errorf("unsupported HTTP verb %q", r.verb)
	}
	return nil
}

func dupVerbErr(r route) error {
	return fmt.Errorf("duplicate route %s %s", r.verb, ginToOpenAPIPath(r.ginPath))
}

// responsesFor builds the responses object for a route: a single 200 carrying
// the response schema when the method returns a value, or a bodyless 200
// otherwise. Every response has a description, which OpenAPI requires.
func responsesFor(r route) *openapi3.Responses {
	desc := "OK"
	resp := &openapi3.Response{Description: &desc}
	if r.responseSchema != nil {
		resp.Content = openapi3.NewContentWithJSONSchemaRef(r.responseSchema)
	}
	responses := openapi3.NewResponses()
	responses.Set("200", &openapi3.ResponseRef{Value: resp})
	return responses
}

// validate marshals the document and reloads it through the loader so component
// $refs are resolved, then runs the built-in validator. A document that does not
// validate is a build failure.
func validate(ctx context.Context, doc *openapi3.T) error {
	raw, err := doc.MarshalJSON()
	if err != nil {
		return err
	}
	loader := openapi3.NewLoader()
	loader.Context = ctx
	loaded, err := loader.LoadFromData(raw)
	if err != nil {
		return err
	}
	return loaded.Validate(ctx)
}

// httpAnnotation parses "// GET /path" from a method's doc comment group.
func httpAnnotation(doc *ast.CommentGroup) (verb, path string, ok bool) {
	if doc == nil {
		return "", "", false
	}
	for _, c := range doc.List {
		text := strings.TrimSpace(strings.TrimPrefix(c.Text, "//"))
		parts := strings.SplitN(text, " ", 2)
		if len(parts) != 2 {
			continue
		}
		switch strings.ToUpper(parts[0]) {
		case "GET", "POST", "PUT", "DELETE", "PATCH":
			return strings.ToUpper(parts[0]), strings.TrimSpace(parts[1]), true
		}
	}
	return "", "", false
}

// extractPathParams returns the ordered names of ":name" segments in a path.
func extractPathParams(path string) []string {
	var params []string
	for _, seg := range strings.Split(path, "/") {
		if strings.HasPrefix(seg, ":") {
			params = append(params, strings.TrimPrefix(seg, ":"))
		}
	}
	return params
}

// ginToOpenAPIPath rewrites Gin ":name" path segments to OpenAPI "{name}".
func ginToOpenAPIPath(path string) string {
	segs := strings.Split(path, "/")
	for i, seg := range segs {
		if strings.HasPrefix(seg, ":") {
			segs[i] = "{" + strings.TrimPrefix(seg, ":") + "}"
		}
	}
	return strings.Join(segs, "/")
}

// isError reports whether t is the predeclared error interface.
func isError(t types.Type) bool {
	return types.Identical(t, types.Universe.Lookup("error").Type())
}
