package handlers

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/PresleyGSilva/cloudvault-go/internal/auth"
	"github.com/PresleyGSilva/cloudvault-go/internal/httpx"
)

type shareReq struct {
	ExpiresInHours int `json:"expiresInHours"`
}

func (d *Drive) CreateFileShare(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	fr, ok := d.fileRow(r, id)
	if !ok {
		httpx.Err(w, http.StatusNotFound, "not_found", "file not found")
		return
	}
	if !d.canReadFile(r, fr.ID) {
		httpx.Err(w, http.StatusForbidden, "forbidden", "no access to this file")
		return
	}
	var req shareReq
	_ = httpx.Decode(w, r, &req)
	exp := req.ExpiresInHours
	if exp <= 0 {
		exp = 24
	}
	if exp > 168 {
		httpx.Err(w, http.StatusBadRequest, "bad_request", "links expire after at most 168 hours (7 days)")
		return
	}
	token := uuid.NewString()[:8]
	expires := time.Now().Add(time.Duration(exp) * time.Hour)
	var sid string
	if err := d.Pool.QueryRow(r.Context(),
		`INSERT INTO shares (org_id, file_id, token, expires_at, created_by) VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		auth.OrgID(r), id, token, expires, auth.UserID(r)).Scan(&sid); err != nil {
		httpx.Err(w, 500, "internal", "could not create share link")
		return
	}
	d.logActivity(r, "file.share", "file", id, "{}")
	httpx.Data(w, 201, map[string]any{"id": sid, "token": token, "expiresAt": expires.UTC().Format(time.RFC3339)})
}

func (d *Drive) CreateFolderShare(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !d.folderOwned(r, id) {
		httpx.Err(w, http.StatusNotFound, "not_found", "folder not found")
		return
	}
	var req shareReq
	_ = httpx.Decode(w, r, &req)
	exp := req.ExpiresInHours
	if exp <= 0 {
		exp = 24
	}
	if exp > 168 {
		httpx.Err(w, http.StatusBadRequest, "bad_request", "links expire after at most 168 hours (7 days)")
		return
	}
	token := uuid.NewString()[:8]
	expires := time.Now().Add(time.Duration(exp) * time.Hour)
	var sid string
	if err := d.Pool.QueryRow(r.Context(),
		`INSERT INTO shares (org_id, folder_id, token, expires_at, created_by) VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		auth.OrgID(r), id, token, expires, auth.UserID(r)).Scan(&sid); err != nil {
		httpx.Err(w, 500, "internal", "could not create share link")
		return
	}
	d.logActivity(r, "folder.share", "folder", id, "{}")
	httpx.Data(w, 201, map[string]any{"id": sid, "token": token, "expiresAt": expires.UTC().Format(time.RFC3339)})
}

// ResolveShare is public (no auth): validates token + expiry.
func (d *Drive) ResolveShare(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	var sid, org, fileID, folderID *string
	var expires time.Time
	var fid, did *string
	err := d.Pool.QueryRow(r.Context(),
		`SELECT id, org_id, file_id, folder_id, expires_at FROM shares WHERE token=$1`, token).
		Scan(&sid, &org, &fid, &did, &expires)
	if err != nil {
		httpx.Err(w, http.StatusNotFound, "not_found", "share link not found")
		return
	}
	_ = fileID
	_ = folderID
	if time.Now().After(expires) {
		httpx.Err(w, http.StatusGone, "expired", "this share link has expired")
		return
	}
	if fid != nil {
		var name, key, mime string
		var size int64
		if err := d.Pool.QueryRow(r.Context(),
			`SELECT display_name, s3_key, mime, size_bytes FROM files WHERE id=$1 AND deleted_at IS NULL`, *fid).
			Scan(&name, &key, &mime, &size); err != nil {
			httpx.Err(w, http.StatusGone, "gone", "shared file is no longer available")
			return
		}
		httpx.Data(w, 200, map[string]any{"type": "file", "name": name, "mime": mime, "sizeBytes": size, "expiresAt": expires.UTC().Format(time.RFC3339)})
		return
	}
	var fname string
	_ = d.Pool.QueryRow(r.Context(), `SELECT name FROM folders WHERE id=$1`, *did).Scan(&fname)
	rs, _ := d.Pool.Query(r.Context(),
		`SELECT display_name, mime, size_bytes FROM files WHERE folder_id=$1 AND deleted_at IS NULL ORDER BY display_name`, *did)
	files := []map[string]any{}
	if rs != nil {
		defer rs.Close()
		for rs.Next() {
			var n, m string
			var s int64
			_ = rs.Scan(&n, &m, &s)
			files = append(files, map[string]any{"name": n, "mime": m, "sizeBytes": s})
		}
	}
	httpx.Data(w, 200, map[string]any{"type": "folder", "name": fname, "files": files, "expiresAt": expires.UTC().Format(time.RFC3339)})
}

// DownloadShare streams the shared file via a short-lived storage URL.
func (d *Drive) DownloadShare(w http.ResponseWriter, r *http.Request) {
	token := chi.URLParam(r, "token")
	var fid *string
	var expires time.Time
	err := d.Pool.QueryRow(r.Context(), `SELECT file_id, expires_at FROM shares WHERE token=$1`, token).Scan(&fid, &expires)
	if err != nil || fid == nil {
		httpx.Err(w, http.StatusNotFound, "not_found", "share link not found")
		return
	}
	if time.Now().After(expires) {
		httpx.Err(w, http.StatusGone, "expired", "this share link has expired")
		return
	}
	var name, key, mime string
	if err := d.Pool.QueryRow(r.Context(), `SELECT display_name, s3_key, mime FROM files WHERE id=$1 AND deleted_at IS NULL`, *fid).Scan(&name, &key, &mime); err != nil {
		httpx.Err(w, http.StatusGone, "gone", "shared file is no longer available")
		return
	}
	u, err := d.Store.PresignGet(r.Context(), key, strings.ToLower(r.URL.Query().Get("inline")) == "true", name)
	if err != nil {
		httpx.Err(w, 500, "internal", "could not create download URL")
		return
	}
	httpx.Data(w, 200, map[string]any{"url": u, "expiresIn": 300, "filename": name})
}
