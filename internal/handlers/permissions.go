package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/PresleyGSilva/cloudvault-go/internal/auth"
	"github.com/PresleyGSilva/cloudvault-go/internal/httpx"
)

// Authorization model:
// - Cross-org access is impossible (org_id scoping on every query).
// - owner/admin can read/write everything in the org.
// - creators can read/write their own files/folders.
// - other members: full access UNLESS explicit permission rows exist on the
//   object; then a matching row (viewer for read, editor for write) is required.
// - folder permissions inherit to contained files for read.

func (d *Drive) creatorOfFile(r *http.Request, fileID string) (string, string) {
	var creator, folder *string
	var c, f string
	_ = d.Pool.QueryRow(r.Context(), `SELECT created_by, folder_id FROM files WHERE id=$1`, fileID).Scan(&creator, &folder)
	if creator != nil {
		c = *creator
	}
	if folder != nil {
		f = *folder
	}
	return c, f
}

func (d *Drive) hasFileGrant(r *http.Request, fileID, user string, levels ...string) bool {
	for _, lv := range levels {
		var ok bool
		_ = d.Pool.QueryRow(r.Context(),
			`SELECT EXISTS(SELECT 1 FROM file_permissions WHERE file_id=$1 AND grantee_user_id=$2 AND level=$3)`,
			fileID, user, lv).Scan(&ok)
		if ok {
			return true
		}
	}
	return false
}

func (d *Drive) fileRestricted(r *http.Request, fileID string) bool {
	var n int
	_ = d.Pool.QueryRow(r.Context(), `SELECT COUNT(*) FROM file_permissions WHERE file_id=$1`, fileID).Scan(&n)
	return n > 0
}

func (d *Drive) hasFolderGrant(r *http.Request, folderID, user string, levels ...string) bool {
	for _, lv := range levels {
		var ok bool
		_ = d.Pool.QueryRow(r.Context(),
			`SELECT EXISTS(SELECT 1 FROM file_permissions WHERE folder_id=$1 AND grantee_user_id=$2 AND level=$3)`,
			folderID, user, lv).Scan(&ok)
		if ok {
			return true
		}
	}
	return false
}

func (d *Drive) folderRestricted(r *http.Request, folderID string) bool {
	var n int
	_ = d.Pool.QueryRow(r.Context(), `SELECT COUNT(*) FROM file_permissions WHERE folder_id=$1`, folderID).Scan(&n)
	return n > 0
}

func (d *Drive) canReadFile(r *http.Request, fileID string) bool {
	uid := auth.UserID(r)
	if auth.IsPrivileged(auth.Role(r)) {
		return true
	}
	creator, folder := d.creatorOfFile(r, fileID)
	if creator == uid {
		return true
	}
	if d.hasFileGrant(r, fileID, uid, "viewer", "editor") {
		return true
	}
	if folder != "" && d.hasFolderGrant(r, folder, uid, "viewer", "editor") {
		return true
	}
	return !d.fileRestricted(r, fileID)
}

func (d *Drive) canWriteFile(r *http.Request, fileID string) bool {
	uid := auth.UserID(r)
	if auth.IsPrivileged(auth.Role(r)) {
		return true
	}
	creator, _ := d.creatorOfFile(r, fileID)
	if creator == uid {
		return true
	}
	return d.hasFileGrant(r, fileID, uid, "editor")
}

func (d *Drive) canWriteFolder(r *http.Request, folderID string) bool {
	uid := auth.UserID(r)
	if auth.IsPrivileged(auth.Role(r)) {
		return true
	}
	var creator *string
	_ = d.Pool.QueryRow(r.Context(), `SELECT created_by FROM folders WHERE id=$1`, folderID).Scan(&creator)
	if creator != nil && *creator == uid {
		return true
	}
	return d.hasFolderGrant(r, folderID, uid, "editor")
}

// ---------- permission endpoints ----------

type permReq struct {
	UserID string `json:"userId"`
	Level  string `json:"level"`
}

