package main

import (
	"context"
	"crypto/subtle"
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
	"github.com/joho/godotenv"
	"github.com/transkarpation/gortc/internal/config"
	"github.com/transkarpation/gortc/internal/ws"
)

//go:embed index.html
var indexHTML []byte

// redactedQueryParams are stripped from request URLs before they are logged.
var redactedQueryParams = []string{"apiKey", "authorization"}

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
			log.Printf("%q %s -> %d %dB in %s",
				r.Method+" "+redactURL(r.URL), r.RemoteAddr,
				ww.Status(), ww.BytesWritten(), time.Since(start))
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

// publishHandler delivers a posted message to all websocket clients subscribed
// to the given channel. It is an internal, server-to-server endpoint guarded by
// a shared secret: the Authorization header must equal publishSecret.
func publishHandler(hub *ws.Hub, publishSecret string) http.HandlerFunc {
	secret := []byte(publishSecret)
	return func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), secret) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
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
		log.Printf("warning: could not load .env: %v", err)
	}

	addr := flag.String("addr", ":8080", "HTTP service address")
	appsFile := flag.String("apps-file", envOr("WS_APPS_FILE", "apps.json"), "path to the JSON apps config (env: WS_APPS_FILE)")
	publishSecret := flag.String("publish-secret", os.Getenv("WS_PUBLISH_SECRET"), "shared secret required (Authorization header) to call POST /publish (env: WS_PUBLISH_SECRET)")
	dev := flag.Bool("dev", false, "enable the /dev/token endpoint for minting test JWTs (never use in production)")
	flag.Parse()

	if *publishSecret == "" {
		log.Fatal("-publish-secret (or WS_PUBLISH_SECRET) must be set")
	}

	cfg, err := config.Load(*appsFile)
	if err != nil {
		log.Fatalf("load apps config: %v", err)
	}
	auth := ws.NewAuthenticator(appsByKey(cfg.Apps))
	log.Printf("loaded %d app(s) from %s", len(cfg.Apps), *appsFile)

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
	r.Post("/publish", publishHandler(hub, *publishSecret))

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
