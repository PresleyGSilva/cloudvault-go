package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PresleyGSilva/cloudvault-go/internal/auth"
	"github.com/PresleyGSilva/cloudvault-go/internal/db"
	"github.com/PresleyGSilva/cloudvault-go/internal/storage"
)

var (
	tpool  *pgxpool.Pool
	tstore *storage.Store
	tsec   = "test-secret"
)

func setup(t *testing.T) {
	t.Helper()
	if tpool != nil {
		return
	}
	durl := os.Getenv("TEST_DATABASE_URL")
	if durl == "" {
		durl = "postgres://cloudvault:cloudvault@localhost:5440/cloudvault?sslmode=disable"
	}
	ctx := context.Background()
	pool, err := db.Connect(ctx, durl)
	if err != nil {
		t.Skipf("no test database: %v", err)
	}
	if err := db.Migrate(ctx, pool, "../../migrations"); err != nil {
		t.Skipf("migrate failed: %v", err)
	}
	ep := os.Getenv("TEST_MINIO_ENDPOINT")
	if ep == "" {
		ep = "localhost:9000"
	}
	st, err := storage.New(ep, "minioadmin", "minioadmin", "cloudvault-test", false)
	if err != nil {
		t.Skipf("no minio: %v", err)
	}
	if err := st.EnsureBucket(ctx); err != nil {
		t.Skipf("minio bucket: %v", err)
	}
	tpool, tstore = pool, st
}

func router() *chi.Mux {
	h := New(tpool, tsec)
	d := &Drive{Handler: h, Store: tstore}
	r := chi.NewRouter()
	r.Post("/auth/register", h.Register)
	r.Post("/auth/login", h.Login)
	r.Group(func(p chi.Router) {
		p.Use(auth.RequireAuth(tsec))
		p.Get("/me", h.Me)
		p.Group(func(g chi.Router) {
			g.Use(auth.RequireOrg(tpool))
			g.Post("/folders", d.CreateFolder)
			g.Post("/files/upload", d.Upload)
			g.Post("/files/presign", d.Presign)
			g.Post("/files/complete", d.Complete)
			g.Get("/files/{id}", d.GetFile)
			g.Get("/files/{id}/download", d.Download)
			g.Delete("/files/{id}", d.TrashFile)
			g.Post("/files/{id}/shares", d.CreateFileShare)
		})
	})
	// public share routes (no auth)
	r.Get("/s/{token}", d.ResolveShare)
	r.Get("/s/{token}/download", d.DownloadShare)
	_ = h
	return r
}

