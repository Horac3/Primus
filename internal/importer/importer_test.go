package importer_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Horac3/Primus/internal/importer"
	"github.com/Horac3/Primus/internal/model"
)

// ---- helpers ---------------------------------------------------------------

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("readFixture %q: %v", name, err)
	}
	return b
}

func findByOperationID(defs []model.RequestDefinition, id string) (model.RequestDefinition, bool) {
	for _, d := range defs {
		if d.Name == id {
			return d, true
		}
	}
	return model.RequestDefinition{}, false
}

// ---- ImportFromBytes unit tests --------------------------------------------

func TestImportFromBytes_GETWithPathParam(t *testing.T) {
	// Arrange
	data := readFixture(t, "get_path_param.json")

	// Act
	defs, err := importer.ImportFromBytes(data)

	// Assert
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(defs) != 1 {
		t.Fatalf("len(defs) = %d, want 1", len(defs))
	}
	d := defs[0]
	if d.Method != "GET" {
		t.Errorf("Method = %q, want GET", d.Method)
	}
	if d.URLTemplate != "/pets/{petId}" {
		t.Errorf("URLTemplate = %q, want /pets/{petId}", d.URLTemplate)
	}
	param, ok := d.QueryParams["petId"]
	if !ok {
		t.Fatal("expected QueryParams[petId] to be set")
	}
	if param.Dynamic == nil {
		t.Fatal("expected Dynamic to be non-nil for path param")
	}
	if param.Dynamic.Kind != model.DynamicKindRandomInt {
		t.Errorf("Dynamic.Kind = %q, want %q", param.Dynamic.Kind, model.DynamicKindRandomInt)
	}
}

func TestImportFromBytes_GETWithEnumQueryParam(t *testing.T) {
	// Arrange
	data := readFixture(t, "get_enum_query.json")

	// Act
	defs, err := importer.ImportFromBytes(data)

	// Assert
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(defs) != 1 {
		t.Fatalf("len(defs) = %d, want 1", len(defs))
	}
	param, ok := defs[0].QueryParams["status"]
	if !ok {
		t.Fatal("expected QueryParams[status]")
	}
	if len(param.EnumValues) != 3 {
		t.Errorf("EnumValues len = %d, want 3", len(param.EnumValues))
	}
	wantEnums := map[string]bool{"available": true, "pending": true, "sold": true}
	for _, v := range param.EnumValues {
		if !wantEnums[v] {
			t.Errorf("unexpected EnumValue %q", v)
		}
	}
}

func TestImportFromBytes_POSTWithBody(t *testing.T) {
	// Arrange
	data := readFixture(t, "post_with_body.json")

	// Act
	defs, err := importer.ImportFromBytes(data)

	// Assert
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(defs) != 1 {
		t.Fatalf("len(defs) = %d, want 1", len(defs))
	}
	d := defs[0]
	if d.Method != "POST" {
		t.Errorf("Method = %q, want POST", d.Method)
	}
	if d.Body == nil {
		t.Fatal("expected Body to be non-nil")
	}
	if len(d.Body.Raw) == 0 {
		t.Error("expected Body.Raw to be non-empty")
	}
	// Body should contain "name" key from the example
	if !strings.Contains(string(d.Body.Raw), "name") {
		t.Errorf("Body.Raw does not contain 'name': %s", d.Body.Raw)
	}
}

// ---- ImportFromBytes petstore integration test -----------------------------

