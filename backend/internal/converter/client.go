// Package converter talks to the Python document conversion service.
//
// Documents are pushed as multipart uploads read straight from the database, so
// the two services share no filesystem. Conversion is submitted as a task and
// polled, because a scanned paper can take minutes and holding a request open
// that long is how the previous design used to time out.
package converter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"mockcreator/internal/config"
)

// Client is an HTTP client for the conversion service.
type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client

	PollInterval   time.Duration
	ConvertTimeout time.Duration
}

// New builds a client from configuration.
func New(cfg config.ConverterConfig) *Client {
	poll := cfg.PollInterval
	if poll <= 0 {
		poll = 2 * time.Second
	}
	return &Client{
		BaseURL: strings.TrimRight(cfg.ServiceURL, "/"),
		APIKey:  cfg.APIKey,
		HTTP: &http.Client{
			// Bounds a single call. Long conversions are polled, not awaited, so
			// this never has to cover the whole job.
			Timeout: cfg.RequestTimeout,
		},
		PollInterval:   poll,
		ConvertTimeout: cfg.ConvertTimeout,
	}
}

// Options mirror the form fields the service accepts.
type Options struct {
	// Engine is auto, geometry or docling. "auto" reads a PDF's own text layer
	// when it has one and falls back to the layout model otherwise, which is the
	// right choice for a mixed library of documents. Naming an engine explicitly
	// is how a user re-runs a document that came out wrong.
	Engine string
	// OCRMode is off, auto or force. "auto" lets the service measure the text
	// layer and decide.
	OCRMode   string
	OCREngine string
	TableMode string
	Languages []string
	MaxPages  int
	// IncludeBlocks asks for the structured block list as well as markdown.
	IncludeBlocks bool
}

// WithDefaults fills empty fields from configuration.
func (o Options) WithDefaults(cfg config.ConverterConfig) Options {
	if o.Engine == "" {
		o.Engine = cfg.DefaultEngine
	}
	if o.OCRMode == "" {
		o.OCRMode = cfg.DefaultOCRMode
	}
	if o.OCREngine == "" {
		o.OCREngine = cfg.DefaultOCREngine
	}
	if o.TableMode == "" {
		o.TableMode = cfg.TableMode
	}
	return o
}

// Block is one structural element of a converted document.
type Block struct {
	OrderIndex int    `json:"order_index"`
	PageNo     int    `json:"page_no"`
	BlockType  string `json:"block_type"`
	Text       string `json:"text"`
	Level      int    `json:"level"`
}

// Metadata describes how a conversion was produced.
//
// The diagnostic half of this matters as much as the text: ExtractionConfidence,
// Warnings and UnreadableChars are what let the pipeline refuse to build a paper
// out of text it cannot vouch for, instead of accepting whatever came back.
type Metadata struct {
	Filename      string   `json:"filename"`
	Extension     string   `json:"extension"`
	MimeType      string   `json:"mime_type"`
	SizeBytes     int64    `json:"size_bytes"`
	Format        string   `json:"format"`
	Engine        string   `json:"engine"`
	EngineReason  string   `json:"engine_reason"`
	EngineVersion string   `json:"engine_version"`
	OCREngine     string   `json:"ocr_engine"`
	OCRMode       string   `json:"ocr_mode"`
	OCRApplied    bool     `json:"ocr_applied"`
	OCRFullPage   bool     `json:"ocr_full_page"`
	TableMode     string   `json:"table_mode"`
	Languages     []string `json:"languages"`
	PageCount     int      `json:"page_count"`
	CharCount     int      `json:"char_count"`
	TableCount    int      `json:"table_count"`
	PictureCount  int      `json:"picture_count"`
	TextRatio     float64  `json:"text_ratio"`
	DurationMS    int64    `json:"duration_ms"`

	// ExtractionConfidence is 0..1. The geometric engine scores its own layout;
	// the layout model is capped below 1 because it infers reading order.
	ExtractionConfidence float64 `json:"extraction_confidence"`
	// UnreadableChars counts characters the source fonts never mapped to
	// Unicode. Any question containing one is held back for review.
	UnreadableChars int `json:"unreadable_chars"`
	// Warnings are human-readable reasons to distrust part of this conversion.
	Warnings []string `json:"warnings"`

	GeometryConfidence float64        `json:"geometry_confidence"`
	GeometryColumns    []int          `json:"geometry_columns_per_page"`
	GeometryRepairs    map[string]int `json:"geometry_repairs"`
	DroppedFurniture   []string       `json:"geometry_dropped_furniture"`
}

