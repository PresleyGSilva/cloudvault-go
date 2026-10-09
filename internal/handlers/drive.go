package handlers

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/PresleyGSilva/cloudvault-go/internal/auth"
	"github.com/PresleyGSilva/cloudvault-go/internal/httpx"
	"github.com/PresleyGSilva/cloudvault-go/internal/storage"
	"github.com/PresleyGSilva/cloudvault-go/internal/validate"
)

type Drive struct {
	*Handler
	Store *storage.Store
}

// jsonMeta builds a tiny JSON object with properly escaped string values.
func jsonMeta(k, v string) string {
	b, _ := json.Marshal(map[string]string{k: v})
	return string(b)
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (d *Drive) logActivity(r *http.Request, action, entityType, entityID, meta string) {
	if meta == "" {
		meta = "{}"
	}
	_, _ = d.Pool.Exec(r.Context(),
		`INSERT INTO activities (org_id, user_id, action, entity_type, entity_id, meta) VALUES ($1,$2,$3,$4,$5,$6::jsonb)`,
		auth.OrgID(r), auth.UserID(r), action, entityType, entityID, meta)
}

// ---------- folders ----------

func (d *Drive) ListFolders(w http.ResponseWriter, r *http.Request) {
	org := auth.OrgID(r)
	parent := r.URL.Query().Get("parentId")
	trash := r.URL.Query().Get("trash") == "true"
	var q string
	var args []any
	if trash {
		q = `SELECT id, parent_id, name, created_at FROM folders WHERE org_id=$1 AND deleted_at IS NOT NULL ORDER BY updated_at DESC`
		args = []any{org}
	} else if parent != "" {
		q = `SELECT id, parent_id, name, created_at FROM folders WHERE org_id=$1 AND parent_id=$2 AND deleted_at IS NULL ORDER BY name`
		args = []any{org, parent}
	} else {
		q = `SELECT id, parent_id, name, created_at FROM folders WHERE org_id=$1 AND parent_id IS NULL AND deleted_at IS NULL ORDER BY name`
		args = []any{org}
	}
	rs, err := d.Pool.Query(r.Context(), q, args...)
	if err != nil {
		httpx.Err(w, 500, "internal", "could not list folders")
		return
	}
	defer rs.Close()
	out := []map[string]any{}
	for rs.Next() {
		var id, name string
		var pid *string
		var created time.Time
		_ = rs.Scan(&id, &pid, &name, &created)
		out = append(out, map[string]any{"id": id, "parentId": pid, "name": name, "createdAt": created.UTC().Format(time.RFC3339)})
	}
	httpx.Data(w, 200, out)
}

type folderReq struct {
	Name     string `json:"name"`
	ParentID string `json:"parentId"`
}

func (d *Drive) CreateFolder(w http.ResponseWriter, r *http.Request) {
	var req folderReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if err := validate.ValidateFolderName(req.Name); err != nil {
		httpx.Err(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	org := auth.OrgID(r)
	if req.ParentID != "" {
		var ok bool
		err := d.Pool.QueryRow(r.Context(),
			`SELECT EXISTS(SELECT 1 FROM folders WHERE id=$1 AND org_id=$2 AND deleted_at IS NULL)`, req.ParentID, org).Scan(&ok)
		if err != nil || !ok {
			httpx.Err(w, http.StatusNotFound, "not_found", "parent folder not found")
			return
		}
	}
	var id string
	err := d.Pool.QueryRow(r.Context(),
		`INSERT INTO folders (org_id, parent_id, name, created_by) VALUES ($1,$2,$3,$4) RETURNING id`,
		org, nullIfEmpty(req.ParentID), req.Name, auth.UserID(r)).Scan(&id)
	if err != nil {
		httpx.Err(w, 500, "internal", "could not create folder")
		return
	}
	d.logActivity(r, "folder.create", "folder", id, `{"name":"`+req.Name+`"}`)
	httpx.Data(w, 201, map[string]any{"id": id, "name": req.Name, "parentId": nullIfEmpty(req.ParentID)})
}

func (d *Drive) folderOwned(r *http.Request, id string) bool {
	var ok bool
	_ = d.Pool.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM folders WHERE id=$1 AND org_id=$2)`, id, auth.OrgID(r)).Scan(&ok)
	return ok
}

func (d *Drive) UpdateFolder(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !d.folderOwned(r, id) {
		httpx.Err(w, http.StatusNotFound, "not_found", "folder not found")
		return
	}
	if !d.canWriteFolder(r, id) {
		httpx.Err(w, http.StatusForbidden, "forbidden", "no write access to this folder")
		return
	}
	var req folderReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	if req.Name != "" {
		req.Name = strings.TrimSpace(req.Name)
		if err := validate.ValidateFolderName(req.Name); err != nil {
			httpx.Err(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		if _, err := d.Pool.Exec(r.Context(), `UPDATE folders SET name=$1, updated_at=now() WHERE id=$2`, req.Name, id); err != nil {
			httpx.Err(w, 500, "internal", "could not rename folder")
			return
		}
	}
	if req.ParentID != "" {
		if req.ParentID == id {
			httpx.Err(w, http.StatusBadRequest, "bad_request", "folder cannot be its own parent")
			return
		}
		var ok bool
		_ = d.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM folders WHERE id=$1 AND org_id=$2 AND deleted_at IS NULL)`, req.ParentID, auth.OrgID(r)).Scan(&ok)
		if !ok {
			httpx.Err(w, http.StatusNotFound, "not_found", "destination folder not found")
			return
		}
		if _, err := d.Pool.Exec(r.Context(), `UPDATE folders SET parent_id=$1, updated_at=now() WHERE id=$2`, req.ParentID, id); err != nil {
			httpx.Err(w, 500, "internal", "could not move folder")
			return
		}
	}
	d.logActivity(r, "folder.update", "folder", id, "{}")
	httpx.Data(w, 200, map[string]any{"id": id, "ok": true})
}