func (d *Drive) FilePermissions(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, ok := d.fileRow(r, id); !ok {
		httpx.Err(w, http.StatusNotFound, "not_found", "file not found")
		return
	}
	if !auth.IsPrivileged(auth.Role(r)) {
		httpx.Err(w, http.StatusForbidden, "forbidden", "only owner/admin can manage permissions")
		return
	}
	rs, _ := d.Pool.Query(r.Context(),
		`SELECT p.grantee_user_id, u.email, p.level FROM file_permissions p JOIN users u ON u.id=p.grantee_user_id WHERE p.file_id=$1`, id)
	out := []map[string]any{}
	if rs != nil {
		defer rs.Close()
		for rs.Next() {
			var uid, email, lv string
			_ = rs.Scan(&uid, &email, &lv)
			out = append(out, map[string]any{"userId": uid, "email": email, "level": lv})
		}
	}
	httpx.Data(w, 200, out)
}

func (d *Drive) SetFilePermission(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, ok := d.fileRow(r, id); !ok {
		httpx.Err(w, http.StatusNotFound, "not_found", "file not found")
		return
	}
	if !auth.IsPrivileged(auth.Role(r)) {
		httpx.Err(w, http.StatusForbidden, "forbidden", "only owner/admin can manage permissions")
		return
	}
	var req permReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	if req.Level != "viewer" && req.Level != "editor" {
		httpx.Err(w, http.StatusBadRequest, "bad_request", "level must be viewer or editor")
		return
	}
	var mem bool
	_ = d.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM org_members WHERE org_id=$1 AND user_id=$2)`, auth.OrgID(r), req.UserID).Scan(&mem)
	if !mem {
		httpx.Err(w, http.StatusBadRequest, "bad_request", "grantee must belong to the organization")
		return
	}
	_, err := d.Pool.Exec(r.Context(),
		`INSERT INTO file_permissions (org_id, file_id, grantee_user_id, level) VALUES ($1,$2,$3,$4)
		 ON CONFLICT (file_id, grantee_user_id) WHERE file_id IS NOT NULL DO UPDATE SET level=$4`,
		auth.OrgID(r), id, req.UserID, req.Level)
	if err != nil {
		httpx.Err(w, 500, "internal", "could not grant permission")
		return
	}
	d.logActivity(r, "file.permission", "file", id, `{"level":"`+req.Level+`"}`)
	httpx.Data(w, 200, map[string]any{"ok": true})
}

func (d *Drive) FolderPermissions(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !d.folderOwned(r, id) {
		httpx.Err(w, http.StatusNotFound, "not_found", "folder not found")
		return
	}
	if !auth.IsPrivileged(auth.Role(r)) {
		httpx.Err(w, http.StatusForbidden, "forbidden", "only owner/admin can manage permissions")
		return
	}
	rs, _ := d.Pool.Query(r.Context(),
		`SELECT p.grantee_user_id, u.email, p.level FROM file_permissions p JOIN users u ON u.id=p.grantee_user_id WHERE p.folder_id=$1`, id)
	out := []map[string]any{}
	if rs != nil {
		defer rs.Close()
		for rs.Next() {
			var uid, email, lv string
			_ = rs.Scan(&uid, &email, &lv)
			out = append(out, map[string]any{"userId": uid, "email": email, "level": lv})
		}
	}
	httpx.Data(w, 200, out)
}

func (d *Drive) SetFolderPermission(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if !d.folderOwned(r, id) {
		httpx.Err(w, http.StatusNotFound, "not_found", "folder not found")
		return
	}
	if !auth.IsPrivileged(auth.Role(r)) {
		httpx.Err(w, http.StatusForbidden, "forbidden", "only owner/admin can manage permissions")
		return
	}
	var req permReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	if req.Level != "viewer" && req.Level != "editor" {
		httpx.Err(w, http.StatusBadRequest, "bad_request", "level must be viewer or editor")
		return
	}
	var mem bool
	_ = d.Pool.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM org_members WHERE org_id=$1 AND user_id=$2)`, auth.OrgID(r), req.UserID).Scan(&mem)
	if !mem {
		httpx.Err(w, http.StatusBadRequest, "bad_request", "grantee must belong to the organization")
		return
	}
	_, err := d.Pool.Exec(r.Context(),
		`INSERT INTO file_permissions (org_id, folder_id, grantee_user_id, level) VALUES ($1,$2,$3,$4)
		 ON CONFLICT (folder_id, grantee_user_id) WHERE folder_id IS NOT NULL DO UPDATE SET level=$4`,
		auth.OrgID(r), id, req.UserID, req.Level)
	if err != nil {
		httpx.Err(w, 500, "internal", "could not grant permission")
		return
	}
	d.logActivity(r, "folder.permission", "folder", id, `{"level":"`+req.Level+`"}`)
	httpx.Data(w, 200, map[string]any{"ok": true})
}