// StackedMathRows reports how many two-dimensional expressions failed to
// linearise, which is the signal that some questions need a human to read the
// original page.
func (m Metadata) StackedMathRows() int {
	if m.GeometryRepairs == nil {
		return 0
	}
	return m.GeometryRepairs["stacked_math_rows"]
}

// Result is a converted document.
type Result struct {
	Markdown string   `json:"markdown"`
	Blocks   []Block  `json:"blocks"`
	Metadata Metadata `json:"metadata"`
}

// Task is the state of an asynchronous conversion.
type Task struct {
	TaskID   string  `json:"task_id"`
	Filename string  `json:"filename"`
	Status   string  `json:"status"`
	Stage    string  `json:"stage"`
	Progress int     `json:"progress"`
	Error    string  `json:"error,omitempty"`
	Result   *Result `json:"result,omitempty"`
}

// Done reports whether the task has reached a terminal state.
func (t Task) Done() bool {
	switch t.Status {
	case "completed", "failed", "cancelled":
		return true
	}
	return false
}

// Health is the service's liveness report.
type Health struct {
	Status        string         `json:"status"`
	Engines       []string       `json:"engines"`
	EngineVersion string         `json:"engine_version"`
	ModelsReady   bool           `json:"models_ready"`
	OCREngines    []string       `json:"ocr_engines"`
	Queue         map[string]int `json:"queue"`
}

// Capabilities is what the service can accept, surfaced in the UI so the upload
// form offers exactly the formats, engines and OCR engines that are really
// installed rather than a hardcoded list that can drift out of date.
type Capabilities struct {
	EngineVersion  string         `json:"engine_version"`
	Extensions     []string       `json:"extensions"`
	Engines        []string       `json:"engines"`
	OCREngines     []string       `json:"ocr_engines"`
	OCRModes       []string       `json:"ocr_modes"`
	TableModes     []string       `json:"table_modes"`
	Defaults       map[string]any `json:"defaults"`
	MaxUploadBytes int64          `json:"max_upload_bytes"`
	Concurrency    int            `json:"concurrency"`
}

func (c *Client) authorize(req *http.Request) {
	if c.APIKey != "" {
		req.Header.Set("X-API-Key", c.APIKey)
	}
}

func (c *Client) do(req *http.Request, out any) error {
	c.authorize(req)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("converter unreachable at %s: %w", c.BaseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return fmt.Errorf("converter returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode converter response: %w", err)
	}
	return nil
}