func (d *Drive) TrashFolder(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !d.folderOwned(r, id) {
		httpx.Err(w, http.StatusNotFound, "not_found", "folder not found")
		return
	}
	if !d.canWriteFolder(r, id) {
		httpx.Err(w, http.StatusForbidden, "forbidden", "no write access to this folder")
		return
	}
	perm := r.URL.Query().Get("permanent") == "true"
	if perm {
		// delete contained file blobs first
		rs, _ := d.Pool.Query(r.Context(), `SELECT s3_key FROM files WHERE org_id=$1 AND folder_id=$2`, auth.OrgID(r), id)
		if rs != nil {
			var keys []string
			for rs.Next() {
				var k string
				_ = rs.Scan(&k)
				keys = append(keys, k)
			}
			rs.Close()
			for _, k := range keys {
				_ = d.Store.Remove(r.Context(), k)
			}
		}
		_, _ = d.Pool.Exec(r.Context(), `DELETE FROM files WHERE folder_id=$1`, id)
		_, _ = d.Pool.Exec(r.Context(), `DELETE FROM folders WHERE id=$1`, id)
		d.logActivity(r, "folder.delete.permanent", "folder", id, "{}")
	} else {
		_, _ = d.Pool.Exec(r.Context(), `UPDATE folders SET deleted_at=now() WHERE id=$1`, id)
		_, _ = d.Pool.Exec(r.Context(), `UPDATE files SET deleted_at=now() WHERE folder_id=$1`, id)
		d.logActivity(r, "folder.trash", "folder", id, "{}")
	}
	httpx.Data(w, 200, map[string]any{"id": id, "ok": true})
}

func (d *Drive) RestoreFolder(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !d.folderOwned(r, id) {
		httpx.Err(w, http.StatusNotFound, "not_found", "folder not found")
		return
	}
	if !d.canWriteFolder(r, id) {
		httpx.Err(w, http.StatusForbidden, "forbidden", "no write access to this folder")
		return
	}
	_, _ = d.Pool.Exec(r.Context(), `UPDATE folders SET deleted_at=NULL WHERE id=$1`, id)
	_, _ = d.Pool.Exec(r.Context(), `UPDATE files SET deleted_at=NULL WHERE folder_id=$1`, id)
	d.logActivity(r, "folder.restore", "folder", id, "{}")
	httpx.Data(w, 200, map[string]any{"id": id, "ok": true})
}

// ---------- files ----------

type fileRow struct {
	ID, FolderID, Name, Key, Mime, Created string
	Size                                   int64
}

