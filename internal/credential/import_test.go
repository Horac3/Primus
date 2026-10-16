package credential_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Horac3/Primus/internal/credential"
	"github.com/Horac3/Primus/internal/model"
)

// ---- ImportCSV -------------------------------------------------------------

func TestImportCSV_HeaderNamesFields(t *testing.T) {
	// Arrange
	in := "username,password,client_id\nalice,pw-a,c1\nbob,pw-b,c2\n"

	// Act
	res, err := credential.ImportCSV(strings.NewReader(in))

	// Assert
	if err != nil {
		t.Fatalf("ImportCSV: %v", err)
	}
	if len(res.Credentials) != 2 || len(res.Errors) != 0 {
		t.Fatalf("got %d credentials, %d errors; want 2, 0", len(res.Credentials), len(res.Errors))
	}
	want := model.Credential{"username": "bob", "password": "pw-b", "client_id": "c2"}
	if fmt.Sprint(res.Credentials[1]) != fmt.Sprint(want) {
		t.Errorf("row 2 = %v, want %v", res.Credentials[1], want)
	}
}

func TestImportCSV_BadRowsReportedWithFileRowNumber(t *testing.T) {
	// Arrange: row 3 has too few fields, row 4 is all empty.
	in := "username,password\nalice,pw-a\nbob\n,\ncarol,pw-c\n"

	// Act
	res, err := credential.ImportCSV(strings.NewReader(in))

	// Assert
	if err != nil {
		t.Fatalf("ImportCSV: %v", err)
	}
	if len(res.Credentials) != 2 {
		t.Errorf("kept %d credentials, want 2", len(res.Credentials))
	}
	if len(res.Errors) != 2 || res.Errors[0].Row != 3 || res.Errors[1].Row != 4 {
		t.Errorf("errors = %+v, want rows 3 and 4", res.Errors)
	}
}

func TestImportCSV_InvalidHeaderIsFatal(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"empty input", ""},
		{"blank column name", "username,,password\na,b,c\n"},
		{"duplicate column name", "username,username\na,b\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			_, err := credential.ImportCSV(strings.NewReader(tc.in))

			// Assert
			if err == nil {
				t.Error("ImportCSV: want an error")
			}
		})
	}
}

func TestImportCSV_DuplicateUsernameWarnsButKeepsRow(t *testing.T) {
	// Arrange
	in := "username,password\nalice,pw-1\nalice,pw-2\n"

	// Act
	res, err := credential.ImportCSV(strings.NewReader(in))

	// Assert
	if err != nil {
		t.Fatalf("ImportCSV: %v", err)
	}
	if len(res.Credentials) != 2 {
		t.Errorf("kept %d credentials, want 2", len(res.Credentials))
	}
	if len(res.Warnings) != 1 || res.Warnings[0].Row != 3 {
		t.Errorf("warnings = %+v, want one warning on row 3", res.Warnings)
	}
}

func TestImport_RejectsMoreThanMaxRows(t *testing.T) {
	// Arrange
	var csvIn strings.Builder
	var jsonIn strings.Builder
	csvIn.WriteString("username,password\n")
	jsonIn.WriteString("[")
	for i := 0; i <= model.MaxCredentialPoolRows; i++ {
		fmt.Fprintf(&csvIn, "u%d,p\n", i)
		if i > 0 {
			jsonIn.WriteString(",")
		}
		fmt.Fprintf(&jsonIn, `{"username":"u%d"}`, i)
	}
	jsonIn.WriteString("]")

	// Act
	_, csvErr := credential.ImportCSV(strings.NewReader(csvIn.String()))
	_, jsonErr := credential.ImportJSON(strings.NewReader(jsonIn.String()))

	// Assert
	if csvErr == nil || jsonErr == nil {
		t.Errorf("csv err = %v, json err = %v; want both to reject %d rows", csvErr, jsonErr, model.MaxCredentialPoolRows+1)
	}
}

