// Package llm adds model-backed judgement to the quality pipeline.
//
// The model is used only where understanding is genuinely required: deciding
// whether a question is answerable, whether an explanation contradicts its
// answer, whether two rewordings are the same question, whether a passage
// supports the questions that cite it. Everything measurable is left to the
// deterministic rules, which are faster, free and repeatable.
//
// Two rules govern this package and are enforced in code rather than trusted to
// a prompt:
//
//  1. The model never supplies content. Any text it returns as a correction is
//     checked against the source it was given, and a correction that is not
//     found there is discarded and the item is sent to a human instead. See
//     grounding.go.
//  2. The model never silently upgrades anything. A model that does not run, or
//     whose answer fails grounding, leaves the content exactly as unverified as
//     it was; it cannot turn an unchecked question into a passing one.
//
// Any OpenAI-compatible endpoint works, which covers a local Ollama or vLLM
// server as well as the hosted providers. With no model configured the pipeline
// runs unchanged and the model-only checks report themselves as not run.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"mockcreator/internal/config"
)

// Client talks to an OpenAI-compatible chat completions endpoint.
type Client struct {
	cfg  config.LLMConfig
	http *http.Client
	// gate bounds how many requests are in flight. Local servers fall over
	// under unbounded concurrency and hosted ones rate-limit.
	gate chan struct{}
}

