package author

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const defaultGeneratorTimeout = 90 * time.Second

type Generator interface {
	GenerateRequest(ctx context.Context, description string, endpoints []Endpoint) (RequestSuggestion, error)
}

type Config struct {
	Analyzer string
	URL      string
	Model    string
	Timeout  time.Duration
}

type Endpoint struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Method string `json:"method"`
	URL    string `json:"url"`
}

type RequestPrompt struct {
	Description string     `json:"description"`
	Endpoints   []Endpoint `json:"endpoints"`
}

type RequestSuggestion struct {
	MatchedExistingID *string           `json:"matched_existing_id"`
	Method            string            `json:"method"`
	URL               string            `json:"url"`
	Headers           map[string]string `json:"headers"`
	Body              string            `json:"body"`
	ConfidenceNote    string            `json:"confidence_note"`
}

func NewGenerator(config Config) (Generator, error) {
	switch strings.ToLower(strings.TrimSpace(config.Analyzer)) {
	case "", "rules":
		return nil, nil
	case "ollama":
		return NewOllamaGenerator(config)
	default:
		return nil, fmt.Errorf("unsupported author generator %q", config.Analyzer)
	}
}

func ResolveRequestSuggestion(suggestion RequestSuggestion, endpoints []Endpoint) (RequestSuggestion, error) {
	suggestion.Method = strings.ToUpper(strings.TrimSpace(suggestion.Method))
	if !validMethod(suggestion.Method) {
		return RequestSuggestion{}, fmt.Errorf("generated method %q is invalid", suggestion.Method)
	}
	if suggestion.MatchedExistingID != nil {
		for _, endpoint := range endpoints {
			if endpoint.ID == *suggestion.MatchedExistingID {
				suggestion.Method = strings.ToUpper(endpoint.Method)
				suggestion.URL = endpoint.URL
				if !validMethod(suggestion.Method) {
					return RequestSuggestion{}, fmt.Errorf("matched endpoint %q has invalid method %q", endpoint.ID, endpoint.Method)
				}
				return validateRequestSuggestion(suggestion)
			}
		}
		return RequestSuggestion{}, fmt.Errorf("generated matched_existing_id %q is not in the provided endpoint list", *suggestion.MatchedExistingID)
	}
	if err := validateAbsoluteHTTPURL(suggestion.URL); err != nil {
		return RequestSuggestion{}, err
	}
	return validateRequestSuggestion(suggestion)
}

func validateRequestSuggestion(suggestion RequestSuggestion) (RequestSuggestion, error) {
	if len(suggestion.Headers) > 100 {
		return RequestSuggestion{}, fmt.Errorf("generated headers exceed the maximum of 100")
	}
	for name, value := range suggestion.Headers {
		if !validHeaderName(name) || strings.ContainsAny(value, "\r\n") {
			return RequestSuggestion{}, fmt.Errorf("generated header %q is invalid", name)
		}
	}
	if suggestion.Body != "" && !jsonValid([]byte(suggestion.Body)) {
		return RequestSuggestion{}, fmt.Errorf("generated body must be valid JSON")
	}
	if len(suggestion.Body) > 1<<20 {
		return RequestSuggestion{}, fmt.Errorf("generated body exceeds the 1 MiB limit")
	}
	suggestion.ConfidenceNote = strings.TrimSpace(suggestion.ConfidenceNote)
	if suggestion.ConfidenceNote == "" {
		return RequestSuggestion{}, fmt.Errorf("generated confidence_note is required")
	}
	return suggestion, nil
}

func validateAbsoluteHTTPURL(raw string) error {
	parsed, err := url.ParseRequestURI(strings.TrimSpace(raw))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil {
		return fmt.Errorf("generated URL must be an absolute http:// or https:// URL without credentials")
	}
	return nil
}

func validMethod(method string) bool {
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
		return true
	default:
		return false
	}
}

func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, char := range name {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' ||
			char >= '0' && char <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", char)) {
			return false
		}
	}
	return true
}

func jsonValid(data []byte) bool {
	return len(data) > 0 && json.Valid(data)
}