func TestImportFromBytes_PetstoreFixture(t *testing.T) {
	// Arrange
	data := readFixture(t, "petstore.json")

	// Act
	defs, err := importer.ImportFromBytes(data)

	// Assert
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(defs) != 20 {
		t.Errorf("len(defs) = %d, want 20", len(defs))
	}

	// getPetById: GET /pet/{petId}
	getPet, ok := findByOperationID(defs, "getPetById")
	if !ok {
		t.Error("getPetById not found")
	} else {
		if getPet.Method != "GET" {
			t.Errorf("getPetById method = %q, want GET", getPet.Method)
		}
		if getPet.URLTemplate != "/pet/{petId}" {
			t.Errorf("getPetById url = %q, want /pet/{petId}", getPet.URLTemplate)
		}
	}

	// addPet: POST /pet
	addPet, ok := findByOperationID(defs, "addPet")
	if !ok {
		t.Error("addPet not found")
	} else {
		if addPet.Method != "POST" {
			t.Errorf("addPet method = %q, want POST", addPet.Method)
		}
		if addPet.URLTemplate != "/pet" {
			t.Errorf("addPet url = %q, want /pet", addPet.URLTemplate)
		}
	}

	// findPetsByStatus: GET /pet/findByStatus
	findByStatus, ok := findByOperationID(defs, "findPetsByStatus")
	if !ok {
		t.Error("findPetsByStatus not found")
	} else {
		if findByStatus.Method != "GET" {
			t.Errorf("findPetsByStatus method = %q, want GET", findByStatus.Method)
		}
		if findByStatus.URLTemplate != "/pet/findByStatus" {
			t.Errorf("findPetsByStatus url = %q, want /pet/findByStatus", findByStatus.URLTemplate)
		}
	}
}

// ---- ImportFromURL test (httptest server, no real network) -----------------

func TestImportFromURL_UsesHTTPToFetchSpec(t *testing.T) {
	// Arrange
	data := readFixture(t, "get_path_param.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(data)
	}))
	defer srv.Close()

	// Act
	defs, err := importer.ImportFromURL(context.Background(), srv.URL+"/spec.json")

	// Assert
	if err != nil {
		t.Fatalf("ImportFromURL error: %v", err)
	}
	if len(defs) != 1 {
		t.Errorf("len(defs) = %d, want 1", len(defs))
	}
}

// ---- Malformed input -------------------------------------------------------

func TestImportFromBytes_MalformedJSON_ReturnsError(t *testing.T) {
	// Arrange
	data := []byte(`{ not valid json `)

	// Act
	_, err := importer.ImportFromBytes(data)

	// Assert
	if err == nil {
		t.Error("expected error for malformed JSON, got nil")
	}
}

func TestImportFromBytes_InvalidOpenAPI_ReturnsError(t *testing.T) {
	// Arrange — valid JSON but not a valid OpenAPI doc
	data := []byte(`{"hello": "world"}`)

	// Act
	_, err := importer.ImportFromBytes(data)

	// Assert
	if err == nil {
		t.Error("expected error for invalid OpenAPI spec, got nil")
	}
}

func TestImportFromBytes_EmptyPaths_ReturnsEmptySlice(t *testing.T) {
	// Arrange
	data := []byte(`{
		"openapi": "3.0.0",
		"info": {"title":"T","version":"1"},
		"paths": {}
	}`)

	// Act
	defs, err := importer.ImportFromBytes(data)

	// Assert
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(defs) != 0 {
		t.Errorf("len(defs) = %d, want 0", len(defs))
	}
}

// ---- Spec title (FR-COLL-08) -----------------------------------------------

func TestParseSpec_ReturnsTitleAndDefinitions(t *testing.T) {
	spec, err := importer.ParseSpec(readFixture(t, "get_path_param.json"))
	if err != nil {
		t.Fatal(err)
	}
	if spec.Title == "" || len(spec.Defs) != 1 {
		t.Fatalf("title %q, %d defs", spec.Title, len(spec.Defs))
	}
}

func TestParseSpec_BlankTitleFallsBack(t *testing.T) {
	data := []byte(`{"openapi":"3.0.0","info":{"title":"  ","version":"1"},"paths":{}}`)
	spec, err := importer.ParseSpec(data)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Title != importer.DefaultTitle {
		t.Fatalf("title %q, want %q", spec.Title, importer.DefaultTitle)
	}
}

func TestFetchSpec_ReturnsTitle(t *testing.T) {
	data := readFixture(t, "get_path_param.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(data) }))
	defer srv.Close()
	spec, err := importer.FetchSpec(context.Background(), srv.URL)
	if err != nil || spec.Title == "" {
		t.Fatalf("spec %+v, err %v", spec, err)
	}
}
