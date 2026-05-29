package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"net/http"
	"net/url"
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

// redactedQueryParams are stripped from request URLs before they are logged.
var redactedQueryParams = []string{"apiKey", "authorization"}

// redactingLogger logs each request like chi's middleware.Logger but masks
// sensitive query parameters so secrets never reach the logs.
func redactingLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		start := time.Now()
		defer func() {
			log.Printf("%q %s -> %d %dB in %s",
				r.Method+" "+redactURL(r.URL), r.RemoteAddr,
				ww.Status(), ww.BytesWritten(), time.Since(start))
		}()
		next.ServeHTTP(ww, r)
	})
}

// devTokenHandler mints a JWT for the requested userId. It exists only to make
// local testing easy and is registered solely when -dev is set.
func devTokenHandler(auth ws.Authenticator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := r.URL.Query().Get("userId")
		if userID == "" {
			http.Error(w, "missing userId query parameter", http.StatusBadRequest)
			return
		}
		ttl := time.Hour
		token, err := auth.Mint(userID, ttl)
		if err != nil {
			http.Error(w, "failed to mint token", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"userId":    userID,
			"token":     token,
			"expiresIn": ttl.String(),
		})
	}
}

// redactURL returns the request URI with sensitive query parameters masked.
func redactURL(u *url.URL) string {
	q := u.Query()
	masked := false
	for _, k := range redactedQueryParams {
		if q.Has(k) {
			q.Set(k, "REDACTED")
			masked = true
		}
	}
	if !masked {
		return u.RequestURI()
	}
	redacted := *u
	redacted.RawQuery = q.Encode()
	return redacted.RequestURI()
}

func main() {
	addr := flag.String("addr", ":8080", "HTTP service address")
	apiKey := flag.String("api-key", os.Getenv("WS_API_KEY"), "expected apiKey for websocket auth (env: WS_API_KEY)")
	jwtSecret := flag.String("jwt-secret", os.Getenv("WS_JWT_SECRET"), "HMAC secret for verifying the authorization JWT (env: WS_JWT_SECRET)")
	dev := flag.Bool("dev", false, "enable the /dev/token endpoint for minting test JWTs (never use in production)")
	flag.Parse()

	if *apiKey == "" || *jwtSecret == "" {
		log.Fatal("both -api-key and -jwt-secret (or WS_API_KEY / WS_JWT_SECRET) must be set")
	}
	auth := ws.Authenticator{APIKey: *apiKey, JWTSecret: []byte(*jwtSecret)}

	hub := ws.NewHub()
	go hub.Run()

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(redactingLogger)
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
		ws.ServeWS(hub, auth, w, r)
	})

	if *dev {
		log.Println("WARNING: dev mode enabled, /dev/token will mint JWTs for any userId")
		r.Get("/dev/token", devTokenHandler(auth))
	}

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
