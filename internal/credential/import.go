package credential

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Horac3/Primus/internal/model"
)

// RowIssue is a problem with one imported row. Row is 1-based as the user
// sees the file: the CSV line number, or the JSON array position. Reason
// never contains credential values.
type RowIssue struct {
	Row    int    `json:"row"`
	Reason string `json:"reason"`
}

// ImportResult is what an import produced: the good rows plus row-level
// errors (row skipped) and warnings (row kept).
type ImportResult struct {
	Credentials []model.Credential
	Errors      []RowIssue
	Warnings    []RowIssue
}

var errTooManyRows = fmt.Errorf("pool exceeds the %d-row limit", model.MaxCredentialPoolRows)

// ImportCSV reads credentials from CSV whose header row names the fields.
// A malformed header or more than MaxCredentialPoolRows data rows is fatal;
// a bad data row is reported and skipped.
func ImportCSV(r io.Reader) (ImportResult, error) {
	reader := csv.NewReader(r)
	reader.FieldsPerRecord = -1
	header, err := reader.Read()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return ImportResult{}, fmt.Errorf("CSV is empty; the first row must name the fields")
		}
		return ImportResult{}, fmt.Errorf("read CSV header: %w", err)
	}
	if err := validateHeader(header); err != nil {
		return ImportResult{}, err
	}

	var res ImportResult
	dups := newDuplicateTracker()
	rows := 0
	for {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		rows++
		if rows > model.MaxCredentialPoolRows {
			return ImportResult{}, errTooManyRows
		}
		var parseErr *csv.ParseError
		if errors.As(err, &parseErr) {
			res.Errors = append(res.Errors, RowIssue{Row: parseErr.Line, Reason: "malformed CSV (" + parseErr.Err.Error() + ")"})
			continue
		}
		if err != nil {
			return ImportResult{}, fmt.Errorf("read CSV: %w", err)
		}
		line, _ := reader.FieldPos(0)
		if len(record) != len(header) {
			res.Errors = append(res.Errors, RowIssue{Row: line,
				Reason: fmt.Sprintf("has %d fields, header has %d", len(record), len(header))})
			continue
		}
		cred := make(model.Credential, len(header))
		empty := true
		for i, name := range header {
			cred[name] = record[i]
			if record[i] != "" {
				empty = false
			}
		}
		if empty {
			res.Errors = append(res.Errors, RowIssue{Row: line, Reason: "every field is empty"})
			continue
		}
		res.Warnings = append(res.Warnings, dups.check(cred, line)...)
		res.Credentials = append(res.Credentials, cred)
	}
	return res, nil
}

func validateHeader(header []string) error {
	seen := make(map[string]bool, len(header))
	for i, name := range header {
		name = strings.TrimSpace(name)
		header[i] = name
		if name == "" {
			return fmt.Errorf("CSV header column %d has no name", i+1)
		}
		if seen[name] {
			return fmt.Errorf("CSV header names field %q twice", name)
		}
		seen[name] = true
	}
	return nil
}

// ImportJSON reads credentials from a JSON array of objects. String,
// number and boolean values are kept as strings; nested values, nulls and
// non-object elements are reported and skipped.
func ImportJSON(r io.Reader) (ImportResult, error) {
	var items []json.RawMessage
	if err := json.NewDecoder(r).Decode(&items); err != nil {
		return ImportResult{}, fmt.Errorf("JSON must be an array of objects: %w", err)
	}
	if len(items) > model.MaxCredentialPoolRows {
		return ImportResult{}, errTooManyRows
	}

	var res ImportResult
	dups := newDuplicateTracker()
	for i, item := range items {
		row := i + 1
		cred, reason := credentialFromJSON(item)
		if reason != "" {
			res.Errors = append(res.Errors, RowIssue{Row: row, Reason: reason})
			continue
		}
		res.Warnings = append(res.Warnings, dups.check(cred, row)...)
		res.Credentials = append(res.Credentials, cred)
	}
	return res, nil
}

func credentialFromJSON(item json.RawMessage) (model.Credential, string) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(item, &fields); err != nil || fields == nil {
		return nil, "is not a JSON object"
	}
	if len(fields) == 0 {
		return nil, "object has no fields"
	}
	cred := make(model.Credential, len(fields))
	for name, raw := range fields {
		raw = bytes.TrimSpace(raw)
		switch {
		case len(raw) > 0 && raw[0] == '"':
			var s string
			if err := json.Unmarshal(raw, &s); err != nil {
				return nil, fmt.Sprintf("field %q is not a valid string", name)
			}
			cred[name] = s
		case bytes.Equal(raw, []byte("true")), bytes.Equal(raw, []byte("false")):
			cred[name] = string(raw)
		case len(raw) > 0 && (raw[0] == '-' || (raw[0] >= '0' && raw[0] <= '9')):
			cred[name] = string(raw)
		default:
			return nil, fmt.Sprintf("field %q must be a string, number or boolean", name)
		}
	}
	return cred, ""
}

// duplicateTracker warns when a row repeats an earlier row's identity
// (username, else email). Duplicates are allowed; the warning says which
// earlier row it repeats without echoing the value.
type duplicateTracker struct {
	firstRow map[string]int
}

func newDuplicateTracker() *duplicateTracker {
	return &duplicateTracker{firstRow: make(map[string]int)}
}

func (d *duplicateTracker) check(c model.Credential, row int) []RowIssue {
	for _, field := range []string{"username", "email"} {
		value, ok := c[field]
		if !ok || value == "" {
			continue
		}
		key := field + "\x00" + value
		if first, seen := d.firstRow[key]; seen {
			return []RowIssue{{Row: row, Reason: fmt.Sprintf("duplicate %s (same as row %d)", field, first)}}
		}
		d.firstRow[key] = row
		return nil
	}
	return nil
}

// secretFieldMarkers are substrings that mark a field name as secret.
var secretFieldMarkers = []string{"pass", "secret", "token", "key", "pin", "otp"}

// IsSecretField reports whether a credential field holds a secret, judged
// by its name (password, client_secret, api_key, access_token, ...).
func IsSecretField(name string) bool {
	lower := strings.ToLower(name)
	for _, marker := range secretFieldMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// MaskedValue replaces secret values in read APIs.
const MaskedValue = "••••••"

// Mask returns a copy of c with every secret field's value hidden.
func Mask(c model.Credential) model.Credential {
	out := make(model.Credential, len(c))
	for name, value := range c {
		if IsSecretField(name) {
			value = MaskedValue
		}
		out[name] = value
	}
	return out
}
