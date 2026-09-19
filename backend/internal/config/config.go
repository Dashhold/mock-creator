package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config holds all runtime configuration loaded from the environment.
type Config struct {
	ServerPort string
	Env        string

	DB        DBConfig
	Converter ConverterConfig
	Worker    WorkerConfig
	Upload    UploadConfig
	Review    ReviewConfig
	Security  SecurityConfig
	LLM       LLMConfig
}

// SecurityConfig holds the API's access controls.
//
// The API is unauthenticated by default because it is designed to run on a
// private network behind the bundled frontend. Setting API_KEY turns on a shared
// secret, which is the minimum required before exposing it any further.
type SecurityConfig struct {
	APIKey         string
	AllowedOrigins []string
}

// AuthEnabled reports whether requests must present the shared secret.
func (s SecurityConfig) AuthEnabled() bool { return s.APIKey != "" }

// OriginAllowed reports whether a browser origin may call the API.
func (s SecurityConfig) OriginAllowed(origin string) bool {
	if origin == "" {
		return false
	}
	for _, allowed := range s.AllowedOrigins {
		if allowed == "*" || strings.EqualFold(allowed, origin) {
			return true
		}
	}
	return false
}

// DBConfig holds PostgreSQL connection settings.
type DBConfig struct {
	Host     string
	Port     string
	User     string
	Password string
	Name     string
	SSLMode  string
	TimeZone string

	MaxOpenConns int
	MaxIdleConns int
}

// ConverterConfig points at the Python document conversion service.
type ConverterConfig struct {
	ServiceURL string
	APIKey     string
	// RequestTimeout bounds a single HTTP call to the service.
	RequestTimeout time.Duration
	// ConvertTimeout bounds how long we wait for one document to finish
	// converting, including OCR.
	ConvertTimeout time.Duration
	PollInterval   time.Duration

	// DefaultEngine is auto, geometry or docling. "auto" reads a PDF's own text
	// layer when one exists, because the document already states where every
	// character sits, and only falls back to the layout model when it does not.
	DefaultEngine string
	// DefaultOCRMode is auto, force or off. "auto" lets the service decide by
	// measuring how much extractable text a page already has.
	DefaultOCRMode   string
	DefaultOCREngine string
	// TableMode is "fast" or "accurate".
	TableMode string

	// MinExtractionConfidence is the floor below which a conversion is not
	// trusted to produce publishable questions. Documents under it still ingest,
	// but everything they yield lands in review.
	MinExtractionConfidence float64
}

// WorkerConfig governs the background job pool.
type WorkerConfig struct {
	// Concurrency is how many jobs run at once. Conversion is memory hungry, so
	// this stays low by default.
	Concurrency  int
	PollInterval time.Duration
	// MaxAttempts is how many times a failed job is retried before giving up.
	MaxAttempts int
	// StaleAfter is how long a job may sit in "processing" before it is
	// considered orphaned (for example the API restarted mid-run) and requeued.
	StaleAfter time.Duration
}

// UploadConfig bounds what may be uploaded.
type UploadConfig struct {
	MaxBytes          int64
	AllowedExtensions []string
}

// ReviewConfig holds the question acceptance bar.
type ReviewConfig struct {
	ScoreThreshold int
	// AutoApproveExtracted approves questions read out of a real question paper
	// without asking a human, on the grounds that they were already vetted by
	// whoever set the original paper.
	//
	// It applies only to questions that pass every deterministic validation
	// check. Anything with an unresolved issue goes to review regardless, which
	// is the difference between trusting the source and trusting the extraction.
	AutoApproveExtracted bool
}

// LLMConfig configures the optional model-backed quality control.
//
// Any OpenAI-compatible endpoint works, which covers a local Ollama or vLLM
// server as well as the hosted providers. Everything is opt-in: with no model
// configured the deterministic checks still run and the model-only checks are
// reported as "not run" rather than quietly counted as passes.
type LLMConfig struct {
	// BaseURL is the root of an OpenAI-compatible API, e.g.
	// http://ollama:11434/v1 or https://api.openai.com/v1.
	BaseURL string
	// APIKey is optional: local servers usually need none.
	APIKey string
	Model  string

	Timeout        time.Duration
	MaxConcurrency int
	MaxRetries     int
	// MaxInputChars caps how much source text goes into one call, so a long
	// passage cannot silently overflow the context window.
	MaxInputChars int

	// Which reviews the model is allowed to perform.
	AuditQuestions   bool
	AuditPapers      bool
	RepairBoundaries bool
	JudgeDuplicates  bool
}

// Enabled reports whether a model is configured. An API key is deliberately not
// required, because a local server does not use one.
func (l LLMConfig) Enabled() bool {
	return l.BaseURL != "" && l.Model != ""
}

// DSN builds the PostgreSQL data source name used by GORM.
func (d DBConfig) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s TimeZone=%s",
		d.Host, d.Port, d.User, d.Password, d.Name, d.SSLMode, d.TimeZone,
	)
}

// AllowsExtension reports whether ext (with or without a leading dot) may be
// uploaded. An empty allow-list permits everything the converter supports.
func (u UploadConfig) AllowsExtension(ext string) bool {
	if len(u.AllowedExtensions) == 0 {
		return true
	}
	ext = strings.ToLower(strings.TrimPrefix(ext, "."))
	for _, allowed := range u.AllowedExtensions {
		if ext == strings.ToLower(strings.TrimPrefix(allowed, ".")) {
			return true
		}
	}
	return false
}

