package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"hb-task/internal/api"
	"hb-task/internal/core"
	"hb-task/internal/integration/auth"
	"hb-task/internal/integration/camunda"
	"hb-task/internal/integration/events"
	"hb-task/internal/integration/postgres"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		slog.Error("initialize database", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	store := &postgres.Store{Pool: pool}
	if err := pool.Ping(ctx); err != nil {
		slog.Error("connect to database", "error", err)
		os.Exit(1)
	}

	if err := store.Migrate(ctx); err != nil {
		slog.Error("apply database migrations", "error", err)
		os.Exit(1)
	}
	slog.Info("database migrations applied")
	cam := camunda.New(camundaURL, camundaUser, camundaPassword)
	if err := cam.Deploy(ctx); err != nil {
		slog.Error("deploy workflow", "error", err)
		os.Exit(1)
	}
	writer := events.NewWriter(kafkaBroker)
	defer writer.Close()
	verifier := auth.New(ctx, oidcIssuer, oidcJWKS)
	handler := (api.API{Service: core.Service{Repo: store}, Verifier: verifier, Ready: func(c *gin.Context) {
		checkCtx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		if err := pool.Ping(checkCtx); err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "database unavailable"})
			return
		}
		c.Status(http.StatusOK)
	}}).Handler()
	server := &http.Server{Addr: ":8080", Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
	var wg sync.WaitGroup
	wg.Go(func() { (camunda.Worker{Client: cam, Repo: store, ID: "vacation-worker"}).Run(ctx) })
	wg.Go(func() { events.Dispatch(ctx, store, cam, writer) })
	wg.Go(func() {
		consumer := events.Consumer{Brokers: kafkaBroker, ClickHouseURL: clickhouseURL, Username: clickhouseUser, Password: clickhousePassword}
		if err := consumer.Run(ctx); err != nil {
			slog.Error("analytics stopped", "error", err)
			stop()
		}
	})
	serverErr := make(chan error, 1)
	wg.Go(func() { slog.Info("API listening", "address", server.Addr); serverErr <- server.ListenAndServe() })
	var result error
	select {
	case <-ctx.Done():
	case result = <-serverErr:
		stop()
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		_ = server.Close()
		slog.Error("HTTP shutdown", "error", err)
	}
	wg.Wait()
	if result != nil && !errors.Is(result, http.ErrServerClosed) {
		slog.Error("application stopped", "error", result)
		os.Exit(1)
	}
}
