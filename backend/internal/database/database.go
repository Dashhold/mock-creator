package database

import (
	"fmt"
	"log"
	"time"

	"mockcreator/internal/config"
	"mockcreator/internal/models"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Connect opens a pooled GORM connection to PostgreSQL.
func Connect(cfg *config.Config) (*gorm.DB, error) {
	logLevel := logger.Warn
	if cfg.IsDevelopment() {
		logLevel = logger.Info
	}

	db, err := gorm.Open(postgres.Open(cfg.DB.DSN()), &gorm.Config{
		Logger: logger.Default.LogMode(logLevel),
		// The engine writes questions in batches during extraction; skipping the
		// default transaction for single statements keeps that cheap.
		SkipDefaultTransaction: true,
	})
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get sql.DB: %w", err)
	}
	sqlDB.SetMaxOpenConns(cfg.DB.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.DB.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(time.Hour)

	return db, nil
}

// WaitForDB blocks until the database answers a ping or the attempts run out.
// Compose health checks cover the normal case; this covers the gap between the
// container reporting healthy and Postgres accepting connections.
func WaitForDB(db *gorm.DB, attempts int, wait time.Duration) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	for i := 1; i <= attempts; i++ {
		if err = sqlDB.Ping(); err == nil {
			return nil
		}
		log.Printf("database not ready (attempt %d/%d): %v", i, attempts, err)
		time.Sleep(wait)
	}
	return fmt.Errorf("database unreachable after %d attempts: %w", attempts, err)
}

// AllModels returns every model in dependency-friendly order for migration.
func AllModels() []any {
	return []any{
		// Taxonomy
		&models.Exam{},
		&models.Subject{},
		&models.Topic{},
		&models.ExamSubject{},
		// Pattern and cross-exam wiring
		&models.ExamPattern{},
		&models.PatternSection{},
		&models.ExamAssociation{},
		// Documents
		&models.Document{},
		&models.DocumentBlob{},
		&models.DocumentConversion{},
		&models.ExtractedBlock{},
		// Content
		&models.Passage{},
		&models.Question{},
		&models.QuestionOption{},
		&models.QuestionExamLink{},
		&models.QualityReview{},
		// Work and output
		&models.Job{},
		&models.TestPaper{},
		&models.TestPaperItem{},
	}
}

// legacyTables are tables from the pre-restructure schema. They are only
// dropped when explicitly asked for, never as a side effect of booting.
var legacyTables = []string{"source_materials", "extracted_chunks", "generation_jobs", "exam_subjects_old"}

// Migrate runs GORM auto-migration for all models.
func Migrate(db *gorm.DB) error {
	if err := db.AutoMigrate(AllModels()...); err != nil {
		return fmt.Errorf("auto-migrate: %w", err)
	}
	if err := createIndexes(db); err != nil {
		return fmt.Errorf("create indexes: %w", err)
	}
	log.Println("database migration complete")
	return nil
}