func doReq(t *testing.T, r http.Handler, method, path string, body any, token, org string) *httptest.ResponseRecorder {
	t.Helper()
	var rd io.Reader
	if body != nil {
		switch b := body.(type) {
		case *bytes.Buffer:
			rd = b
		default:
			raw, _ := json.Marshal(body)
			rd = bytes.NewReader(raw)
		}
	}
	req := httptest.NewRequest(method, path, rd)
	if body != nil {
		if _, ok := body.(*bytes.Buffer); !ok {
			req.Header.Set("Content-Type", "application/json")
		}
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if org != "" {
		req.Header.Set("X-Org-ID", org)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func register(t *testing.T, r http.Handler, name, email string) (token, org string) {
	t.Helper()
	rec := doReq(t, r, "POST", "/auth/register",
		map[string]string{"name": name, "email": email, "password": "Secret123!", "orgName": name + " Org"}, "", "")
	if rec.Code != 200 {
		t.Fatalf("register %s: %d %s", email, rec.Code, rec.Body.String())
	}
	var out struct {
		Data struct {
			AccessToken string `json:"accessToken"`
			Orgs        []struct {
				ID string `json:"id"`
			} `json:"orgs"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Data.AccessToken == "" || len(out.Data.Orgs) == 0 {
		t.Fatalf("bad session payload: %s", rec.Body.String())
	}
	return out.Data.AccessToken, out.Data.Orgs[0].ID
}

func uploadProxy(t *testing.T, r http.Handler, token, org, filename, ctype string, content []byte) string {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("filename", filename)
	_ = mw.WriteField("contentType", ctype)
	fw, _ := mw.CreateFormFile("file", filename)
	_, _ = fw.Write(content)
	_ = mw.Close()
	req := httptest.NewRequest("POST", "/files/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Org-ID", org)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 201 {
		t.Fatalf("upload %s: %d %s", filename, rec.Code, rec.Body.String())
	}
	var out struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out.Data.ID
}

// Cross-org access must be forbidden.
func TestCrossOrgForbidden(t *testing.T) {
	setup(t)
	r := router()
	tokA, orgA := register(t, r, "Alice", fmt.Sprintf("alice-%d@x.local", time.Now().UnixNano()))
	tokB, _ := register(t, r, "Bob", fmt.Sprintf("bob-%d@x.local", time.Now().UnixNano()))
	fid := uploadProxy(t, r, tokA, orgA, "secret.txt", "text/plain", []byte("top secret"))
	// Bob tries with his own token but Alice's org header: not a member -> 403
	rec := doReq(t, r, "GET", "/files/"+fid, nil, tokB, orgA)
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
		t.Fatalf("expected 403/404 cross-org, got %d %s", rec.Code, rec.Body.String())
	}
}

// Path traversal names must be refused at upload.
func TestPathTraversalRefused(t *testing.T) {
	setup(t)
	r := router()
	tok, org := register(t, r, "Trav", fmt.Sprintf("trav-%d@x.local", time.Now().UnixNano()))
	for _, evil := range []string{"../evil.txt", "a/b.txt", ".."} {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		_ = mw.WriteField("filename", evil)
		_ = mw.WriteField("contentType", "text/plain")
		fw, _ := mw.CreateFormFile("file", "x.txt")
		_, _ = fw.Write([]byte("x"))
		_ = mw.Close()
		req := httptest.NewRequest("POST", "/files/upload", &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("X-Org-ID", org)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for %q, got %d %s", evil, rec.Code, rec.Body.String())
		}
	}
	// forged s3 key on complete must be refused
	rec := doReq(t, r, "POST", "/files/complete",
		map[string]any{"filename": "ok.txt", "contentType": "text/plain", "s3Key": "../../etc/passwd", "sizeBytes": 2}, tok, org)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for forged key, got %d %s", rec.Code, rec.Body.String())
	}
}

// Upload/download round-trip through the API proxy + MinIO.
func TestUploadDownloadE2E(t *testing.T) {
	setup(t)
	r := router()
	tok, org := register(t, r, "E2E", fmt.Sprintf("e2e-%d@x.local", time.Now().UnixNano()))
	payload := []byte("hello cloudvault " + time.Now().String())
	fid := uploadProxy(t, r, tok, org, "hello.txt", "text/plain", payload)
	rec := doReq(t, r, "GET", "/files/"+fid+"/download", nil, tok, org)
	if rec.Code != 200 {
		t.Fatalf("download: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Data struct {
			URL string `json:"url"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out.Data.URL == "" {
		t.Fatal("empty download url")
	}
	resp, err := http.Get(out.Data.URL)
	if err != nil {
		t.Fatalf("fetch presigned url: %v", err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(got, payload) {
		t.Fatalf("round-trip mismatch: %q vs %q", got, payload)
	}
}

// Share links must expire (410 Gone).
func TestShareExpiry(t *testing.T) {
	setup(t)
	r := router()
	tok, org := register(t, r, "Shar", fmt.Sprintf("shar-%d@x.local", time.Now().UnixNano()))
	fid := uploadProxy(t, r, tok, org, "shared.txt", "text/plain", []byte("share me"))
	rec := doReq(t, r, "POST", "/files/"+fid+"/shares", map[string]any{"expiresInHours": 1}, tok, org)
	if rec.Code != 201 {
		t.Fatalf("share: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	// force expiry
	_, err := tpool.Exec(context.Background(), `UPDATE shares SET expires_at=now()-interval '1 hour' WHERE token=$1`, out.Data.Token)
	if err != nil {
		t.Fatal(err)
	}
	rec2 := doReq(t, r, "GET", "/s/"+out.Data.Token, nil, "", "")
	if rec2.Code != http.StatusGone {
		t.Fatalf("expected 410 expired, got %d %s", rec2.Code, rec2.Body.String())
	}
	// fresh link resolves
	rec3 := doReq(t, r, "POST", "/files/"+fid+"/shares", map[string]any{"expiresInHours": 1}, tok, org)
	var out2 struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec3.Body.Bytes(), &out2)
	rec4 := doReq(t, r, "GET", "/s/"+out2.Data.Token, nil, "", "")
	if rec4.Code != 200 {
		t.Fatalf("expected 200 fresh share, got %d %s", rec4.Code, rec4.Body.String())
	}
}
