// Command server runs the Mock Creator API and its background workers.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"mockcreator/internal/config"
	"mockcreator/internal/converter"
	"mockcreator/internal/database"
	"mockcreator/internal/pipeline"
	"mockcreator/internal/server"
)

func main() {
	migrateOnly := flag.Bool("migrate", false, "run database migrations and exit")
	reset := flag.Bool("reset", false,
		"DESTRUCTIVE: drop every table and rebuild the schema, then exit")
	dropLegacy := flag.Bool("drop-legacy", false,
		"drop tables left over from the previous schema, then exit")
	flag.Parse()

	cfg := config.Load()

	db, err := database.Connect(cfg)
	if err != nil {
		log.Fatalf("database connect failed: %v", err)
	}
	if err := database.WaitForDB(db, 30, 2*time.Second); err != nil {
		log.Fatalf("database never became ready: %v", err)
	}

	switch {
	case *reset:
		log.Println("resetting schema (this deletes all data)")
		if err := database.Reset(db); err != nil {
			log.Fatalf("reset failed: %v", err)
		}
		if err := database.Seed(db); err != nil {
			log.Fatalf("seed failed: %v", err)
		}
		return
	case *dropLegacy:
		if err := database.DropLegacy(db); err != nil {
			log.Fatalf("dropping legacy tables failed: %v", err)
		}
		return
	}

	if err := database.Migrate(db); err != nil {
		log.Fatalf("migration failed: %v", err)
	}
	if err := database.Seed(db); err != nil {
		log.Fatalf("seed failed: %v", err)
	}
	if *migrateOnly {
		log.Println("migrate-only flag set; exiting")
		return
	}

	conv := converter.New(cfg.Converter)

	// Report the converter's state at boot so a misconfigured URL is obvious
	// here instead of surfacing as a failed upload later.
	probeCtx, cancelProbe := context.WithTimeout(context.Background(), 10*time.Second)
	if health, err := conv.Health(probeCtx); err != nil {
		log.Printf("converter at %s is not answering yet: %v", cfg.Converter.ServiceURL, err)
	} else {
		log.Printf("converter ready: engines=%v docling=%s ocr=%v models_ready=%t",
			health.Engines, health.EngineVersion, health.OCREngines, health.ModelsReady)
	}
	cancelProbe()

	jobs := pipeline.New(db, cfg, conv)
	workerCtx, stopWorkers := context.WithCancel(context.Background())
	jobs.Start(workerCtx)

	router := server.New(db, cfg, jobs, conv)
	srv := &http.Server{
		Addr:    ":" + cfg.ServerPort,
		Handler: router,
		// Generous because uploads can be large; the converter itself is polled,
		// so no request ever waits for OCR.
		ReadHeaderTimeout: 20 * time.Second,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       2 * time.Minute,
	}

	go func() {
		log.Printf("content engine API listening on %s (env=%s)", srv.Addr, cfg.Env)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	// Drain in-flight work on shutdown rather than losing it.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("shutdown requested")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("http shutdown: %v", err)
	}

	stopWorkers()
	jobs.Shutdown(30 * time.Second)
	log.Println("stopped")
}
