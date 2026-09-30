package pipeline

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"mockcreator/internal/models"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// The system keeps a document's text, not its file. This file holds the two
// halves of that: turning material that already is text into a stored
// conversion without a converter round trip, and dropping a file once its text
// has been saved.

// PlainTextEngine names the "conversion" of material that was text already.
const PlainTextEngine = "plain-text"

// plainTextExtensions are formats whose bytes already are their text. They are
// read here rather than sent to the converter, so nothing is staged and no
// conversion round trip is paid for. The set matches the converter's own
// plain-text path, so the stored result is the same either way.
var plainTextExtensions = map[string]bool{
	"txt": true, "text": true, "csv": true, "rst": true, "json": true,
}

// IsPlainText reports whether files with this extension are read as text
// directly instead of being converted.
func IsPlainText(extension string) bool {
	return plainTextExtensions[strings.ToLower(strings.TrimPrefix(extension, "."))]
}

// errOriginalNotKept means a conversion was needed but the document's file is
// no longer stored, because only its text is kept once it has been read.
var errOriginalNotKept = errors.New("the original file is no longer stored, only its text; " +
	"upload the same file again to convert it afresh")

// DecodeText turns uploaded bytes into text.
//
// UTF-8 is expected, with or without a byte-order mark. UTF-16 is accepted when
// it announces itself with a BOM, because that is what Windows editors write
// when told to save as "Unicode", and papers in Hindi and other non-Latin
// scripts are often saved that way. Invalid bytes are replaced rather than
// rejected, as the converter does. Line endings are normalised to \n.
func DecodeText(data []byte) string {
	var text string
	switch {
	case bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}):
		text = string(data[3:])
	case bytes.HasPrefix(data, []byte{0xFF, 0xFE}):
		text = decodeUTF16(data[2:], false)
	case bytes.HasPrefix(data, []byte{0xFE, 0xFF}):
		text = decodeUTF16(data[2:], true)
	default:
		text = string(data)
	}
	text = strings.ToValidUTF8(text, "\uFFFD")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.ReplaceAll(text, "\r", "\n")
}

func decodeUTF16(data []byte, bigEndian bool) string {
	units := make([]uint16, 0, len(data)/2)
	for i := 0; i+1 < len(data); i += 2 {
		if bigEndian {
			units = append(units, uint16(data[i])<<8|uint16(data[i+1]))
		} else {
			units = append(units, uint16(data[i+1])<<8|uint16(data[i]))
		}
	}
	return string(utf16.Decode(units))
}

// StoreTextConversion records text as a new document's conversion, with one
// block per non-empty line, inside the caller's transaction. It returns how
// many blocks were stored.
//
// The result matches the converter's plain-text path, so a document reads the
// same whether its text was pasted in, uploaded as a .txt file, or converted.
func StoreTextConversion(tx *gorm.DB, docID uint, text, reason string) (int, error) {
	warnings, _ := json.Marshal([]string{})
	record := models.DocumentConversion{
		DocumentID:   docID,
		Engine:       PlainTextEngine,
		EngineReason: reason,
		Format:       "markdown",
		Markdown:     text,
		PageCount:    1,
		CharCount:    utf8.RuneCountInString(text),
		TextRatio:    1,
		Confidence:   1,
		Warnings:     datatypes.JSON(warnings),
		Status:       models.StatusCompleted,
	}
	if err := tx.Create(&record).Error; err != nil {
		return 0, fmt.Errorf("save text: %w", err)
	}

	var blocks []models.ExtractedBlock
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		blocks = append(blocks, models.ExtractedBlock{
			DocumentID: docID,
			OrderIndex: len(blocks),
			PageNo:     1,
			BlockType:  "paragraph",
			Text:       line,
			CharCount:  len(line),
		})
	}
	if len(blocks) == 0 {
		return 0, nil
	}
	if err := tx.CreateInBatches(blocks, 200).Error; err != nil {
		return 0, fmt.Errorf("save text blocks: %w", err)
	}
	return len(blocks), nil
}

// DiscardOriginal deletes a document's stored file and records that it is gone.
// Once the text is saved the file is dead weight: nothing downstream reads it.
func DiscardOriginal(db *gorm.DB, docID uint) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("document_id = ?", docID).Delete(&models.DocumentBlob{}).Error; err != nil {
			return err
		}
		return tx.Model(&models.Document{}).Where("id = ?", docID).
			Update("has_original", false).Error
	})
}

// DiscardConvertedOriginals drops the stored file of every document that
// finished processing with its text saved. Documents that failed or are still
// in flight keep their file so they can be retried. It reports how many files
// were dropped and how many bytes they held.
func DiscardConvertedOriginals(db *gorm.DB) (int, int64, error) {
	var ids []uint
	err := db.Model(&models.Document{}).
		Where("status = ?", models.StatusCompleted).
		Where(`EXISTS (SELECT 1 FROM document_blobs b WHERE b.document_id = documents.id)`).
		Where(`EXISTS (SELECT 1 FROM document_conversions c
		        WHERE c.document_id = documents.id AND c.deleted_at IS NULL
		          AND c.status = ? AND c.markdown <> '')`, models.StatusCompleted).
		Pluck("id", &ids).Error
	if err != nil || len(ids) == 0 {
		return 0, 0, err
	}

	var freed int64
	if err := db.Model(&models.DocumentBlob{}).Where("document_id IN ?", ids).
		Select("COALESCE(SUM(octet_length(data)), 0)").Scan(&freed).Error; err != nil {
		return 0, 0, err
	}
	for i, id := range ids {
		if err := DiscardOriginal(db, id); err != nil {
			return i, 0, fmt.Errorf("document %d: %w", id, err)
		}
	}
	return len(ids), freed, nil
}

// discardOriginal drops a document's file after its text has been saved,
// unless this deployment keeps originals. A failure is logged rather than
// failing the ingest: the text is safe, and a file left behind is only space.
func (r *Runner) discardOriginal(docID uint) {
	if r.cfg.Upload.KeepOriginals {
		return
	}
	if err := DiscardOriginal(r.db, docID); err != nil {
		log.Printf("pipeline: could not drop the stored file of document %d: %v", docID, err)
	}
}

// hasOriginal reports whether a document's file is still stored.
func (r *Runner) hasOriginal(docID uint) bool {
	var count int64
	if err := r.db.Model(&models.DocumentBlob{}).
		Where("document_id = ?", docID).Count(&count).Error; err != nil {
		// Unknown counts as present, so the conversion reports the real error.
		return true
	}
	return count > 0
}
