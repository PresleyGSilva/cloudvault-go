package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/PresleyGSilva/cloudvault-go/internal/auth"
	"github.com/PresleyGSilva/cloudvault-go/internal/config"
	"github.com/PresleyGSilva/cloudvault-go/internal/db"
	"github.com/PresleyGSilva/cloudvault-go/internal/handlers"
	"github.com/PresleyGSilva/cloudvault-go/internal/httpx"
	"github.com/PresleyGSilva/cloudvault-go/internal/storage"
)

// resolve finds a bundled asset (migrations dir, openapi.yaml) relative to the
// working directory, APP_DIR, or the executable location (in that order).
func resolve(p string) string {
	if _, err := os.Stat(p); err == nil {
		return p
	}
	if d := os.Getenv("APP_DIR"); d != "" {
		if cand := filepath.Join(d, p); statOK(cand) {
			return cand
		}
	}
	if ex, err := os.Executable(); err == nil {
		if cand := filepath.Join(filepath.Dir(ex), p); statOK(cand) {
			return cand
		}
	}
	return p
}

func statOK(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	cfg := config.Load()
	ctx := context.Background()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("db connect failed", "err", err)
		os.Exit(1)
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool, resolve("migrations")); err != nil {
		slog.Error("migrate failed", "err", err)
		os.Exit(1)
	}
	if err := db.Seed(ctx, pool); err != nil {
		slog.Error("seed failed", "err", err)
		os.Exit(1)
	}

	store, err := storage.New(cfg.MinioEndpoint, cfg.MinioUser, cfg.MinioPass, cfg.MinioBucket, cfg.MinioUseSSL)
	if err != nil {
		slog.Error("storage init failed", "err", err)
		os.Exit(1)
	}
	sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := store.EnsureBucket(sctx); err != nil {
		// MinIO may come up slightly later under compose; retry once
		slog.Warn("bucket ensure failed, retrying", "err", err)
		time.Sleep(5 * time.Second)
		if err2 := store.EnsureBucket(ctx); err2 != nil {
			slog.Error("bucket ensure failed", "err", err2)
			os.Exit(1)
		}
	}
	_ = sctx

	h := handlers.New(pool, cfg.JWTSecret)
	drive := &handlers.Drive{Handler: h, Store: store}

	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.RealIP, middleware.Recoverer)
	r.Use(cors)
	r.Get("/healthz", func(w http.ResponseWriter, req *http.Request) {
		httpx.Data(w, 200, map[string]any{"ok": true, "service": "cloudvault"})
	})
	r.Get("/openapi.yaml", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		http.ServeFile(w, req, resolve("openapi.yaml"))
	})

	r.Route("/api/v1", func(v chi.Router) {
		v.Post("/auth/register", h.Register)
		v.Post("/auth/login", h.Login)
		v.Post("/auth/refresh", h.Refresh)
		v.Post("/auth/logout", h.Logout)
		v.Get("/s/{token}", drive.ResolveShare)
		v.Get("/s/{token}/download", drive.DownloadShare)
		v.Group(func(p chi.Router) {
			p.Use(auth.RequireAuth(cfg.JWTSecret))
			p.Get("/me", h.Me)
			p.Group(func(t chi.Router) {
				t.Use(auth.RequireOrg(pool))
				t.Get("/folders", drive.ListFolders)
				t.Post("/folders", drive.CreateFolder)
				t.Patch("/folders/{id}", drive.UpdateFolder)
				t.Delete("/folders/{id}", drive.TrashFolder)
				t.Post("/folders/{id}/restore", drive.RestoreFolder)
				t.Get("/folders/{id}/permissions", drive.FolderPermissions)
				t.Put("/folders/{id}/permissions", drive.SetFolderPermission)
				t.Post("/folders/{id}/shares", drive.CreateFolderShare)
				t.Get("/files", drive.ListFiles)
				t.Post("/files/presign", drive.Presign)
				t.Post("/files/complete", drive.Complete)
				t.Post("/files/upload", drive.Upload)
				t.Get("/files/{id}", drive.GetFile)
				t.Patch("/files/{id}", drive.UpdateFile)
				t.Delete("/files/{id}", drive.TrashFile)
				t.Post("/files/{id}/restore", drive.RestoreFile)
				t.Get("/files/{id}/download", drive.Download)
				t.Get("/files/{id}/preview", drive.Preview)
				t.Get("/files/{id}/permissions", drive.FilePermissions)
				t.Put("/files/{id}/permissions", drive.SetFilePermission)
				t.Post("/files/{id}/shares", drive.CreateFileShare)
				t.Get("/activities", drive.Activities)
				t.Get("/members", drive.Members)
				t.Get("/quota", drive.Quota)
			})
		})
	})

	srv := &http.Server{Addr: ":" + cfg.Port, Handler: r}
	go func() {
		slog.Info("listening", "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("serve failed", "err", err)
			os.Exit(1)
		}
	}()
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	slog.Info("shutting down")
	sctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()
	_ = srv.Shutdown(sctx2)
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Reflect the Origin so credentialed (cookie) requests pass the
		// browser check; `*` is rejected when credentials are included.
		if origin := r.Header.Get("Origin"); origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		} else {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		}
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Org-ID, X-User")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	})
}
