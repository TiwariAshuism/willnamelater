package main

import (
	"encoding/json"
	"fmt"

	"github.com/getkin/kin-openapi/openapi3"
	goyaml "github.com/goccy/go-yaml"
)

// Marshal renders doc to deterministic YAML. The document is first marshaled to
// JSON through kin-openapi (which renders $refs correctly), then re-encoded as
// YAML with goccy/go-yaml, whose map-key sorting makes the output byte-stable
// regardless of Go map iteration order. The only ordered collections in the
// document — required[] and the path parameter list — are sorted at build time.
func Marshal(doc *openapi3.T) ([]byte, error) {
	raw, err := doc.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("marshal json: %w", err)
	}
	// Decode into a generic tree so every object key becomes a map key that
	// goccy sorts; struct field order from kin-openapi is intentionally dropped.
	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		return nil, fmt.Errorf("decode json: %w", err)
	}
	out, err := goyaml.Marshal(tree)
	if err != nil {
		return nil, fmt.Errorf("marshal yaml: %w", err)
	}
	return out, nil
}
