package main

import (
	"context"
	_ "embed"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/transkarpation/gortc/internal/ws"
)

//go:embed index.html
var indexHTML []byte

func main() {
	addr := flag.String("addr", ":8080", "HTTP service address")
	flag.Parse()

	hub := ws.NewHub()
	go hub.Run()

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(indexHTML)
	})
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	r.Get("/ws", func(w http.ResponseWriter, r *http.Request) {
		ws.ServeWS(hub, w, r)
	})

	srv := &http.Server{
		Addr:        *addr,
		Handler:     r,
		ReadTimeout: 15 * time.Second,
		// No WriteTimeout: it would abort long-lived websocket connections.
		IdleTimeout: 60 * time.Second,
	}

	// Start the server in a goroutine so we can listen for shutdown signals.
	go func() {
		log.Printf("websocket server listening on %s (endpoint: /ws)", *addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()

	// Wait for an interrupt signal to gracefully shut down.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Println("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("graceful shutdown failed: %v", err)
	}
	log.Println("server stopped")
}
