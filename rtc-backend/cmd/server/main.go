package main

import (
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/joho/godotenv"
	"github.com/transkarpation/gortc/rtc-backend/internal/config"
	"github.com/transkarpation/gortc/rtc-backend/internal/ws"
)

//go:embed index.html
var indexHTML []byte

// redactedQueryParams are stripped from request URLs before they are logged.
var redactedQueryParams = []string{"apiKey", "authorization"}

// setupLogger installs a slog default logger writing to stderr in the given
// format ("json" or "text").
func setupLogger(format string) {
	var h slog.Handler
	if format == "json" {
		h = slog.NewJSONHandler(os.Stderr, nil)
	} else {
		h = slog.NewTextHandler(os.Stderr, nil)
	}
	slog.SetDefault(slog.New(h))
}

// envOr returns the value of the environment variable key, or def if unset.
func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// appsByKey indexes configured apps by their API key for the Authenticator.
func appsByKey(apps []config.App) map[string]ws.App {
	m := make(map[string]ws.App, len(apps))
	for _, a := range apps {
		m[a.APIKey] = ws.App{AppID: a.AppID, APISecret: []byte(a.APISecret)}
	}
	return m
}

// redactingLogger logs each request like chi's middleware.Logger but masks
// sensitive query parameters so secrets never reach the logs.
func redactingLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		start := time.Now()
		defer func() {
			slog.Info("request",
				"method", r.Method,
				"path", redactURL(r.URL),
				"status", ww.Status(),
				"bytes", ww.BytesWritten(),
				"duration", time.Since(start),
				"remote", r.RemoteAddr,
			)
		}()
		next.ServeHTTP(ww, r)
	})
}

// devTokenHandler mints a JWT for the requested apiKey + userId. It exists only
// to make local testing easy and is registered solely when -dev is set.
func devTokenHandler(auth ws.Authenticator) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		apiKey, userID := q.Get("apiKey"), q.Get("userId")
		if apiKey == "" || userID == "" {
			http.Error(w, "missing apiKey or userId query parameter", http.StatusBadRequest)
			return
		}
		ttl := time.Hour
		token, err := auth.Mint(apiKey, userID, ttl)
		if err != nil {
			http.Error(w, "unknown apiKey", http.StatusBadRequest)
			return
		}
		appID, _ := auth.AppID(apiKey)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"appId":           appID,
			"userId":          userID,
			"token":           token,
			"expiresIn":       ttl.String(),
			"personalChannel": ws.PersonalChannel(appID, userID),
		})
	}
}

// internalAuth guards server-to-server endpoints: the Authorization header must
// equal the shared secret.
func internalAuth(secret string) func(http.Handler) http.Handler {
	want := []byte(secret)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// publishHandler delivers a posted message to all websocket clients subscribed
// to the given channel.
func publishHandler(hub *ws.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Channel string `json:"channel"`
			Data    string `json:"data"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
		if body.Channel == "" {
			http.Error(w, "missing channel", http.StatusBadRequest)
			return
		}
		hub.Publish(body.Channel, []byte(body.Data))
		w.WriteHeader(http.StatusAccepted)
	}
}

// subscribeHandler subscribes the connection identified by connectionId to the
// given channels. Returns 404 if no connection with that id exists.
func subscribeHandler(hub *ws.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ConnectionID string   `json:"connectionId"`
			Channels     []string `json:"channels"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
			http.Error(w, "invalid JSON body", http.StatusBadRequest)
			return
		}
		if body.ConnectionID == "" {
			http.Error(w, "missing connectionId", http.StatusBadRequest)
			return
		}
		if len(body.Channels) == 0 {
			http.Error(w, "missing channels", http.StatusBadRequest)
			return
		}
		if !hub.Subscribe(body.ConnectionID, body.Channels) {
			http.Error(w, "unknown connectionId", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusAccepted)
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
	// Load .env into the process environment before reading flag defaults. A
	// missing file is fine; real environment variables always take precedence.
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		slog.Warn("could not load .env", "err", err)
	}

	addr := flag.String("addr", ":8080", "HTTP service address")
	appsFile := flag.String("apps-file", envOr("WS_APPS_FILE", "apps.json"), "path to the JSON apps config (env: WS_APPS_FILE)")
	publishSecret := flag.String("publish-secret", os.Getenv("WS_PUBLISH_SECRET"), "shared secret required (Authorization header) to call POST /publish (env: WS_PUBLISH_SECRET)")
	logFormat := flag.String("log-format", envOr("LOG_FORMAT", "text"), "log output format: text or json")
	dev := flag.Bool("dev", false, "enable the /dev/token endpoint for minting test JWTs (never use in production)")
	flag.Parse()

	setupLogger(*logFormat)

	if *publishSecret == "" {
		slog.Error("-publish-secret (or WS_PUBLISH_SECRET) must be set")
		os.Exit(1)
	}

	cfg, err := config.Load(*appsFile)
	if err != nil {
		slog.Error("load apps config", "err", err)
		os.Exit(1)
	}
	auth := ws.NewAuthenticator(appsByKey(cfg.Apps))
	slog.Info("loaded apps", "count", len(cfg.Apps), "file", *appsFile)

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

	// Internal server-to-server endpoints, guarded by the shared publish secret.
	r.Group(func(r chi.Router) {
		r.Use(internalAuth(*publishSecret))
		r.Post("/publish", publishHandler(hub))
		r.Post("/subscribe", subscribeHandler(hub))
	})

	if *dev {
		slog.Warn("dev mode enabled: /dev/token will mint JWTs for any userId")
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
		slog.Info("listening", "addr", *addr, "endpoint", "/ws")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	// Wait for an interrupt signal to gracefully shut down.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	slog.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("graceful shutdown failed", "err", err)
		os.Exit(1)
	}
	slog.Info("server stopped")
}