// Load reads configuration from a .env file (if present) and the environment.
func Load() *Config {
	// .env is optional; ignore the error when it is missing.
	_ = godotenv.Load()

	return &Config{
		ServerPort: getEnv("SERVER_PORT", "8080"),
		Env:        getEnv("SERVER_ENV", "development"),
		DB: DBConfig{
			Host:         getEnv("DB_HOST", "localhost"),
			Port:         getEnv("DB_PORT", "5432"),
			User:         getEnv("DB_USER", "postgres"),
			Password:     getEnv("DB_PASSWORD", "postgres"),
			Name:         getEnv("DB_NAME", "mockcreator"),
			SSLMode:      getEnv("DB_SSLMODE", "disable"),
			TimeZone:     getEnv("DB_TIMEZONE", "UTC"),
			MaxOpenConns: getInt("DB_MAX_OPEN_CONNS", 25),
			MaxIdleConns: getInt("DB_MAX_IDLE_CONNS", 5),
		},
		Converter: ConverterConfig{
			ServiceURL:              getEnv("CONVERTER_URL", "http://localhost:5001"),
			APIKey:                  getEnv("CONVERTER_API_KEY", ""),
			RequestTimeout:          getDuration("CONVERTER_REQUEST_TIMEOUT", 2*time.Minute),
			ConvertTimeout:          getDuration("CONVERTER_CONVERT_TIMEOUT", 30*time.Minute),
			PollInterval:            getDuration("CONVERTER_POLL_INTERVAL", 2*time.Second),
			DefaultEngine:           getEnv("CONVERTER_ENGINE", "auto"),
			DefaultOCRMode:          getEnv("CONVERTER_OCR_MODE", "auto"),
			DefaultOCREngine:        getEnv("CONVERTER_OCR_ENGINE", "rapidocr"),
			TableMode:               getEnv("CONVERTER_TABLE_MODE", "accurate"),
			MinExtractionConfidence: getFloat("CONVERTER_MIN_CONFIDENCE", 0.75),
		},
		Worker: WorkerConfig{
			Concurrency:  getInt("WORKER_CONCURRENCY", 2),
			PollInterval: getDuration("WORKER_POLL_INTERVAL", 2*time.Second),
			MaxAttempts:  getInt("WORKER_MAX_ATTEMPTS", 2),
			StaleAfter:   getDuration("WORKER_STALE_AFTER", 45*time.Minute),
		},
		Upload: UploadConfig{
			MaxBytes: getInt64("UPLOAD_MAX_BYTES", 128<<20), // 128 MiB
			AllowedExtensions: getList("UPLOAD_ALLOWED_EXTENSIONS",
				"pdf,docx,doc,pptx,xlsx,xls,html,htm,md,txt,csv,rtf,odt,epub,png,jpg,jpeg,tiff,tif,bmp,webp"),
		},
		Review: ReviewConfig{
			ScoreThreshold:       getInt("REVIEW_SCORE_THRESHOLD", 7),
			AutoApproveExtracted: getBool("REVIEW_AUTO_APPROVE_EXTRACTED", true),
		},
		Security: SecurityConfig{
			APIKey: getEnv("API_KEY", ""),
			AllowedOrigins: getList("CORS_ALLOWED_ORIGINS",
				"http://localhost:3000,http://127.0.0.1:3000"),
		},
		LLM: LLMConfig{
			BaseURL:        strings.TrimRight(getEnv("LLM_BASE_URL", ""), "/"),
			APIKey:         getEnv("LLM_API_KEY", ""),
			Model:          getEnv("LLM_MODEL", ""),
			Timeout:        getDuration("LLM_TIMEOUT", 120*time.Second),
			MaxConcurrency: getInt("LLM_MAX_CONCURRENCY", 4),
			MaxRetries:     getInt("LLM_MAX_RETRIES", 2),
			MaxInputChars:  getInt("LLM_MAX_INPUT_CHARS", 24000),

			AuditQuestions:   getBool("LLM_AUDIT_QUESTIONS", true),
			AuditPapers:      getBool("LLM_AUDIT_PAPERS", true),
			RepairBoundaries: getBool("LLM_REPAIR_BOUNDARIES", true),
			JudgeDuplicates:  getBool("LLM_JUDGE_DUPLICATES", true),
		},
	}
}

// IsDevelopment reports whether the server runs in development mode.
func (c *Config) IsDevelopment() bool {
	return c.Env == "development"
}

func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return fallback
}

func getInt(key string, fallback int) int {
	if v, err := strconv.Atoi(getEnv(key, "")); err == nil && v > 0 {
		return v
	}
	return fallback
}

func getInt64(key string, fallback int64) int64 {
	if v, err := strconv.ParseInt(getEnv(key, ""), 10, 64); err == nil && v > 0 {
		return v
	}
	return fallback
}

// getFloat reads a 0..1 style threshold. Zero is accepted, so a threshold can be
// switched off explicitly rather than only by deleting the variable.
func getFloat(key string, fallback float64) float64 {
	if v, err := strconv.ParseFloat(getEnv(key, ""), 64); err == nil && v >= 0 {
		return v
	}
	return fallback
}

// getBool accepts the usual spellings rather than only the exact string "true".
func getBool(key string, fallback bool) bool {
	raw := strings.ToLower(getEnv(key, ""))
	switch raw {
	case "1", "t", "true", "y", "yes", "on":
		return true
	case "0", "f", "false", "n", "no", "off":
		return false
	}
	return fallback
}

func getDuration(key string, fallback time.Duration) time.Duration {
	raw := getEnv(key, "")
	if raw == "" {
		return fallback
	}
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return d
	}
	// Bare numbers are read as seconds for convenience.
	if n, err := strconv.Atoi(raw); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return fallback
}

func getList(key, fallback string) []string {
	raw := getEnv(key, fallback)
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
