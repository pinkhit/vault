package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/pinkhit/vault/backend/internal/analytics"
	"github.com/pinkhit/vault/backend/internal/api"
	"github.com/pinkhit/vault/backend/internal/auth"
	"github.com/pinkhit/vault/backend/internal/config"
	"github.com/pinkhit/vault/backend/internal/database"
	"github.com/pinkhit/vault/backend/internal/documents"
	"github.com/pinkhit/vault/backend/internal/indexing"
	"github.com/pinkhit/vault/backend/internal/storage"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	// New database and journal files must not be world readable.
	syscall.Umask(0077)
	c, err := config.Load()
	if err != nil {
		return err
	}
	store, err := storage.Open(c.DataDir)
	if err != nil {
		return err
	}
	defer store.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	db, err := database.Open(ctx, filepath.Join(c.DataDir, "metadata.sqlite"))
	cancel()
	if err != nil {
		return err
	}
	defer db.Close()

	service := &documents.Service{
		DB:       db,
		Store:    store,
		MaxBytes: c.MaxUploadBytes,
		Log:      log,
	}
	// Recovery precedes the listener; no request can race cleanup.
	if err = service.Recover(context.Background()); err != nil {
		return err
	}
	if _, err = indexing.Recover(context.Background(), db); err != nil {
		return err
	}
	if c.StaticDir != "" {
		if _, err = os.Stat(filepath.Join(c.StaticDir, "index.html")); err != nil {
			return fmt.Errorf("frontend build missing: %w", err)
		}
	}
	authentication, err := auth.New(db, c.SessionTTL, c.SessionIdleTTL, c.SecureCookies)
	if err != nil {
		return err
	}
	metrics := analytics.New(db, store.VolumeCapacity, os.Getenv("FAMILYVAULT_CONTAINER") == "1")
	metrics.HostStorageFile = os.Getenv("FAMILYVAULT_HOST_STORAGE_FILE")
	handler := (&api.API{Documents: service, Auth: authentication, Storage: metrics, Log: log}).Handler(c.StaticDir)
	server := &http.Server{
		Addr:              c.Address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       2 * time.Minute,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	workerCtx, stopWorker := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		if err := indexing.NewWorker(db, store, log).Run(workerCtx); err != nil {
			log.Error("indexing worker stopped")
		}
	}()
	defer func() {
		stopWorker()
		<-workerDone
	}()
	result := make(chan error, 1)
	go func() { result <- server.ListenAndServe() }()
	log.Info("authenticated localhost server: synthetic documents only", "address", c.Address)
	select {
	case err = <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-signalCtx.Done():
		stopWorker()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err = server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return err
		}
		return nil
	}
}
