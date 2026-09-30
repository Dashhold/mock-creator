package server

import (
	"testing"

	"mockcreator/internal/config"
	"mockcreator/internal/converter"
)

// Gin panics at registration time when a static path segment collides with a
// parameter at the same level, so building the router is itself the check.
func TestRouterBuildsWithoutConflicts(t *testing.T) {
	cfg := config.Load()
	conv := converter.New(cfg.Converter)

	engine := New(nil, cfg, nil, conv)
	routes := engine.Routes()
	if len(routes) < 40 {
		t.Fatalf("expected the full route table, got %d routes", len(routes))
	}

	seen := map[string]bool{}
	for _, route := range routes {
		key := route.Method + " " + route.Path
		if seen[key] {
			t.Errorf("duplicate route registered: %s", key)
		}
		seen[key] = true
	}

	for _, required := range []string{
		"POST /api/v1/documents",
		"GET /api/v1/documents/:id/conversion",
		"POST /api/v1/documents/:id/ingest",
		"POST /api/v1/exams/:id/pattern-analysis",
		"POST /api/v1/exams/:id/patterns/:patternId/activate",
		"POST /api/v1/exams/:id/papers",
		"PATCH /api/v1/questions",
		"GET /api/v1/questions/:id",
		"GET /api/v1/warehouse",
		"GET /api/v1/papers/:id/export",
		"GET /api/v1/meta/overview",
	} {
		if !seen[required] {
			t.Errorf("missing route %s", required)
		}
	}
}