// New builds a client. It is safe to call with an unconfigured LLMConfig; the
// result simply reports itself unavailable.
func New(cfg config.LLMConfig) *Client {
	concurrency := cfg.MaxConcurrency
	if concurrency < 1 {
		concurrency = 1
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	return &Client{
		cfg:  cfg,
		http: &http.Client{Timeout: timeout},
		gate: make(chan struct{}, concurrency),
	}
}

// Available reports whether a model is configured.
func (c *Client) Available() bool { return c != nil && c.cfg.Enabled() }

// Model returns the configured model name, for recording on reviews.
func (c *Client) Model() string {
	if c == nil {
		return ""
	}
	return c.cfg.Model
}

// Usage is the token cost of one call, so model spend is visible rather than
// invisible.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	// Temperature is pinned at zero everywhere. Quality review has to give the
	// same verdict on the same input, otherwise re-running it changes which
	// questions ship.
	Temperature    float64         `json:"temperature"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
	Stream         bool            `json:"stream"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type chatResponse struct {
	Choices []struct {
		Message      chatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// ErrUnavailable is returned when no model is configured.
var ErrUnavailable = errors.New("no language model is configured")

// completeJSON sends a prompt and decodes the reply into out.
//
// The reply is required to be JSON. Models that honour response_format get it
// natively; the rest are handled by extracting the first JSON object from the
// text, because a model that wraps its answer in prose is common and is not a
// reason to fail the check.
func (c *Client) completeJSON(ctx context.Context, system, user string, maxTokens int, out any) (Usage, error) {
	if !c.Available() {
		return Usage{}, ErrUnavailable
	}

	// Truncating here rather than letting the server reject the call keeps a
	// long passage from failing an audit outright.
	if limit := c.cfg.MaxInputChars; limit > 0 && len(user) > limit {
		user = user[:limit] + "\n\n[input truncated at the configured limit]"
	}

	payload := chatRequest{
		Model: c.cfg.Model,
		Messages: []chatMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		Temperature:    0,
		MaxTokens:      maxTokens,
		ResponseFormat: &responseFormat{Type: "json_object"},
	}

	select {
	case c.gate <- struct{}{}:
		defer func() { <-c.gate }()
	case <-ctx.Done():
		return Usage{}, ctx.Err()
	}

	attempts := c.cfg.MaxRetries + 1
	if attempts < 1 {
		attempts = 1
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		usage, err := c.callOnce(ctx, payload, out)
		if err == nil {
			return usage, nil
		}
		lastErr = err
		if ctx.Err() != nil || !retryable(err) {
			break
		}
		// A short linear backoff. Long waits are worse than failing the check,
		// because the pipeline can always fall back to review.
		select {
		case <-ctx.Done():
			return Usage{}, ctx.Err()
		case <-time.After(time.Duration(attempt) * 2 * time.Second):
		}
	}
	return Usage{}, lastErr
}

func (c *Client) callOnce(ctx context.Context, payload chatRequest, out any) (Usage, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return Usage{}, fmt.Errorf("encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Usage{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return Usage{}, fmt.Errorf("model unreachable at %s: %w", c.cfg.BaseURL, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return Usage{}, fmt.Errorf("read model response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return Usage{}, &httpError{
			status: resp.StatusCode,
			body:   strings.TrimSpace(string(raw)),
		}
	}

	var decoded chatResponse
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return Usage{}, fmt.Errorf("decode model response: %w", err)
	}
	if decoded.Error != nil {
		return Usage{}, fmt.Errorf("model returned an error: %s", decoded.Error.Message)
	}
	if len(decoded.Choices) == 0 {
		return Usage{}, errors.New("model returned no choices")
	}

	content := decoded.Choices[0].Message.Content
	if decoded.Choices[0].FinishReason == "length" {
		return decoded.Usage, errors.New("model reply was cut off before it finished; raise LLM_MAX_INPUT_CHARS or use a larger context")
	}

	object, err := extractJSONObject(content)
	if err != nil {
		return decoded.Usage, err
	}
	if err := json.Unmarshal([]byte(object), out); err != nil {
		return decoded.Usage, fmt.Errorf("model reply was not the expected shape: %w", err)
	}
	return decoded.Usage, nil
}

// httpError carries the status so retry logic can tell a rate limit from a bad
// request.
type httpError struct {
	status int
	body   string
}

func (e *httpError) Error() string {
	return fmt.Sprintf("model endpoint returned %d: %s", e.status, trimTo(e.body, 300))
}

func retryable(err error) bool {
	var he *httpError
	if errors.As(err, &he) {
		// Rate limits and server faults are worth another go; a rejected request
		// will be rejected again.
		return he.status == 408 || he.status == 429 || he.status >= 500
	}
	// Network-level failures are retried; decoding failures are not, because the
	// same prompt will produce the same malformed shape.
	return strings.Contains(err.Error(), "unreachable") ||
		strings.Contains(err.Error(), "timeout") ||
		strings.Contains(err.Error(), "connection reset") ||
		strings.Contains(err.Error(), "EOF")
}

// extractJSONObject pulls the first complete JSON object out of a model reply.
//
// Models that support response_format return bare JSON and this is a no-op.
// Others wrap it in prose or a fenced code block, which is not worth failing a
// quality check over.
func extractJSONObject(content string) (string, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return "", errors.New("model returned an empty reply")
	}
	if strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") {
		return trimmed, nil
	}

	// Strip a fenced block if present.
	if idx := strings.Index(trimmed, "```"); idx >= 0 {
		rest := trimmed[idx+3:]
		if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
			rest = rest[nl+1:]
		}
		if end := strings.Index(rest, "```"); end >= 0 {
			rest = rest[:end]
		}
		trimmed = strings.TrimSpace(rest)
	}

	start := strings.IndexByte(trimmed, '{')
	if start < 0 {
		return "", fmt.Errorf("model reply contained no JSON object: %s", trimTo(trimmed, 200))
	}

	// Walk the string tracking depth so a nested object is not cut short. String
	// literals are skipped so a brace inside quoted text cannot unbalance it.
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(trimmed); i++ {
		ch := trimmed[i]
		switch {
		case escaped:
			escaped = false
		case ch == '\\' && inString:
			escaped = true
		case ch == '"':
			inString = !inString
		case inString:
			// nothing
		case ch == '{':
			depth++
		case ch == '}':
			depth--
			if depth == 0 {
				return trimmed[start : i+1], nil
			}
		}
	}
	return "", fmt.Errorf("model reply held an unterminated JSON object: %s", trimTo(trimmed, 200))
}

func trimTo(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "\u2026"
}

// Ping checks that the configured endpoint answers and can return JSON. It is
// used at boot so a misconfigured model surfaces there rather than as a
// mysteriously skipped audit later.
func (c *Client) Ping(ctx context.Context) error {
	if !c.Available() {
		return ErrUnavailable
	}
	var out struct {
		OK bool `json:"ok"`
	}
	_, err := c.completeJSON(ctx,
		`You are a JSON API. Reply with exactly {"ok": true} and nothing else.`,
		`Reply with {"ok": true}.`, 32, &out)
	return err
}
