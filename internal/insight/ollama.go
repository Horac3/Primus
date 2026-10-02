package insight

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const defaultLLMTimeout = 90 * time.Second

var numberPattern = regexp.MustCompile(`[-+]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][-+]?[0-9]+)?`)

type OllamaAnalyzer struct {
	URL    string
	Model  string
	Client *http.Client
}

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaChatRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	Format   map[string]any  `json:"format"`
	Options  struct {
		Temperature float64 `json:"temperature"`
	} `json:"options"`
	KeepAlive string `json:"keep_alive"`
}

type ollamaChatResponse struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
}

type ollamaInsight struct {
	Headline    string   `json:"headline"`
	Explanation string   `json:"explanation"`
	NextSteps   []string `json:"next_steps"`
}

func newOllamaAnalyzer(config Config) (Analyzer, error) {
	parsedURL, err := url.Parse(strings.TrimRight(config.LLMURL, "/"))
	if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.Host == "" {
		return nil, fmt.Errorf("invalid PRIMUS_LLM_URL %q", config.LLMURL)
	}
	model := strings.TrimSpace(config.LLMModel)
	if model == "" {
		return nil, fmt.Errorf("PRIMUS_LLM_MODEL is required for the ollama analyzer")
	}
	timeout := config.LLMTimeout
	if timeout <= 0 {
		timeout = defaultLLMTimeout
	}
	return OllamaAnalyzer{
		URL:    strings.TrimRight(config.LLMURL, "/"),
		Model:  model,
		Client: &http.Client{Timeout: timeout},
	}, nil
}

func (OllamaAnalyzer) Name() string { return "ollama" }

func (a OllamaAnalyzer) ModelName() string { return a.Model }

func (a OllamaAnalyzer) Analyze(ctx context.Context, summary Summary, findings []Finding) (Insight, error) {
	inputJSON, err := json.Marshal(struct {
		Summary  Summary   `json:"summary"`
		Findings []Finding `json:"findings"`
	}{Summary: summary, Findings: findings})
	if err != nil {
		return Insight{}, fmt.Errorf("marshal Ollama analysis input: %w", err)
	}
	requestBody := ollamaChatRequest{
		Model: a.Model,
		Messages: []ollamaMessage{
			{Role: "system", Content: "Explain load test results for a developer. Use only numbers present in the input JSON. Never invent numbers or causes not supported by the findings. Be brief."},
			{Role: "user", Content: string(inputJSON)},
		},
		Stream:    false,
		Format:    ollamaInsightSchema(),
		KeepAlive: "2m",
	}
	requestBody.Options.Temperature = 0
	encodedRequest, err := json.Marshal(requestBody)
	if err != nil {
		return Insight{}, fmt.Errorf("marshal Ollama chat request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.URL+"/api/chat", bytes.NewReader(encodedRequest))
	if err != nil {
		return Insight{}, fmt.Errorf("create Ollama chat request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	client := a.Client
	if client == nil {
		client = &http.Client{Timeout: defaultLLMTimeout}
	}
	response, err := client.Do(request)
	if err != nil {
		return Insight{}, fmt.Errorf("call Ollama chat API: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return Insight{}, fmt.Errorf("Ollama chat API returned %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	var chatResponse ollamaChatResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&chatResponse); err != nil {
		return Insight{}, fmt.Errorf("decode Ollama chat response: %w", err)
	}
	var generated ollamaInsight
	if err := json.Unmarshal([]byte(chatResponse.Message.Content), &generated); err != nil {
		return Insight{}, fmt.Errorf("decode Ollama insight JSON: %w", err)
	}
	if strings.TrimSpace(generated.Headline) == "" || strings.TrimSpace(generated.Explanation) == "" || generated.NextSteps == nil {
		return Insight{}, fmt.Errorf("Ollama insight is missing a required field")
	}
	textParts := []string{generated.Headline, generated.Explanation}
	textParts = append(textParts, generated.NextSteps...)
	text := strings.Join(textParts, " ")
	if err := verifyInsightNumbers(text, inputJSON); err != nil {
		return Insight{}, err
	}
	return Insight{
		Text:         text,
		Sentences:    textParts,
		Headline:     generated.Headline,
		Explanation:  generated.Explanation,
		NextSteps:    generated.NextSteps,
		AnalyzerName: a.Name(),
		ModelName:    a.ModelName(),
		CreatedAt:    time.Now().UTC(),
	}, nil
}

func ollamaInsightSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"headline":    map[string]string{"type": "string"},
			"explanation": map[string]string{"type": "string"},
			"next_steps": map[string]any{
				"type":  "array",
				"items": map[string]string{"type": "string"},
			},
		},
		"required":             []string{"headline", "explanation", "next_steps"},
		"additionalProperties": false,
	}
}

func verifyInsightNumbers(text string, inputJSON []byte) error {
	allowed := make(map[string]struct{})
	var input any
	decoder := json.NewDecoder(bytes.NewReader(inputJSON))
	decoder.UseNumber()
	if err := decoder.Decode(&input); err != nil {
		return fmt.Errorf("decode Ollama analysis input for number check: %w", err)
	}
	collectInputNumbers(input, allowed)
	for _, number := range numbersInText(text) {
		if _, ok := allowed[number]; !ok {
			return fmt.Errorf("Ollama insight contains unsupported number %q", number)
		}
	}
	return nil
}

func collectInputNumbers(value any, numbers map[string]struct{}) {
	switch typed := value.(type) {
	case json.Number:
		numbers[string(typed)] = struct{}{}
	case string:
		for _, number := range numbersInText(typed) {
			numbers[number] = struct{}{}
		}
	case []any:
		for _, item := range typed {
			collectInputNumbers(item, numbers)
		}
	case map[string]any:
		for key, item := range typed {
			if numberPattern.MatchString(key) && numberPattern.FindString(key) == key {
				numbers[key] = struct{}{}
			}
			collectInputNumbers(item, numbers)
		}
	}
}

func numbersInText(text string) []string {
	indices := numberPattern.FindAllStringIndex(text, -1)
	numbers := make([]string, 0, len(indices))
	for _, index := range indices {
		if index[0] > 0 {
			previous := text[index[0]-1]
			if previous == '_' || previous >= 'a' && previous <= 'z' || previous >= 'A' && previous <= 'Z' || previous >= '0' && previous <= '9' {
				continue
			}
		}
		numbers = append(numbers, text[index[0]:index[1]])
	}
	return numbers
}
