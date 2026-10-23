package importer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/getkin/kin-openapi/openapi2"
	"github.com/getkin/kin-openapi/openapi2conv"
	"github.com/getkin/kin-openapi/openapi3"

	"github.com/Horac3/Primus/internal/model"
)

// DefaultTitle names the collection of a spec whose info.title is blank.
const DefaultTitle = "Imported API"

// Spec is a parsed spec: the title the importer names a collection from, and its operations.
type Spec struct {
	Title string
	Defs  []model.RequestDefinition
}

// FetchSpec fetches a spec from url and delegates to ParseSpec.
func FetchSpec(ctx context.Context, url string) (Spec, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Spec{}, fmt.Errorf("importer: build request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Spec{}, fmt.Errorf("importer: fetch %q: %w", url, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return Spec{}, fmt.Errorf("importer: read body: %w", err)
	}
	return ParseSpec(data)
}

// ParseSpec parses a Swagger 2.0 or OpenAPI 3.x spec and returns its title and one
// RequestDefinition per operation. Returns an error for malformed or structurally invalid
// specs; Defs is an empty (non-nil) slice for specs with no operations.
func ParseSpec(data []byte) (Spec, error) {
	doc, err := loadDoc(data)
	if err != nil {
		return Spec{}, err
	}
	title := DefaultTitle
	if doc.Info != nil && strings.TrimSpace(doc.Info.Title) != "" {
		title = strings.TrimSpace(doc.Info.Title)
	}
	return Spec{Title: title, Defs: extractDefinitions(doc)}, nil
}

// ImportFromURL fetches a spec from url and returns its operations.
func ImportFromURL(ctx context.Context, url string) ([]model.RequestDefinition, error) {
	spec, err := FetchSpec(ctx, url)
	if err != nil {
		return nil, err
	}
	return spec.Defs, nil
}

// ImportFromBytes parses a Swagger 2.0 or OpenAPI 3.x spec and returns one
// RequestDefinition per operation. Returns an error for malformed or
// structurally invalid specs; returns an empty (non-nil) slice for specs
// with no operations.
func ImportFromBytes(data []byte) ([]model.RequestDefinition, error) {
	spec, err := ParseSpec(data)
	if err != nil {
		return nil, err
	}
	return spec.Defs, nil
}

