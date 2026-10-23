package web

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/Horac3/Primus/internal/author"
	"github.com/Horac3/Primus/internal/store"
)

func postNLRequestHandler(db *sql.DB, generator author.Generator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if generator == nil {
			http.Error(w, "natural-language authoring is unavailable; configure PRIMUS_ANALYZER=ollama", http.StatusServiceUnavailable)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		var input struct {
			Description string `json:"description"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			http.Error(w, "invalid request: "+err.Error(), http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(input.Description) == "" {
			http.Error(w, "description is required", http.StatusBadRequest)
			return
		}
		definitions, err := store.ListRequestDefinitions(db)
		if err != nil {
			http.Error(w, "list endpoints: "+err.Error(), http.StatusInternalServerError)
			return
		}
		endpoints := make([]author.Endpoint, 0, len(definitions))
		for _, definition := range definitions {
			endpoints = append(endpoints, author.Endpoint{
				ID: definition.ID, Name: definition.Name, Method: definition.Method, URL: definition.URLTemplate,
			})
		}
		sort.Slice(endpoints, func(i, j int) bool { return endpoints[i].ID < endpoints[j].ID })
		suggestion, err := generator.GenerateRequest(r.Context(), input.Description, endpoints)
		if err != nil {
			log.Printf("web: generate request definition: %v", err)
			http.Error(w, "request generation failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if err := json.NewEncoder(w).Encode(suggestion); err != nil {
			log.Printf("web: encode generated request definition: %v", err)
		}
	}
}

func postNLTestConfigHandler(db *sql.DB, generator author.Generator, bounds author.Bounds) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if generator == nil {
			http.Error(w, "natural-language authoring is unavailable; configure PRIMUS_ANALYZER=ollama", http.StatusServiceUnavailable)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		var input struct {
			RequestID   string `json:"request_id"`
			Description string `json:"description"`
		}
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			http.Error(w, "invalid request: "+err.Error(), http.StatusBadRequest)
			return
		}
		input.RequestID = strings.TrimSpace(input.RequestID)
		if input.RequestID == "" || strings.TrimSpace(input.Description) == "" {
			http.Error(w, "request_id and description are required", http.StatusBadRequest)
			return
		}
		definition, err := store.GetRequestDefinition(db, input.RequestID)
		if err != nil {
			http.Error(w, "load endpoint: "+err.Error(), http.StatusNotFound)
			return
		}
		suggestion, err := generator.GenerateTestConfig(r.Context(), input.Description, author.Endpoint{
			ID: definition.ID, Name: definition.Name, Method: definition.Method, URL: definition.URLTemplate,
		}, bounds)
		if err != nil {
			log.Printf("web: generate test config: %v", err)
			http.Error(w, "test config generation rejected: "+err.Error(), http.StatusUnprocessableEntity)
			return
		}
		if err := author.ValidateTestConfigSuggestion(suggestion, bounds); err != nil {
			log.Printf("web: generated test config failed server validation: %v", err)
			http.Error(w, "test config generation rejected: "+err.Error(), http.StatusUnprocessableEntity)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if err := json.NewEncoder(w).Encode(suggestion); err != nil {
			log.Printf("web: encode generated test config: %v", err)
		}
	}
}

func postGeneratePayloadsHandler(db *sql.DB, generator author.Generator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if generator == nil {
			http.Error(w, "payload generation is unavailable; configure PRIMUS_ANALYZER=ollama", http.StatusServiceUnavailable)
			return
		}
		requestedCount, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("count")))
		if err != nil || requestedCount < 1 {
			http.Error(w, "count must be a positive integer", http.StatusBadRequest)
			return
		}
		count := requestedCount
		if count > author.MaxPayloadCount {
			count = author.MaxPayloadCount
		}
		definition, err := store.GetRequestDefinition(db, r.PathValue("id"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if definition.Body == nil || len(definition.Body.Raw) == 0 {
			http.Error(w, "endpoint has no body template", http.StatusBadRequest)
			return
		}
		payloads, err := generator.GeneratePayloads(r.Context(), definition.Body.Raw, count)
		if err != nil {
			log.Printf("web: generate payload variants for endpoint %s: %v", definition.ID, err)
			http.Error(w, "payload generation failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		if err := author.ValidatePayloads(definition.Body.Raw, payloads, count); err != nil {
			log.Printf("web: generated payload pool failed server validation for endpoint %s: %v", definition.ID, err)
			http.Error(w, "payload generation rejected: "+err.Error(), http.StatusUnprocessableEntity)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		response := struct {
			Payloads       []json.RawMessage `json:"payloads"`
			RequestedCount int               `json:"requested_count"`
			GeneratedCount int               `json:"generated_count"`
			Capped         bool              `json:"capped"`
		}{
			Payloads: payloads, RequestedCount: requestedCount, GeneratedCount: len(payloads),
			Capped: requestedCount > count,
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			log.Printf("web: encode generated payloads for endpoint %s: %v", definition.ID, err)
		}
	}
}
