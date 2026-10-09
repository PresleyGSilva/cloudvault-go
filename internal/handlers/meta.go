package handlers

import (
	"net/http"
	"time"

	"github.com/PresleyGSilva/cloudvault-go/internal/auth"
	"github.com/PresleyGSilva/cloudvault-go/internal/httpx"
)

func (d *Drive) Activities(w http.ResponseWriter, r *http.Request) {
	rs, err := d.Pool.Query(r.Context(),
		`SELECT a.action, a.entity_type, a.entity_id, a.meta, a.created_at, COALESCE(u.email,'') FROM activities a LEFT JOIN users u ON u.id=a.user_id WHERE a.org_id=$1 ORDER BY a.created_at DESC LIMIT 100`,
		auth.OrgID(r))
	if err != nil {
		httpx.Err(w, 500, "internal", "could not list activities")
		return
	}
	defer rs.Close()
	out := []map[string]any{}
	for rs.Next() {
		var action, etype, eid, email string
		var created time.Time
		var meta []byte
		_ = rs.Scan(&action, &etype, &eid, &meta, &created, &email)
		out = append(out, map[string]any{"action": action, "entityType": etype, "entityId": eid, "meta": string(meta), "actor": email, "createdAt": created.UTC().Format(time.RFC3339)})
	}
	httpx.Data(w, 200, out)
}
func (d *Drive) Members(w http.ResponseWriter, r *http.Request) {
	rs, err := d.Pool.Query(r.Context(),
		`SELECT u.id, u.name, u.email, m.role FROM org_members m JOIN users u ON u.id=m.user_id WHERE m.org_id=$1 ORDER BY u.name`,
		auth.OrgID(r))
	if err != nil {
		httpx.Err(w, 500, "internal", "could not list members")
		return
	}
	defer rs.Close()
	out := []map[string]any{}
	for rs.Next() {
		var id, name, email, role string
		_ = rs.Scan(&id, &name, &email, &role)
		out = append(out, map[string]any{"id": id, "name": name, "email": email, "role": role})
	}
	httpx.Data(w, 200, out)
}

func (d *Drive) Quota(w http.ResponseWriter, r *http.Request) {
	var quota, used, files, folders int64
	_ = d.Pool.QueryRow(r.Context(), `SELECT quota_bytes FROM orgs WHERE id=$1`, auth.OrgID(r)).Scan(&quota)
	_ = d.Pool.QueryRow(r.Context(), `SELECT COALESCE(SUM(size_bytes),0), COUNT(*) FROM files WHERE org_id=$1 AND deleted_at IS NULL`, auth.OrgID(r)).Scan(&used, &files)
	_ = d.Pool.QueryRow(r.Context(), `SELECT COUNT(*) FROM folders WHERE org_id=$1 AND deleted_at IS NULL`, auth.OrgID(r)).Scan(&folders)
	pct := 0.0
	if quota > 0 {
		pct = float64(used) / float64(quota) * 100
	}
	httpx.Data(w, 200, map[string]any{
		"usedBytes": quota * 0, "used": used, "quota": quota,
		"usedBytesTotal": used, "quotaBytes": quota, "percent": pct,
		"files": files, "folders": folders,
	})
}