// loadDoc detects swagger 2.0 vs openapi 3.x and returns a validated *openapi3.T.
func loadDoc(data []byte) (*openapi3.T, error) {
	// Peek at the raw JSON to decide which parser to use.
	var probe struct {
		Swagger string `json:"swagger"`
		OpenAPI string `json:"openapi"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("importer: invalid JSON: %w", err)
	}
	if probe.Swagger == "" && probe.OpenAPI == "" {
		return nil, fmt.Errorf("importer: document is neither Swagger 2.0 nor OpenAPI 3.x (missing 'swagger'/'openapi' key)")
	}

	if strings.HasPrefix(probe.Swagger, "2.") {
		return loadSwagger2(data)
	}
	return loadOpenAPI3(data)
}

func loadSwagger2(data []byte) (*openapi3.T, error) {
	var doc2 openapi2.T
	if err := json.Unmarshal(data, &doc2); err != nil {
		return nil, fmt.Errorf("importer: parse swagger 2.0: %w", err)
	}
	doc3, err := openapi2conv.ToV3(&doc2)
	if err != nil {
		return nil, fmt.Errorf("importer: convert swagger 2.0 to openapi 3: %w", err)
	}
	return doc3, nil
}

func loadOpenAPI3(data []byte) (*openapi3.T, error) {
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromData(data)
	if err != nil {
		return nil, fmt.Errorf("importer: parse openapi 3: %w", err)
	}
	if err := doc.Validate(loader.Context); err != nil {
		return nil, fmt.Errorf("importer: validate openapi 3: %w", err)
	}
	return doc, nil
}

// extractDefinitions walks all paths/operations and produces RequestDefinitions.
func extractDefinitions(doc *openapi3.T) []model.RequestDefinition {
	var defs []model.RequestDefinition
	for path, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			defs = append(defs, operationToDefinition(method, path, op))
		}
	}
	return defs
}

func operationToDefinition(method, path string, op *openapi3.Operation) model.RequestDefinition {
	id := op.OperationID
	if id == "" {
		id = strings.ToUpper(method) + " " + path
	}

	def := model.RequestDefinition{
		ID:          id,
		Name:        id,
		Method:      strings.ToUpper(method),
		URLTemplate: path,
	}

	// Parameters (path + query)
	params := collectParams(op)
	if len(params) > 0 {
		def.QueryParams = params
	}

	// Request body
	def.Body = extractBody(op)

	return def
}

// collectParams builds a ParamSpec map from all path and query parameters.
func collectParams(op *openapi3.Operation) map[string]model.ParamSpec {
	if len(op.Parameters) == 0 {
		return nil
	}
	out := make(map[string]model.ParamSpec, len(op.Parameters))
	for _, pRef := range op.Parameters {
		p := pRef.Value
		if p == nil {
			continue
		}
		if p.In != "path" && p.In != "query" {
			continue
		}
		out[p.Name] = buildParamSpec(p)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func buildParamSpec(p *openapi3.Parameter) model.ParamSpec {
	spec := model.ParamSpec{}

	// Capture enum values if present.
	if p.Schema != nil && p.Schema.Value != nil {
		for _, ev := range p.Schema.Value.Enum {
			if s, ok := ev.(string); ok {
				spec.EnumValues = append(spec.EnumValues, s)
			}
		}
	}

	// Default Dynamic value based on schema type.
	schemaType := "string"
	if p.Schema != nil && p.Schema.Value != nil && p.Schema.Value.Type != nil {
		if types := p.Schema.Value.Type.Slice(); len(types) > 0 {
			schemaType = types[0]
		}
	}

	if schemaType == "integer" || schemaType == "number" {
		spec.Dynamic = &model.DynamicValue{
			Kind:   model.DynamicKindRandomInt,
			IntMin: 1,
			IntMax: 1000,
		}
	} else {
		spec.Dynamic = &model.DynamicValue{
			Kind:      model.DynamicKindRandomString,
			StringLen: 8,
		}
	}

	return spec
}

// extractBody generates a stub BodyTemplate from the operation's request body.
func extractBody(op *openapi3.Operation) *model.BodyTemplate {
	if op.RequestBody == nil || op.RequestBody.Value == nil {
		return nil
	}
	for _, mt := range op.RequestBody.Value.Content {
		if mt == nil || mt.Schema == nil || mt.Schema.Value == nil {
			continue
		}
		schema := mt.Schema.Value

		// Prefer the schema-level example.
		if schema.Example != nil {
			b, err := json.Marshal(schema.Example)
			if err == nil {
				return &model.BodyTemplate{Raw: b}
			}
		}
		// Prefer media-type example.
		if mt.Example != nil {
			b, err := json.Marshal(mt.Example)
			if err == nil {
				return &model.BodyTemplate{Raw: b}
			}
		}

		// Fall back: build minimal object from required properties.
		stub := buildMinimalObject(schema)
		b, err := json.Marshal(stub)
		if err == nil {
			return &model.BodyTemplate{Raw: b}
		}
	}
	return nil
}

// buildMinimalObject constructs a map[string]any from required properties.
func buildMinimalObject(schema *openapi3.Schema) map[string]any {
	obj := make(map[string]any)
	for _, reqProp := range schema.Required {
		propRef, ok := schema.Properties[reqProp]
		if !ok || propRef.Value == nil {
			obj[reqProp] = ""
			continue
		}
		propType := "string"
		if propRef.Value.Type != nil {
			if types := propRef.Value.Type.Slice(); len(types) > 0 {
				propType = types[0]
			}
		}
		switch propType {
		case "integer", "number":
			obj[reqProp] = 0
		case "boolean":
			obj[reqProp] = false
		case "array":
			obj[reqProp] = []any{}
		default:
			obj[reqProp] = ""
		}
	}
	// If no required props, add all defined props as stubs.
	if len(obj) == 0 {
		for name, propRef := range schema.Properties {
			if propRef.Value == nil {
				obj[name] = ""
				continue
			}
			propType := "string"
			if propRef.Value.Type != nil {
				if types := propRef.Value.Type.Slice(); len(types) > 0 {
					propType = types[0]
				}
			}
			switch propType {
			case "integer", "number":
				obj[name] = 0
			case "boolean":
				obj[name] = false
			case "array":
				obj[name] = []any{}
			default:
				obj[name] = ""
			}
		}
	}
	return obj
}
