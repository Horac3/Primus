package author

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const (
	MaxPayloadCount         = 500
	MaxPayloadTemplateBytes = 64 << 10
	MaxPayloadPoolBytes     = 1 << 20
	maxAuthorResponseBytes  = 8 << 20
)

type PayloadPrompt struct {
	Template json.RawMessage `json:"template"`
	Count    int             `json:"count"`
}

func ValidatePayloads(template json.RawMessage, payloads []json.RawMessage, expectedCount int) error {
	if len(template) == 0 || len(template) > MaxPayloadTemplateBytes || !json.Valid(template) {
		return fmt.Errorf("body template must be valid JSON no larger than %d bytes", MaxPayloadTemplateBytes)
	}
	if expectedCount < 1 || expectedCount > MaxPayloadCount {
		return fmt.Errorf("payload count must be between 1 and %d", MaxPayloadCount)
	}
	if len(payloads) != expectedCount {
		return fmt.Errorf("generated %d payloads, want %d", len(payloads), expectedCount)
	}

	var total int
	var templateValue any
	if err := json.Unmarshal(template, &templateValue); err != nil {
		return fmt.Errorf("decode body template: %w", err)
	}
	for i, payload := range payloads {
		if len(payload) == 0 || !json.Valid(payload) {
			return fmt.Errorf("generated payload %d is not valid JSON", i+1)
		}
		total += len(payload)
		if total > MaxPayloadPoolBytes {
			return fmt.Errorf("generated payload pool exceeds the %d-byte limit", MaxPayloadPoolBytes)
		}
		var payloadValue any
		if err := json.Unmarshal(payload, &payloadValue); err != nil {
			return fmt.Errorf("decode generated payload %d: %w", i+1, err)
		}
		if !sameJSONShape(templateValue, payloadValue) {
			return fmt.Errorf("generated payload %d does not match the body template shape", i+1)
		}
	}
	return nil
}

func sameJSONShape(template, payload any) bool {
	switch expected := template.(type) {
	case map[string]any:
		actual, ok := payload.(map[string]any)
		if !ok || len(expected) != len(actual) {
			return false
		}
		for key, value := range expected {
			actualValue, ok := actual[key]
			if !ok || !sameJSONShape(value, actualValue) {
				return false
			}
		}
		return true
	case []any:
		actual, ok := payload.([]any)
		if !ok || len(expected) != len(actual) {
			return false
		}
		for i := range expected {
			if !sameJSONShape(expected[i], actual[i]) {
				return false
			}
		}
		return true
	case string:
		_, ok := payload.(string)
		return ok
	case float64:
		_, ok := payload.(float64)
		return ok
	case bool:
		_, ok := payload.(bool)
		return ok
	case nil:
		return payload == nil
	default:
		return false
	}
}

func decodePayloadResponse(content string) ([]json.RawMessage, error) {
	var response struct {
		Payloads []json.RawMessage `json:"payloads"`
	}
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return nil, fmt.Errorf("decode generated payloads: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("decode generated payloads: expected one JSON object")
	}
	return response.Payloads, nil
}

func payloadsJSONSchema(count int) map[string]any {
	types := []any{
		map[string]any{"type": "object"},
		map[string]any{"type": "array"},
		map[string]any{"type": "string"},
		map[string]any{"type": "number"},
		map[string]any{"type": "boolean"},
		map[string]any{"type": "null"},
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"payloads": map[string]any{
				"type": "array", "items": map[string]any{"anyOf": types},
				"minItems": count, "maxItems": count,
			},
		},
		"required":             []string{"payloads"},
		"additionalProperties": false,
	}
}

func compactPayloadJSON(payload json.RawMessage) json.RawMessage {
	var compact bytes.Buffer
	if err := json.Compact(&compact, payload); err != nil {
		return payload
	}
	return compact.Bytes()
}
