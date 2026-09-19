// Package server wires the HTTP routes.
package server

import (
	"log"
	"net/http"
	"strings"
	"time"

	"mockcreator/internal/config"
	"mockcreator/internal/converter"
	"mockcreator/internal/handler"
	"mockcreator/internal/pipeline"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// New builds the HTTP engine.
func New(db *gorm.DB, cfg *config.Config, jobs *pipeline.Runner, conv *converter.Client) *gin.Engine {
	if !cfg.IsDevelopment() {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())
	// Uploads stream to a temp file beyond this, which keeps a large PDF from
	// pinning its whole size in memory twice.
	r.MaxMultipartMemory = 16 << 20

	r.Use(corsMiddleware(cfg.Security))

	h := handler.New(db, cfg, jobs, conv)

	// HEAD as well as GET: monitoring tools and `wget --spider` probe with HEAD,
	// and a router that only answers GET reports itself as down.
	health := healthHandler(db)
	r.GET("/health", health)
	r.HEAD("/health", health)

	api := r.Group("/api/v1")
	api.Use(authMiddleware(cfg.Security))

	// Meta: what the engine supports and what is happening right now.
	meta := api.Group("/meta")
	{
		meta.GET("/capabilities", h.Capabilities)
		meta.GET("/overview", h.Overview)
		meta.GET("/converter", h.ConverterHealth)
		meta.GET("/model", h.ModelStatus)
	}

	// Review: everything that is not cleared for delivery, and the decisions
	// taken about it. Kept as its own group because a reviewer's workflow is not
	// the same as browsing the warehouse.
	review := api.Group("/review")
	{
		review.GET("", h.ReviewQueue)
		review.GET("/summary", h.ReviewSummary)
		review.POST("/requalify", h.RequalifyQuestions)
	}

	// Exams and everything scoped to one exam.
	exams := api.Group("/exams")
	{
		exams.GET("", h.ListExams)
		exams.POST("", h.CreateExam)
		exams.GET("/:id", h.GetExam)
		exams.PUT("/:id", h.UpdateExam)
		exams.DELETE("/:id", h.DeleteExam)

		exams.GET("/:id/subjects", h.ListExamSubjects)
		exams.POST("/:id/subjects", h.AddExamSubject)
		exams.DELETE("/:id/subjects/:subjectId", h.RemoveExamSubject)

		exams.GET("/:id/patterns", h.ListPatterns)
		exams.POST("/:id/patterns", h.CreatePattern)
		exams.GET("/:id/patterns/:patternId", h.GetPattern)
		exams.PUT("/:id/patterns/:patternId", h.UpdatePattern)
		exams.DELETE("/:id/patterns/:patternId", h.DeletePattern)
		exams.POST("/:id/patterns/:patternId/activate", h.ActivatePattern)
		// Deriving a pattern sits outside /patterns so a static segment never
		// shares a level with the :patternId parameter.
		exams.POST("/:id/pattern-analysis", h.AnalyzePattern)

		exams.GET("/:id/associations", h.ListAssociations)
		exams.POST("/:id/associations", h.CreateAssociation)
		exams.PUT("/:id/associations/:assocId", h.UpdateAssociation)
		exams.DELETE("/:id/associations/:assocId", h.DeleteAssociation)

		exams.POST("/:id/paper-availability", h.PaperAvailability)
		exams.POST("/:id/papers", h.GeneratePaper)
	}

	// Subject catalogue and topics.
	api.GET("/subjects", h.ListSubjects)
	api.POST("/subjects", h.CreateSubject)
	api.GET("/subjects/:id", h.GetSubject)
	api.PUT("/subjects/:id", h.UpdateSubject)
	api.DELETE("/subjects/:id", h.DeleteSubject)

	api.GET("/topics", h.ListTopics)
	api.POST("/topics", h.CreateTopic)
	api.PUT("/topics/:id", h.UpdateTopic)
	api.DELETE("/topics/:id", h.DeleteTopic)

	// Documents: upload, inspect, process.
	docs := api.Group("/documents")
	{
		docs.GET("", h.ListDocuments)
		docs.POST("", h.UploadDocuments)
		docs.GET("/:id", h.GetDocument)
		docs.PATCH("/:id", h.UpdateDocument)
		docs.DELETE("/:id", h.DeleteDocument)
		docs.GET("/:id/download", h.DownloadDocument)
		docs.GET("/:id/conversion", h.GetConversion)
		docs.GET("/:id/blocks", h.ListBlocks)
		docs.POST("/:id/ingest", h.IngestDocument)
		docs.POST("/:id/reparse", h.ReparseDocument)
	}

	// Background work.
	jobsGroup := api.Group("/jobs")
	{
		jobsGroup.GET("", h.ListJobs)
		jobsGroup.GET("/:id", h.GetJob)
		jobsGroup.POST("/:id/cancel", h.CancelJob)
		jobsGroup.POST("/:id/retry", h.RetryJob)
	}

	// The warehouse.
	api.GET("/questions", h.ListQuestions)
	api.POST("/questions", h.CreateQuestion)
	// Collection-level PATCH is the bulk edit, which also avoids a static
	// segment colliding with /questions/:id.
	api.PATCH("/questions", h.BulkUpdateQuestions)
	api.GET("/questions/:id", h.GetQuestion)
	api.PUT("/questions/:id", h.UpdateQuestion)
	api.DELETE("/questions/:id", h.DeleteQuestion)
	api.POST("/questions/:id/review", h.ReviewQuestion)
	// Provenance answers "where did this come from and how was it processed",
	// which is what a reviewer needs before deciding whether the extractor or the
	// source is at fault.
	api.GET("/questions/:id/provenance", h.QuestionProvenance)
	api.POST("/questions/:id/resolve", h.ResolveIssue)
	api.GET("/warehouse", h.WarehouseSummary)

	// Papers.
	papers := api.Group("/papers")
	{
		papers.GET("", h.ListPapers)
		papers.GET("/:id", h.GetPaper)
		papers.PUT("/:id", h.UpdatePaper)
		papers.DELETE("/:id", h.DeletePaper)
		papers.GET("/:id/export", h.ExportPaper)
		papers.GET("/:id/qa", h.PaperQA)
		papers.POST("/:id/qa", h.RunPaperQA)
	}

	r.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{"error": "no such endpoint: " + c.Request.URL.Path})
	})

	if !cfg.Security.AuthEnabled() {
		log.Println("security: API_KEY is not set, so the API accepts any request; " +
			"keep it on a private network or set API_KEY before exposing it")
	}

	return r
}