// Health checks the service.
func (c *Client) Health(ctx context.Context) (*Health, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/health", nil)
	if err != nil {
		return nil, err
	}
	var out Health
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Capabilities reports the supported formats and engines.
func (c *Client) Capabilities(ctx context.Context) (*Capabilities, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/v1/capabilities", nil)
	if err != nil {
		return nil, err
	}
	var out Capabilities
	if err := c.do(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// buildUpload assembles the multipart body for a conversion request.
func buildUpload(filename string, data []byte, opts Options) (io.Reader, string, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return nil, "", fmt.Errorf("create form file: %w", err)
	}
	if _, err := part.Write(data); err != nil {
		return nil, "", fmt.Errorf("write file part: %w", err)
	}

	fields := map[string]string{
		"engine":         opts.Engine,
		"ocr_mode":       opts.OCRMode,
		"ocr_engine":     opts.OCREngine,
		"table_mode":     opts.TableMode,
		"languages":      strings.Join(opts.Languages, ","),
		"max_pages":      strconv.Itoa(opts.MaxPages),
		"include_blocks": strconv.FormatBool(opts.IncludeBlocks),
	}
	for key, value := range fields {
		if value == "" {
			continue
		}
		if err := writer.WriteField(key, value); err != nil {
			return nil, "", fmt.Errorf("write field %s: %w", key, err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("close multipart writer: %w", err)
	}
	return &buf, writer.FormDataContentType(), nil
}

// Submit queues a conversion and returns the task to poll.
func (c *Client) Submit(ctx context.Context, filename string, data []byte, opts Options) (*Task, error) {
	body, contentType, err := buildUpload(filename, data, opts)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/convert/async", body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)

	var task Task
	if err := c.do(req, &task); err != nil {
		return nil, err
	}
	if task.TaskID == "" {
		return nil, fmt.Errorf("converter accepted the upload but returned no task id")
	}
	return &task, nil
}

// TaskStatus polls one task.
func (c *Client) TaskStatus(ctx context.Context, taskID string, includeResult bool) (*Task, error) {
	url := fmt.Sprintf("%s/v1/tasks/%s?include_result=%t", c.BaseURL, taskID, includeResult)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	var task Task
	if err := c.do(req, &task); err != nil {
		return nil, err
	}
	return &task, nil
}

// Cancel abandons a task.
func (c *Client) Cancel(ctx context.Context, taskID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.BaseURL+"/v1/tasks/"+taskID, nil)
	if err != nil {
		return err
	}
	return c.do(req, nil)
}

// ProgressFunc receives stage updates while a conversion runs.
type ProgressFunc func(stage string, percent int)

// Convert submits a document and waits for the result, reporting progress.
//
// If ctx is cancelled the remote task is cancelled too, so abandoning a job
// does not leave the converter grinding on work nobody wants.
func (c *Client) Convert(
	ctx context.Context,
	filename string,
	data []byte,
	opts Options,
	onProgress ProgressFunc,
) (*Result, error) {
	task, err := c.Submit(ctx, filename, data, opts)
	if err != nil {
		return nil, err
	}

	deadline := time.Now().Add(c.ConvertTimeout)
	ticker := time.NewTicker(c.PollInterval)
	defer ticker.Stop()

	lastStage := ""
	for {
		select {
		case <-ctx.Done():
			// Best effort: the context is already dead, so use a fresh one.
			cancelCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = c.Cancel(cancelCtx, task.TaskID)
			cancel()
			return nil, ctx.Err()
		case <-ticker.C:
		}

		status, err := c.TaskStatus(ctx, task.TaskID, false)
		if err != nil {
			return nil, err
		}
		if onProgress != nil && status.Stage != lastStage {
			lastStage = status.Stage
			onProgress(status.Stage, status.Progress)
		}

		switch status.Status {
		case "completed":
			full, err := c.TaskStatus(ctx, task.TaskID, true)
			if err != nil {
				return nil, err
			}
			if full.Result == nil {
				return nil, fmt.Errorf("converter reported success but returned no document")
			}
			// The task has been consumed; release it so memory is reclaimed.
			go func(id string) {
				cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_ = c.Cancel(cleanup, id)
			}(task.TaskID)
			return full.Result, nil
		case "failed":
			return nil, fmt.Errorf("conversion failed: %s", status.Error)
		case "cancelled":
			return nil, fmt.Errorf("conversion was cancelled")
		}

		if time.Now().After(deadline) {
			cancelCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = c.Cancel(cancelCtx, task.TaskID)
			cancel()
			return nil, fmt.Errorf("conversion timed out after %s", c.ConvertTimeout)
		}
	}
}