func (d *Drive) ListFiles(w http.ResponseWriter, r *http.Request) {
	org := auth.OrgID(r)
	q := r.URL.Query()
	folder := q.Get("folderId")
	search := strings.ToLower(strings.TrimSpace(q.Get("q")))
	kind := strings.ToLower(strings.TrimSpace(q.Get("type")))
	trash := q.Get("trash") == "true"
	sql := `SELECT id, folder_id, display_name, s3_key, mime, size_bytes, created_at FROM files WHERE org_id=$1`
	args := []any{org}
	i := 2
	if trash {
		sql += ` AND deleted_at IS NOT NULL`
	} else {
		sql += ` AND deleted_at IS NULL`
	}
	if folder != "" {
		sql += ` AND folder_id=$` + itoa(i)
		args = append(args, folder)
		i++
	}
	if search != "" {
		sql += ` AND lower(display_name) LIKE $` + itoa(i)
		args = append(args, "%"+search+"%")
		i++
	}
	sql += ` ORDER BY created_at DESC LIMIT 200`
	rs, err := d.Pool.Query(r.Context(), sql, args...)
	if err != nil {
		httpx.Err(w, 500, "internal", "could not list files")
		return
	}
	defer rs.Close()
	out := []map[string]any{}
	for rs.Next() {
		var id, name, key, mime string
		var fid *string
		var size int64
		var created time.Time
		_ = rs.Scan(&id, &fid, &name, &key, &mime, &size, &created)
		k := validate.Kind(mime)
		if kind != "" && kind != "all" && k != kind {
			continue
		}
		out = append(out, map[string]any{"id": id, "folderId": fid, "name": name, "mime": mime, "kind": k, "sizeBytes": size, "createdAt": created.UTC().Format(time.RFC3339)})
	}
	httpx.Data(w, 200, out)
}

func itoa(i int) string {
	return strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(fmtsprint(i)), " ", ""))
}

func fmtsprint(i int) string {
	if i == 0 {
		return "0"
	}
	s := ""
	for i > 0 {
		s = string(rune('0'+i%10)) + s
		i /= 10
	}
	return s
}

type presignReq struct {
	FolderID    string `json:"folderId"`
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
	SizeBytes   int64  `json:"sizeBytes"`
}

