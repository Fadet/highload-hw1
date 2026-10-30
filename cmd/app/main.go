// Command app runs the items microservice.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/Fadet/highload-hw1/internal/api"
	"github.com/Fadet/highload-hw1/internal/metrics"
	"github.com/Fadet/highload-hw1/internal/store"
)

func main() {
	addr := getenv("ADDR", ":8080")
	podID := getenv("POD_ID", "local")
	datacenter := getenv("DATACENTER", "local")

	// Every metric registered through this registerer gets pod and
	// datacenter labels, so handlers do not need to know about them.
	registry := prometheus.NewRegistry()
	reg := prometheus.WrapRegistererWith(prometheus.Labels{
		"pod":        podID,
		"datacenter": datacenter,
	}, registry)
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	mux := http.NewServeMux()

	// Business endpoints go through the instrumenting router.
	router := metrics.NewRouter(reg, mux)
	api.New(store.New()).Register(router)

	// Service endpoints are registered directly and are not instrumented.
	mux.HandleFunc("GET /health", api.Health)
	mux.Handle("GET /metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	mux.Handle("GET /handlers", router.HandlersHandler())

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		slog.Info("starting server", "addr", addr, "pod", podID, "datacenter", datacenter)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server failed", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown failed", "err", err)
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