func TestImportCSV_AcceptsExactlyMaxRows(t *testing.T) {
	// Arrange
	var in strings.Builder
	in.WriteString("username\n")
	for i := 0; i < model.MaxCredentialPoolRows; i++ {
		fmt.Fprintf(&in, "u%d\n", i)
	}

	// Act
	res, err := credential.ImportCSV(strings.NewReader(in.String()))

	// Assert
	if err != nil || len(res.Credentials) != model.MaxCredentialPoolRows {
		t.Errorf("got %d credentials, err %v; want %d", len(res.Credentials), err, model.MaxCredentialPoolRows)
	}
}

// ---- ImportJSON ------------------------------------------------------------

func TestImportJSON_ArrayOfObjects(t *testing.T) {
	// Arrange
	in := `[{"email":"a@x.io","password":"pw-a"},{"email":"b@x.io","password":"pw-b","tenant":42}]`

	// Act
	res, err := credential.ImportJSON(strings.NewReader(in))

	// Assert
	if err != nil {
		t.Fatalf("ImportJSON: %v", err)
	}
	if len(res.Credentials) != 2 || len(res.Errors) != 0 {
		t.Fatalf("got %d credentials, %d errors (%+v); want 2, 0", len(res.Credentials), len(res.Errors), res.Errors)
	}
	if res.Credentials[1]["tenant"] != "42" {
		t.Errorf("numeric field = %q, want \"42\"", res.Credentials[1]["tenant"])
	}
}

func TestImportJSON_BadRowsReported(t *testing.T) {
	// Arrange: row 2 is not an object, row 3 is empty, row 4 has a nested value.
	in := `[{"username":"a"}, "bob", {}, {"username":"c","meta":{"x":1}}, {"username":"d"}]`

	// Act
	res, err := credential.ImportJSON(strings.NewReader(in))

	// Assert
	if err != nil {
		t.Fatalf("ImportJSON: %v", err)
	}
	if len(res.Credentials) != 2 {
		t.Errorf("kept %d credentials, want 2", len(res.Credentials))
	}
	rows := make([]int, 0, len(res.Errors))
	for _, e := range res.Errors {
		rows = append(rows, e.Row)
	}
	if fmt.Sprint(rows) != "[2 3 4]" {
		t.Errorf("error rows = %v, want [2 3 4]", rows)
	}
}

func TestImportJSON_NotAnArrayIsFatal(t *testing.T) {
	// Act
	_, err := credential.ImportJSON(strings.NewReader(`{"username":"a"}`))

	// Assert
	if err == nil {
		t.Error("ImportJSON: want an error for a non-array document")
	}
}

func TestImport_RowErrorsNeverEchoValues(t *testing.T) {
	// Arrange
	in := "username,password\nalice,leaky-secret,extra\n"

	// Act
	res, err := credential.ImportCSV(strings.NewReader(in))

	// Assert
	if err != nil || len(res.Errors) != 1 {
		t.Fatalf("errors = %+v, err %v; want one row error", res.Errors, err)
	}
	if strings.Contains(res.Errors[0].Reason, "leaky-secret") {
		t.Errorf("reason %q leaks a credential value", res.Errors[0].Reason)
	}
}

// ---- masking ---------------------------------------------------------------

func TestIsSecretField(t *testing.T) {
	cases := map[string]bool{
		"password": true, "Password": true, "passwd": true, "client_secret": true,
		"api_key": true, "access_token": true,
		"username": false, "email": false, "client_id": false, "tenant": false,
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			// Act
			got := credential.IsSecretField(name)

			// Assert
			if got != want {
				t.Errorf("IsSecretField(%q) = %v, want %v", name, got, want)
			}
		})
	}
}

func TestMask_HidesSecretsAndKeepsIdentity(t *testing.T) {
	// Arrange
	c := model.Credential{"username": "alice", "password": "pw-a", "client_secret": "cs"}

	// Act
	masked := credential.Mask(c)

	// Assert
	if masked["username"] != "alice" {
		t.Errorf("username = %q, want it unmasked", masked["username"])
	}
	if masked["password"] == "pw-a" || masked["client_secret"] == "cs" {
		t.Errorf("masked = %v, want secret fields hidden", masked)
	}
	if c["password"] != "pw-a" {
		t.Error("Mask modified its input")
	}
}
