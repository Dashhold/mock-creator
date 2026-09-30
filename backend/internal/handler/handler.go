// Package handler implements the HTTP API.
//
// Responses are uniform: {"data": ...} on success, {"error": "..."} on failure,
// and list endpoints add {"meta": {...}} with pagination. Long operations never
// run inline; they create a job and return it, so the UI can track progress.
package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"mockcreator/internal/config"
	"mockcreator/internal/converter"
	"mockcreator/internal/pipeline"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// contextWithTimeout derives a bounded context from the request, so an upstream
// call cannot outlive the client that asked for it.
func contextWithTimeout(c *gin.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Request.Context(), d)
}

// Handler carries shared dependencies for all HTTP handlers.
type Handler struct {
	DB        *gorm.DB
	Cfg       *config.Config
	Jobs      *pipeline.Runner
	Converter *converter.Client
}

// New builds a handler set.
func New(db *gorm.DB, cfg *config.Config, jobs *pipeline.Runner, conv *converter.Client) *Handler {
	return &Handler{DB: db, Cfg: cfg, Jobs: jobs, Converter: conv}
}

// --- responses -------------------------------------------------------------

func ok(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{"data": data})
}

func created(c *gin.Context, data any) {
	c.JSON(http.StatusCreated, gin.H{"data": data})
}

func accepted(c *gin.Context, data any) {
	c.JSON(http.StatusAccepted, gin.H{"data": data})
}

func list(c *gin.Context, data any, meta any) {
	payload := gin.H{"data": data}
	if meta != nil {
		payload["meta"] = meta
	}
	c.JSON(http.StatusOK, payload)
}

func badRequest(c *gin.Context, err error) {
	c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
}

func badRequestf(c *gin.Context, format string, args ...any) {
	c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf(format, args...)})
}

func notFound(c *gin.Context, what string) {
	if what == "" {
		what = "not found"
	}
	c.JSON(http.StatusNotFound, gin.H{"error": what})
}

func conflict(c *gin.Context, message string) {
	c.JSON(http.StatusConflict, gin.H{"error": message})
}

func serverError(c *gin.Context, err error) {
	c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
}

// dbError maps a GORM error onto the right status code.
func dbError(c *gin.Context, err error, what string) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		notFound(c, what+" not found")
		return
	}
	if isUniqueViolation(err) {
		conflict(c, what+" already exists")
		return
	}
	serverError(c, err)
}

// isUniqueViolation detects a duplicate-key error without importing the driver.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "duplicate key") ||
		strings.Contains(message, "unique constraint") ||
		strings.Contains(message, "sqlstate 23505")
}

// --- request parsing -------------------------------------------------------

// idParam parses the ":id" path parameter.
func idParam(c *gin.Context) (uint, bool) {
	return namedIDParam(c, "id")
}

func namedIDParam(c *gin.Context, name string) (uint, bool) {
	raw := c.Param(name)
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 {
		badRequestf(c, "invalid %s %q", name, raw)
		return 0, false
	}
	return uint(value), true
}

// Page describes a slice of a collection.
type Page struct {
	Page       int   `json:"page"`
	PageSize   int   `json:"page_size"`
	Total      int64 `json:"total"`
	TotalPages int   `json:"total_pages"`
}

const (
	defaultPageSize = 50
	maxPageSize     = 200
)

// pageParams reads pagination from the query string.
//
// Every list endpoint paginates. The previous API returned entire tables, which
// is fine with fifty questions and ruinous with fifty thousand.
func pageParams(c *gin.Context) (page int, size int, offset int) {
	page, _ = strconv.Atoi(c.Query("page"))
	if page < 1 {
		page = 1
	}
	size, _ = strconv.Atoi(c.Query("page_size"))
	switch {
	case size <= 0:
		size = defaultPageSize
	case size > maxPageSize:
		size = maxPageSize
	}
	return page, size, (page - 1) * size
}

func makePage(page, size int, total int64) Page {
	totalPages := 0
	if size > 0 {
		totalPages = int((total + int64(size) - 1) / int64(size))
	}
	return Page{Page: page, PageSize: size, Total: total, TotalPages: totalPages}
}

// uintQuery reads an optional unsigned integer query parameter.
func uintQuery(c *gin.Context, key string) *uint {
	raw := strings.TrimSpace(c.Query(key))
	if raw == "" {
		return nil
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 {
		return nil
	}
	out := uint(value)
	return &out
}

// intQuery reads an optional integer query parameter.
func intQuery(c *gin.Context, key string) *int {
	raw := strings.TrimSpace(c.Query(key))
	if raw == "" {
		return nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return nil
	}
	return &value
}

// boolQuery reads an optional boolean query parameter.
func boolQuery(c *gin.Context, key string, fallback bool) bool {
	raw := strings.ToLower(strings.TrimSpace(c.Query(key)))
	switch raw {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return fallback
}

// filterQuery applies simple equality filters from the query string.
func filterQuery(c *gin.Context, query *gorm.DB, mapping map[string]string) *gorm.DB {
	for param, column := range mapping {
		value := strings.TrimSpace(c.Query(param))
		if value == "" || value == "all" {
			continue
		}
		query = query.Where(column+" = ?", value)
	}
	return query
}

// slug normalises a user-supplied code so it is safe and predictable.
func slug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_' || r == ' ' || r == '.' || r == '/':
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