func (d *Drive) Presign(w http.ResponseWriter, r *http.Request) {
	var req presignReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	req.Filename = strings.TrimSpace(req.Filename)
	if err := validate.ValidateFileName(req.Filename); err != nil {
		httpx.Err(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := validate.AllowedType(req.ContentType); err != nil {
		httpx.Err(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if req.SizeBytes <= 0 || req.SizeBytes > validate.MaxFileBytes {
		httpx.Err(w, http.StatusBadRequest, "bad_request", "size must be between 1 byte and 200 MiB")
		return
	}
	org := auth.OrgID(r)
	if req.FolderID != "" {
		var ok bool
		_ = d.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM folders WHERE id=$1 AND org_id=$2 AND deleted_at IS NULL)`, req.FolderID, org).Scan(&ok)
		if !ok {
			httpx.Err(w, http.StatusNotFound, "not_found", "folder not found")
			return
		}
		if !d.canWriteFolder(r, req.FolderID) {
			httpx.Err(w, http.StatusForbidden, "forbidden", "no write access to this folder")
			return
		}
	}
	if err := d.checkQuota(r, req.SizeBytes); err != nil {
		httpx.Err(w, http.StatusConflict, "quota_exceeded", err.Error())
		return
	}
	key := org + "/" + uuid.NewString() + "-" + validate.SanitizeForKey(req.Filename)
	url, err := d.Store.PresignPut(r.Context(), key)
	if err != nil {
		httpx.Err(w, 500, "internal", "could not create upload URL")
		return
	}
	httpx.Data(w, 200, map[string]any{"uploadUrl": url, "s3Key": key, "expiresIn": 900})
}

type completeReq struct {
	FolderID    string `json:"folderId"`
	Filename    string `json:"filename"`
	ContentType string `json:"contentType"`
	S3Key       string `json:"s3Key"`
	SizeBytes   int64  `json:"sizeBytes"`
}

func (d *Drive) Complete(w http.ResponseWriter, r *http.Request) {
	var req completeReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	req.Filename = strings.TrimSpace(req.Filename)
	if err := validate.ValidateFileName(req.Filename); err != nil {
		httpx.Err(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := validate.AllowedType(req.ContentType); err != nil {
		httpx.Err(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	org := auth.OrgID(r)
	// Anti path-traversal: key must be exactly "<org>/<uuid>-<safe>" with no dots-dots or slashes beyond prefix.
	if !validKeyForOrg(req.S3Key, org) {
		httpx.Err(w, http.StatusBadRequest, "bad_request", "invalid object key (possible path traversal)")
		return
	}
	size, _, err := d.Store.Stat(r.Context(), req.S3Key)
	if err != nil {
		httpx.Err(w, http.StatusBadRequest, "bad_request", "uploaded object not found in storage; upload first")
		return
	}
	if size <= 0 || size > validate.MaxFileBytes {
		_ = d.Store.Remove(r.Context(), req.S3Key)
		httpx.Err(w, http.StatusBadRequest, "bad_request", "object size out of bounds")
		return
	}
	if req.FolderID != "" {
		var ok bool
		_ = d.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM folders WHERE id=$1 AND org_id=$2 AND deleted_at IS NULL)`, req.FolderID, org).Scan(&ok)
		if !ok {
			httpx.Err(w, http.StatusNotFound, "not_found", "folder not found")
			return
		}
	}
	if err := d.checkQuota(r, size); err != nil {
		_ = d.Store.Remove(r.Context(), req.S3Key)
		httpx.Err(w, http.StatusConflict, "quota_exceeded", err.Error())
		return
	}
	var id string
	err = d.Pool.QueryRow(r.Context(),
		`INSERT INTO files (org_id, folder_id, display_name, s3_key, mime, size_bytes, created_by) VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
		org, nullIfEmpty(req.FolderID), req.Filename, req.S3Key, req.ContentType, size, auth.UserID(r)).Scan(&id)
	if err != nil {
		_ = d.Store.Remove(r.Context(), req.S3Key)
		httpx.Err(w, 500, "internal", "could not register file")
		return
	}
	d.logActivity(r, "file.upload", "file", id, `{"name":"`+req.Filename+`"}`)
	httpx.Data(w, 201, map[string]any{"id": id, "name": req.Filename, "sizeBytes": size, "mime": req.ContentType})
}

// validKeyForOrg rejects traversal: must start with org+"/", remainder has no "/" "\" or "..".
func validKeyForOrg(key, org string) bool {
	if !strings.HasPrefix(key, org+"/") {
		return false
	}
	rest := strings.TrimPrefix(key, org+"/")
	if rest == "" || strings.Contains(rest, "/") || strings.Contains(rest, "\\") || strings.Contains(rest, "..") || strings.Contains(rest, "\x00") {
		return false
	}
	return true
}

// Upload proxies a small multipart file through the API (up to 15 MiB).
func (d *Drive) Upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, validate.MaxProxyBytes+(1<<20))
	if err := r.ParseMultipartForm(validate.MaxProxyBytes + (1 << 20)); err != nil {
		httpx.Err(w, http.StatusBadRequest, "bad_request", "multipart body required (max ~16 MiB via this endpoint; use presigned URLs for large files)")
		return
	}
	folderID := strings.TrimSpace(r.FormValue("folderId"))
	name := strings.TrimSpace(r.FormValue("filename"))
	ctype := strings.TrimSpace(r.FormValue("contentType"))
	f, hdr, err := r.FormFile("file")
	if err != nil {
		httpx.Err(w, http.StatusBadRequest, "bad_request", "field 'file' is required")
		return
	}
	defer f.Close()
	if name == "" {
		name = hdr.Filename
	}
	if ctype == "" {
		ctype = hdr.Header.Get("Content-Type")
		if ctype == "" {
			ctype = "application/octet-stream"
		}
	}
	if err := validate.ValidateFileName(name); err != nil {
		httpx.Err(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if err := validate.AllowedType(ctype); err != nil {
		httpx.Err(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	org := auth.OrgID(r)
	if folderID != "" {
		var ok bool
		_ = d.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM folders WHERE id=$1 AND org_id=$2 AND deleted_at IS NULL)`, folderID, org).Scan(&ok)
		if !ok {
			httpx.Err(w, http.StatusNotFound, "not_found", "folder not found")
			return
		}
	}
	key := org + "/" + uuid.NewString() + "-" + validate.SanitizeForKey(name)
	if err := d.checkQuota(r, hdr.Size); err != nil {
		httpx.Err(w, http.StatusConflict, "quota_exceeded", err.Error())
		return
	}
	if err := d.Store.Put(r.Context(), key, ctype, f, hdr.Size); err != nil {
		httpx.Err(w, 500, "internal", "could not store file")
		return
	}
	var id string
	err = d.Pool.QueryRow(r.Context(),
		`INSERT INTO files (org_id, folder_id, display_name, s3_key, mime, size_bytes, created_by) VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING id`,
		org, nullIfEmpty(folderID), name, key, ctype, hdr.Size, auth.UserID(r)).Scan(&id)
	if err != nil {
		_ = d.Store.Remove(r.Context(), key)
		httpx.Err(w, 500, "internal", "could not register file")
		return
	}
	d.logActivity(r, "file.upload", "file", id, `{"name":"`+name+`"}`)
	httpx.Data(w, 201, map[string]any{"id": id, "name": name, "sizeBytes": hdr.Size, "mime": ctype})
}

func (d *Drive) fileRow(r *http.Request, id string) (fileRow, bool) {
	var fr fileRow
	var fid *string
	var created time.Time
	err := d.Pool.QueryRow(r.Context(),
		`SELECT id, folder_id, display_name, s3_key, mime, size_bytes, created_at FROM files WHERE id=$1 AND org_id=$2`,
		id, auth.OrgID(r)).Scan(&fr.ID, &fid, &fr.Name, &fr.Key, &fr.Mime, &fr.Size, &created)
	if err != nil {
		return fr, false
	}
	fr.Created = created.UTC().Format(time.RFC3339)
	if fid != nil {
		fr.FolderID = *fid
	}
	return fr, true
}

func (d *Drive) GetFile(w http.ResponseWriter, r *http.Request) {
	fr, ok := d.fileRow(r, chi.URLParam(r, "id"))
	if !ok {
		httpx.Err(w, http.StatusNotFound, "not_found", "file not found")
		return
	}
	if !d.canReadFile(r, fr.ID) {
		httpx.Err(w, http.StatusForbidden, "forbidden", "no access to this file")
		return
	}
	httpx.Data(w, 200, map[string]any{"id": fr.ID, "folderId": nullIfEmpty(fr.FolderID), "name": fr.Name, "mime": fr.Mime, "kind": validate.Kind(fr.Mime), "sizeBytes": fr.Size, "createdAt": fr.Created})
}

func (d *Drive) Download(w http.ResponseWriter, r *http.Request) {
	fr, ok := d.fileRow(r, chi.URLParam(r, "id"))
	if !ok {
		httpx.Err(w, http.StatusNotFound, "not_found", "file not found")
		return
	}
	if !d.canReadFile(r, fr.ID) {
		httpx.Err(w, http.StatusForbidden, "forbidden", "no access to this file")
		return
	}
	u, err := d.Store.PresignGet(r.Context(), fr.Key, false, fr.Name)
	if err != nil {
		httpx.Err(w, 500, "internal", "could not create download URL")
		return
	}
	d.logActivity(r, "file.download", "file", fr.ID, "{}")
	httpx.Data(w, 200, map[string]any{"url": u, "expiresIn": 300, "filename": fr.Name})
}

func (d *Drive) Preview(w http.ResponseWriter, r *http.Request) {
	fr, ok := d.fileRow(r, chi.URLParam(r, "id"))
	if !ok {
		httpx.Err(w, http.StatusNotFound, "not_found", "file not found")
		return
	}
	if !d.canReadFile(r, fr.ID) {
		httpx.Err(w, http.StatusForbidden, "forbidden", "no access to this file")
		return
	}
	k := validate.Kind(fr.Mime)
	if k != "image" && k != "pdf" && k != "doc" && fr.Mime != "text/plain" {
		httpx.Err(w, http.StatusBadRequest, "bad_request", "preview not available for this file type")
		return
	}
	u, err := d.Store.PresignGet(r.Context(), fr.Key, true, fr.Name)
	if err != nil {
		httpx.Err(w, 500, "internal", "could not create preview URL")
		return
	}
	httpx.Data(w, 200, map[string]any{"url": u, "expiresIn": 300, "mime": fr.Mime})
}

type fileUpdateReq struct {
	Name     string  `json:"name"`
	FolderID *string `json:"folderId"`
}

func (d *Drive) UpdateFile(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	fr, ok := d.fileRow(r, id)
	if !ok {
		httpx.Err(w, http.StatusNotFound, "not_found", "file not found")
		return
	}
	if !d.canWriteFile(r, fr.ID) {
		httpx.Err(w, http.StatusForbidden, "forbidden", "no write access to this file")
		return
	}
	var req fileUpdateReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	if req.Name != "" {
		req.Name = strings.TrimSpace(req.Name)
		if err := validate.ValidateFileName(req.Name); err != nil {
			httpx.Err(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		if _, err := d.Pool.Exec(r.Context(), `UPDATE files SET display_name=$1, updated_at=now() WHERE id=$2`, req.Name, id); err != nil {
			httpx.Err(w, 500, "internal", "could not rename file")
			return
		}
	}
	if req.FolderID != nil {
		if *req.FolderID == "" {
			_, _ = d.Pool.Exec(r.Context(), `UPDATE files SET folder_id=NULL, updated_at=now() WHERE id=$1`, id)
		} else {
			var ok bool
			_ = d.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM folders WHERE id=$1 AND org_id=$2 AND deleted_at IS NULL)`, *req.FolderID, auth.OrgID(r)).Scan(&ok)
			if !ok {
				httpx.Err(w, http.StatusNotFound, "not_found", "destination folder not found")
				return
			}
			_, _ = d.Pool.Exec(r.Context(), `UPDATE files SET folder_id=$1, updated_at=now() WHERE id=$2`, *req.FolderID, id)
		}
	}
	_ = fr
	d.logActivity(r, "file.update", "file", id, "{}")
	httpx.Data(w, 200, map[string]any{"id": id, "ok": true})
}

func (d *Drive) TrashFile(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	fr, ok := d.fileRow(r, id)
	if !ok {
		httpx.Err(w, http.StatusNotFound, "not_found", "file not found")
		return
	}
	if !d.canWriteFile(r, fr.ID) {
		httpx.Err(w, http.StatusForbidden, "forbidden", "no write access to this file")
		return
	}
	if r.URL.Query().Get("permanent") == "true" {
		_ = d.Store.Remove(r.Context(), fr.Key)
		_, _ = d.Pool.Exec(r.Context(), `DELETE FROM files WHERE id=$1`, id)
		d.logActivity(r, "file.delete.permanent", "file", id, "{}")
	} else {
		_, _ = d.Pool.Exec(r.Context(), `UPDATE files SET deleted_at=now() WHERE id=$1`, id)
		d.logActivity(r, "file.trash", "file", id, "{}")
	}
	httpx.Data(w, 200, map[string]any{"id": id, "ok": true})
}

func (d *Drive) RestoreFile(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	fr, ok := d.fileRow(r, id)
	if !ok {
		httpx.Err(w, http.StatusNotFound, "not_found", "file not found")
		return
	}
	if !d.canWriteFile(r, fr.ID) {
		httpx.Err(w, http.StatusForbidden, "forbidden", "no write access to this file")
		return
	}
	_, _ = d.Pool.Exec(r.Context(), `UPDATE files SET deleted_at=NULL WHERE id=$1`, id)
	d.logActivity(r, "file.restore", "file", id, "{}")
	httpx.Data(w, 200, map[string]any{"id": id, "ok": true})
}

func (d *Drive) checkQuota(r *http.Request, extra int64) error {
	var quota, used int64
	_ = d.Pool.QueryRow(r.Context(), `SELECT quota_bytes FROM orgs WHERE id=$1`, auth.OrgID(r)).Scan(&quota)
	_ = d.Pool.QueryRow(r.Context(), `SELECT COALESCE(SUM(size_bytes),0) FROM files WHERE org_id=$1 AND deleted_at IS NULL`, auth.OrgID(r)).Scan(&used)
	if used+extra > quota {
		return errQuota(quota, used)
	}
	return nil
}

type quotaErr struct{ quota, used int64 }

func (e *quotaErr) Error() string {
	return "storage quota exceeded (used " + fmtsprint64(e.used) + " of " + fmtsprint64(e.quota) + " bytes)"
}

func fmtsprint64(i int64) string {
	if i == 0 {
		return "0"
	}
	s := ""
	for i > 0 {
		s = string(rune('0'+i%10)) + s
		i /= 10
	}
	return s
}
func errQuota(quota, used int64) error { return &quotaErr{quota, used} }

var _ = time.Now
