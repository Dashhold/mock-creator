package handler

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"

	"github.com/gin-gonic/gin"

	"mockcreator/internal/models"
	"mockcreator/internal/render"
)

// maxPDFQuestions bounds one PDF render's memory (a 500-question booklet
// peaks at about 133 MB in Typst). Larger papers still export as Word or
// Markdown. RENDER_MAX_QUESTIONS overrides it.
func maxPDFQuestions() int {
	if v, err := strconv.Atoi(os.Getenv("RENDER_MAX_QUESTIONS")); err == nil && v > 0 {
		return v
	}
	return 500
}

// exportBooklet renders the Dashhold-EdTech booklet as PDF or Word. The export
// gate has already run: a failed paper never gets here, and a paper that is not
// cleared only gets here with draft=true, so it is stamped DRAFT.
//
// Query options:
//
//	part=paper|key|both   what the booklet holds (default: both, or paper when answers=false)
//	explanations=bool     worked solutions after the key (default: as today)
//	watermark=bool        faint brand watermark on every page (default true)
func (h *Handler) exportBooklet(c *gin.Context, paper models.TestPaper, format string, withAnswers, withExplanations bool) {
	part := render.PartBoth
	if !withAnswers {
		part = render.PartPaper
	}
	if raw := c.Query("part"); raw != "" {
		p, ok := render.ParsePart(raw)
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "part must be paper, key or both"})
			return
		}
		part = p
	}

	doc := render.Build(paper, render.Options{
		Part:         part,
		Explanations: withExplanations,
		Draft:        !paper.Publishable(),
		DraftReason:  paper.QAExplanation(),
		Watermark:    boolQuery(c, "watermark", true),
	})

	filename := slug(paper.Title)
	if filename == "" {
		filename = fmt.Sprintf("paper-%d", paper.ID)
	}
	filename += "-" + string(part)
	c.Header("Cache-Control", "no-store")

	switch format {
	case "pdf":
		if doc.QuestionCount > maxPDFQuestions() {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{
				"error": fmt.Sprintf("PDF export is limited to %d questions; export this paper as Word instead", maxPDFQuestions()),
			})
			return
		}
		renderer, err := render.DefaultPDF()
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": render.ErrPDFUnavailable.Error(), "detail": err.Error()})
			return
		}
		pdf, err := renderer.Render(c.Request.Context(), doc)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, render.ErrBusy) {
				status = http.StatusServiceUnavailable
			}
			c.JSON(status, gin.H{"error": err.Error()})
			return
		}
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename+".pdf"))
		c.Data(http.StatusOK, "application/pdf", pdf)
	default: // docx, word
		file, err := render.DOCX(doc)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename+".docx"))
		c.Data(http.StatusOK, "application/vnd.openxmlformats-officedocument.wordprocessingml.document", file)
	}
}