// createIndexes adds the indexes GORM tags cannot express, using IF NOT EXISTS
// so a repeated boot is harmless.
func createIndexes(db *gorm.DB) error {
	statements := []string{
		// Uniqueness has to exclude soft-deleted rows. A plain unique index lets a
		// deleted exam keep its code forever, so recreating an exam with the same
		// code fails with no way to recover. GORM's uniqueIndex tag cannot express
		// a WHERE clause, hence the raw statements; the DROPs migrate databases
		// created before this was fixed.
		`DROP INDEX IF EXISTS idx_exams_code`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_exams_code_live
		   ON exams (code) WHERE deleted_at IS NULL`,

		`DROP INDEX IF EXISTS idx_subjects_code`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_subjects_code_live
		   ON subjects (code) WHERE deleted_at IS NULL`,

		`DROP INDEX IF EXISTS idx_exam_subject`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_exam_subject_live
		   ON exam_subjects (exam_id, subject_id) WHERE deleted_at IS NULL`,

		// Two partial indexes because Postgres treats NULLs as distinct, so a
		// single index over a nullable subject_id would not stop two identical
		// all-subjects associations.
		`DROP INDEX IF EXISTS idx_assoc_pair`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_assoc_pair_subject_live
		   ON exam_associations (exam_id, source_exam_id, subject_id)
		   WHERE deleted_at IS NULL AND subject_id IS NOT NULL`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_assoc_pair_all_live
		   ON exam_associations (exam_id, source_exam_id)
		   WHERE deleted_at IS NULL AND subject_id IS NULL`,

		// One stored copy per distinct passage, so a comprehension set shared by
		// two documents links to the same text instead of duplicating it.
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_passages_hash_live
		   ON passages (content_hash) WHERE deleted_at IS NULL`,

		// Warehouse browsing is always "questions for this subject, newest
		// first", optionally narrowed by status.
		`CREATE INDEX IF NOT EXISTS idx_questions_subject_status
		   ON questions (subject_id, status) WHERE deleted_at IS NULL`,
		// The generator filters on answered + approved + difficulty per subject,
		// and now also on the quality gate, which is the column that keeps a
		// defective question out of a paper.
		`DROP INDEX IF EXISTS idx_questions_pickable`,
		`CREATE INDEX IF NOT EXISTS idx_questions_pickable
		   ON questions (subject_id, difficulty, quality_status, has_answer, status)
		   WHERE deleted_at IS NULL`,
		// The review queue is "everything not passing, worst and least confident
		// first".
		`CREATE INDEX IF NOT EXISTS idx_questions_review_queue
		   ON questions (quality_status, extraction_confidence)
		   WHERE deleted_at IS NULL AND quality_status <> 'pass'`,
		// Duplicate detection looks the hash up on every extracted question.
		`CREATE INDEX IF NOT EXISTS idx_questions_hash_live
		   ON questions (content_hash) WHERE deleted_at IS NULL`,
		// The job feed polls for runnable work ordered by age.
		`CREATE INDEX IF NOT EXISTS idx_jobs_status_created
		   ON jobs (status, created_at) WHERE deleted_at IS NULL`,
		// Free-text search over question stems in the Warehouse.
		`CREATE INDEX IF NOT EXISTS idx_questions_text_trgm
		   ON questions USING gin (question_text gin_trgm_ops)`,
	}

	// The trigram index needs the extension; if it cannot be created (for
	// example on a managed instance without the extension available) search
	// still works, just with a sequential scan.
	if err := db.Exec(`CREATE EXTENSION IF NOT EXISTS pg_trgm`).Error; err != nil {
		log.Printf("pg_trgm unavailable, question search will not use an index: %v", err)
		statements = statements[:len(statements)-1]
	}

	for _, stmt := range statements {
		if err := db.Exec(stmt).Error; err != nil {
			return fmt.Errorf("%s: %w", stmt, err)
		}
	}
	return nil
}

// DropLegacy removes tables from the previous schema. Destructive and opt-in.
func DropLegacy(db *gorm.DB) error {
	for _, table := range legacyTables {
		if !db.Migrator().HasTable(table) {
			continue
		}
		if err := db.Migrator().DropTable(table); err != nil {
			return fmt.Errorf("drop %s: %w", table, err)
		}
		log.Printf("dropped legacy table %s", table)
	}
	return nil
}

// Reset drops every managed table and rebuilds the schema. Destructive and
// opt-in; used when moving from the old exam-specific schema to this one.
func Reset(db *gorm.DB) error {
	if err := DropLegacy(db); err != nil {
		return err
	}
	all := AllModels()
	// Drop in reverse dependency order.
	for i := len(all) - 1; i >= 0; i-- {
		if err := db.Migrator().DropTable(all[i]); err != nil {
			return fmt.Errorf("drop table: %w", err)
		}
	}
	log.Println("all tables dropped")
	return Migrate(db)
}
