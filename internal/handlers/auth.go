package handlers

import (
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/PresleyGSilva/cloudvault-go/internal/auth"
	"github.com/PresleyGSilva/cloudvault-go/internal/httpx"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Handler struct {
	Pool      *pgxpool.Pool
	Store     interface{}
	JWTSecret string
}

func New(pool *pgxpool.Pool, secret string) *Handler {
	return &Handler{Pool: pool, JWTSecret: secret}
}

type registerReq struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
	OrgName  string `json:"orgName"`
}

type loginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var req registerReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.Name = strings.TrimSpace(req.Name)
	req.OrgName = strings.TrimSpace(req.OrgName)
	if req.Name == "" || req.Email == "" || len(req.Password) < 8 {
		httpx.Err(w, http.StatusBadRequest, "bad_request", "name, valid email and password (min 8 chars) are required")
		return
	}
	if !strings.Contains(req.Email, "@") {
		httpx.Err(w, http.StatusBadRequest, "bad_request", "invalid email")
		return
	}
	if req.OrgName == "" {
		req.OrgName = req.Name + "'s workspace"
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		httpx.Err(w, http.StatusInternalServerError, "internal", "could not create user")
		return
	}
	ctx := r.Context()
	var userID string
	err = h.Pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, name) VALUES ($1,$2,$3) RETURNING id`,
		req.Email, hash, req.Name).Scan(&userID)
	if err != nil {
		if strings.Contains(err.Error(), "users_email_unique") || strings.Contains(err.Error(), "duplicate") {
			httpx.Err(w, http.StatusConflict, "conflict", "email already registered")
			return
		}
		httpx.Err(w, http.StatusInternalServerError, "internal", "could not create user")
		return
	}
	var orgID string
	if err := h.Pool.QueryRow(ctx, `INSERT INTO orgs (name) VALUES ($1) RETURNING id`, req.OrgName).Scan(&orgID); err != nil {
		httpx.Err(w, http.StatusInternalServerError, "internal", "could not create workspace")
		return
	}
	_, _ = h.Pool.Exec(ctx, `INSERT INTO org_members (org_id, user_id, role) VALUES ($1,$2,'owner')`, orgID, userID)
	h.respondSession(w, r, userID, req.Name, req.Email)
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if !httpx.Decode(w, r, &req) {
		return
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	var id, hash, name, email string
	err := h.Pool.QueryRow(r.Context(),
		`SELECT id, password_hash, name, email FROM users WHERE lower(email)=$1`, req.Email).Scan(&id, &hash, &name, &email)
	if err != nil || !auth.CheckPassword(hash, req.Password) {
		httpx.Err(w, http.StatusUnauthorized, "unauthorized", "invalid credentials")
		return
	}
	h.respondSession(w, r, id, name, email)
}

func (h *Handler) respondSession(w http.ResponseWriter, r *http.Request, userID, name, email string) {
	rows, _ := h.Pool.Query(r.Context(),
		`SELECT m.org_id, o.name, m.role FROM org_members m JOIN orgs o ON o.id=m.org_id WHERE m.user_id=$1 ORDER BY o.created_at`,
		userID)
	orgs := []map[string]any{}
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var oid, oname, role string
			_ = rows.Scan(&oid, &oname, &role)
			orgs = append(orgs, map[string]any{"id": oid, "name": oname, "role": role})
		}
	}
	access, _ := auth.SignAccess(h.JWTSecret, userID, email)
	refresh, _ := auth.SignRefresh(h.JWTSecret, userID, email)
	http.SetCookie(w, &http.Cookie{
		Name: "refresh_token", Value: refresh, Path: "/api/v1/auth",
		MaxAge: 7 * 24 * 3600, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	httpx.Data(w, http.StatusOK, map[string]any{
		"user":        map[string]any{"id": userID, "name": name, "email": email},
		"accessToken": access,
		"orgs":        orgs,
	})
}

func (h *Handler) Refresh(w http.ResponseWriter, r *http.Request) {
	c, err := r.Cookie("refresh_token")
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "unauthorized", "missing refresh token")
		return
	}
	tok := c.Value
	// parse via sign helper: re-validate manually
	uid, mail, err := auth.ParseRefresh(h.JWTSecret, tok)
	if err != nil {
		httpx.Err(w, http.StatusUnauthorized, "unauthorized", "invalid refresh token")
		return
	}
	access, _ := auth.SignAccess(h.JWTSecret, uid, mail)
	httpx.Data(w, http.StatusOK, map[string]any{"accessToken": access})
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "refresh_token", Value: "", Path: "/api/v1/auth", MaxAge: -1, HttpOnly: true})
	httpx.Data(w, http.StatusOK, map[string]any{"ok": true})
}

func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	uid := auth.UserID(r)
	var name, email string
	if err := h.Pool.QueryRow(r.Context(), `SELECT name, email FROM users WHERE id=$1`, uid).Scan(&name, &email); err != nil {
		httpx.Err(w, http.StatusNotFound, "not_found", "user not found")
		return
	}
	rows, _ := h.Pool.Query(r.Context(),
		`SELECT m.org_id, o.name, m.role FROM org_members m JOIN orgs o ON o.id=m.org_id WHERE m.user_id=$1`, uid)
	orgs := []map[string]any{}
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var oid, oname, role string
			_ = rows.Scan(&oid, &oname, &role)
			orgs = append(orgs, map[string]any{"id": oid, "name": oname, "role": role})
		}
	}
	httpx.Data(w, http.StatusOK, map[string]any{
		"user": map[string]any{"id": uid, "name": name, "email": email},
		"orgs": orgs,
	})
}

var _ = uuid.NewString
var _ = time.Now
