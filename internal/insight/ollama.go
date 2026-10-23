package insight

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const defaultLLMTimeout = 90 * time.Second

var numberPattern = regexp.MustCompile(`[-+]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][-+]?[0-9]+)?`)
var ordinalSuffixPattern = regexp.MustCompile(`(?i)^(?:st|nd|rd|th)\b`)
var percentageSuffixPattern = regexp.MustCompile(`(?i)^\s*(?:%|percent(?:age)?\b)`)

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
			{Role: "system", Content: "Explain load test results for a developer. Use only numbers present in the input JSON. When citing a value from the input, keep its original unit and precision. Never invent numbers or causes not supported by the findings. Be brief."},
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
	if strings.TrimSpace(generated.Headline) == "" || strings.TrimSpace(generated.Explanation) == "" || len(generated.NextSteps) == 0 {
		return Insight{}, fmt.Errorf("Ollama insight is missing a required field")
	}
	textParts := []string{generated.Headline, generated.Explanation}
	textParts = append(textParts, generated.NextSteps...)
	text := strings.Join(textParts, " ")
	if err := verifyInsightNumbers(text, inputJSON); err != nil {
		slog.Debug("Ollama insight rejected by numeric grounding check",
			"response", chatResponse.Message.Content,
			"error", err,
		)
		return Insight{}, err
	}
	return Insight{
		Text:        text,
		Sentences:   textParts,
		Headline:    generated.Headline,
		Explanation: generated.Explanation,
		NextSteps:   generated.NextSteps,
		// The model's headline states the numbers, its explanation the likely causes. The comparison is
		// written by code so it cannot misquote either run (FR-INS-03).
		Sections: Sections{
			Numbers:    []string{generated.Headline},
			Causes:     []string{generated.Explanation},
			NextSteps:  generated.NextSteps,
			Comparison: comparisonLines(summary),
		},
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
	allowed := make([]float64, 0)
	var input any
	decoder := json.NewDecoder(bytes.NewReader(inputJSON))
	decoder.UseNumber()
	if err := decoder.Decode(&input); err != nil {
		return fmt.Errorf("decode Ollama analysis input for number check: %w", err)
	}
	collectInputNumbers(input, &allowed)
	for _, index := range numberPattern.FindAllStringIndex(text, -1) {
		if numberIsEmbedded(text, index[0]) || ordinalSuffixPattern.MatchString(text[index[1]:]) {
			continue
		}
		number := text[index[0]:index[1]]
		outputNumber, err := strconv.ParseFloat(number, 64)
		if err != nil || (!isGrounded(outputNumber, allowed) &&
			!(percentageSuffixPattern.MatchString(text[index[1]:]) && isRoundedPercentage(number, allowed))) {
			return fmt.Errorf("Ollama insight contains unsupported number %q", number)
		}
	}
	return nil
}

func collectInputNumbers(value any, numbers *[]float64) {
	switch typed := value.(type) {
	case json.Number:
		appendGroundingNumber(string(typed), numbers)
	case string:
		for _, number := range numbersInText(typed) {
			appendGroundingNumber(number, numbers)
		}
	case []any:
		for _, item := range typed {
			collectInputNumbers(item, numbers)
		}
	case map[string]any:
		for key, item := range typed {
			if numberPattern.MatchString(key) && numberPattern.FindString(key) == key {
				appendGroundingNumber(key, numbers)
			}
			collectInputNumbers(item, numbers)
		}
	}
}

func appendGroundingNumber(number string, numbers *[]float64) {
	value, err := strconv.ParseFloat(number, 64)
	if err == nil && !math.IsInf(value, 0) && !math.IsNaN(value) {
		*numbers = append(*numbers, value)
	}
}

func isGrounded(outputNumber float64, inputNumbers []float64) bool {
	if math.IsInf(outputNumber, 0) || math.IsNaN(outputNumber) {
		return false
	}
	for _, inputNumber := range inputNumbers {
		for _, candidate := range [...]float64{inputNumber, inputNumber * 100, inputNumber / 100} {
			if math.IsInf(candidate, 0) || math.IsNaN(candidate) {
				continue
			}
			if candidate == 0 {
				if math.Abs(outputNumber) < 1e-6 {
					return true
				}
				continue
			}
			relativeDifference := math.Abs(outputNumber-candidate) / math.Abs(candidate)
			if relativeDifference < 0.02 {
				return true
			}
		}
	}
	return false
}

func isRoundedPercentage(output string, inputNumbers []float64) bool {
	outputNumber, err := strconv.ParseFloat(output, 64)
	if err != nil || math.IsInf(outputNumber, 0) || math.IsNaN(outputNumber) {
		return false
	}
	decimalPlaces, err := displayedDecimalPlaces(output)
	if err != nil || decimalPlaces > 308 {
		return false
	}
	roundingTolerance := 0.5 * math.Pow(10, -float64(decimalPlaces))
	for _, inputNumber := range inputNumbers {
		percentage := inputNumber * 100
		if math.IsInf(percentage, 0) || math.IsNaN(percentage) {
			continue
		}
		if math.Abs(outputNumber-percentage) <= roundingTolerance {
			return true
		}
	}
	return false
}

func displayedDecimalPlaces(number string) (int, error) {
	mantissa := number
	exponent := 0
	if exponentIndex := strings.IndexAny(number, "eE"); exponentIndex >= 0 {
		mantissa = number[:exponentIndex]
		var err error
		exponent, err = strconv.Atoi(number[exponentIndex+1:])
		if err != nil {
			return 0, err
		}
	}
	decimalPlaces := 0
	if decimalIndex := strings.IndexByte(mantissa, '.'); decimalIndex >= 0 {
		decimalPlaces = len(mantissa) - decimalIndex - 1
	}
	decimalPlaces -= exponent
	if decimalPlaces < 0 {
		decimalPlaces = 0
	}
	return decimalPlaces, nil
}

func numbersInText(text string) []string {
	indices := numberPattern.FindAllStringIndex(text, -1)
	numbers := make([]string, 0, len(indices))
	for _, index := range indices {
		if numberIsEmbedded(text, index[0]) {
			continue
		}
		// Ordinals such as "95th" identify a metric, not a measured value.
		if ordinalSuffixPattern.MatchString(text[index[1]:]) {
			continue
		}
		numbers = append(numbers, text[index[0]:index[1]])
	}
	return numbers
}

func numberIsEmbedded(text string, start int) bool {
	if start == 0 {
		return false
	}
	previous := text[start-1]
	return previous == '_' || previous >= 'a' && previous <= 'z' ||
		previous >= 'A' && previous <= 'Z' || previous >= '0' && previous <= '9'
}
