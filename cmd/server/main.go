package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kubo-market/idempotency-shield/internal/config"
	"github.com/kubo-market/idempotency-shield/internal/handler"
	"github.com/kubo-market/idempotency-shield/internal/monitor"
	"github.com/kubo-market/idempotency-shield/internal/seed"
	"github.com/kubo-market/idempotency-shield/internal/service"
	"github.com/kubo-market/idempotency-shield/internal/storage"
)

func main() {
	cfg := config.Load()

	// Database
	db, err := storage.NewPostgresDB(cfg.DatabaseDSN)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()
	log.Println("Connected to PostgreSQL")

	// Repository
	repo := storage.NewPostgresRepository(db)

	// Services
	idempotencySvc := service.NewIdempotencyService(repo, cfg.KeyExpiryTTL)
	reportingSvc := service.NewReportingService(repo)

	// Metrics
	metrics := monitor.NewMetrics()

	// Handlers
	paymentHandler := handler.NewPaymentHandler(idempotencySvc)
	reportingHandler := handler.NewReportingHandler(reportingSvc)
	healthHandler := handler.NewHealthHandler(db, metrics)
	policyHandler := handler.NewPolicyHandler(repo)

	// Seed data
	seedData(db)

	// Router
	mux := http.NewServeMux()

	// Health
	mux.HandleFunc("/health", healthHandler.Health)

	// Payments
	mux.HandleFunc("/v1/payments", withMetrics(metrics, paymentHandler.ProcessPayment))
	mux.HandleFunc("/v1/payments/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/complete") {
			paymentHandler.CompletePayment(w, r)
			return
		}
		http.NotFound(w, r)
	})

	// Merchants
	mux.HandleFunc("/v1/merchants/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.Trim(r.URL.Path, "/")
		if strings.HasSuffix(path, "/duplicates") {
			reportingHandler.GetDuplicates(w, r)
			return
		}
		if strings.HasSuffix(path, "/policy") {
			policyHandler.UpdatePolicy(w, r)
			return
		}
		http.NotFound(w, r)
	})

	// Metrics
	mux.HandleFunc("/v1/metrics", healthHandler.Metrics)
	mux.HandleFunc("/v1/metrics/", func(w http.ResponseWriter, r *http.Request) {
		healthHandler.Metrics(w, r)
	})

	// Apply middleware
	var h http.Handler = mux
	h = handler.RequestID(h)
	h = handler.Logging(h)
	h = handler.Recovery(h)

	// Server
	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      h,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Graceful shutdown
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("Shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	log.Printf("Idempotency Shield running on :%s", cfg.Port)
	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("Server error: %v", err)
	}
	log.Println("Server stopped")
}

func withMetrics(m *monitor.Metrics, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sw := &metricsWriter{ResponseWriter: w, status: 200}
		next(sw, r)

		switch sw.status {
		case 201:
			m.RecordNew()
		case 200:
			m.RecordCached()
		case 409:
			m.RecordDuplicate()
		case 422:
			m.RecordMismatch()
		}
	}
}

type metricsWriter struct {
	http.ResponseWriter
	status int
}

func (w *metricsWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func seedData(db *sql.DB) {
	log.Println("Seeding sample data...")
	seedSQL := seed.GenerateSQL()
	_, err := db.Exec(seedSQL)
	if err != nil {
		log.Printf("Seed data (may already exist): %v", err)
	} else {
		log.Println("Seed data loaded successfully")
	}
}