// healthHandler reports whether the service can reach its database.
func healthHandler(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		state := "ok"
		status := http.StatusOK

		sqlDB, err := db.DB()
		if err == nil {
			err = sqlDB.Ping()
		}
		if err != nil {
			state = "db_unavailable"
			status = http.StatusServiceUnavailable
		}
		c.JSON(status, gin.H{"status": state, "time": time.Now().UTC()})
	}
}

// corsMiddleware allows the bundled frontend to call the API directly during
// development, when Vite serves on a different origin.
func corsMiddleware(cfg config.SecurityConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if cfg.OriginAllowed(origin) {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			c.Header("Access-Control-Allow-Headers", "Content-Type, X-API-Key, Authorization")
			c.Header("Access-Control-Max-Age", "600")
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// authMiddleware enforces the shared secret when one is configured.
func authMiddleware(cfg config.SecurityConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !cfg.AuthEnabled() {
			c.Next()
			return
		}

		supplied := c.GetHeader("X-API-Key")
		if supplied == "" {
			header := c.GetHeader("Authorization")
			if parts := strings.SplitN(header, " ", 2); len(parts) == 2 &&
				strings.EqualFold(parts[0], "bearer") {
				supplied = parts[1]
			}
		}
		if supplied != cfg.APIKey {
			c.AbortWithStatusJSON(http.StatusUnauthorized,
				gin.H{"error": "missing or invalid API key"})
			return
		}
		c.Next()
	}
}
