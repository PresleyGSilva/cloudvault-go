package auth

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/PresleyGSilva/cloudvault-go/internal/httpx"
)

type ctxKey string

const (
	KeyUserID ctxKey = "user_id"
	KeyEmail  ctxKey = "email"
	KeyOrgID  ctxKey = "org_id"
	KeyRole   ctxKey = "role"
)

func HashPassword(pw string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(h), err
}

func CheckPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

func Sign(secret, userID, email string, ttl time.Duration, typ string) (string, error) {
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":   userID,
		"email": email,
		"typ":   typ,
		"exp":   time.Now().Add(ttl).Unix(),
		"iat":   time.Now().Unix(),
	})
	return t.SignedString([]byte(secret))
}

func SignAccess(secret, userID, email string) (string, error) {
	return Sign(secret, userID, email, 15*time.Minute, "access")
}

func SignRefresh(secret, userID, email string) (string, error) {
	return Sign(secret, userID, email, 7*24*time.Hour, "refresh")
}

func parse(secret, token, wantTyp string) (userID, email string, err error) {
	t, err := jwt.Parse(token, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, jwt.ErrSignatureInvalid
		}
		return []byte(secret), nil
	})
	if err != nil || !t.Valid {
		return "", "", jwt.ErrTokenInvalidClaims
	}
	c, ok := t.Claims.(jwt.MapClaims)
	if !ok || c["typ"] != wantTyp {
		return "", "", jwt.ErrTokenInvalidClaims
	}
	sub, _ := c["sub"].(string)
	mail, _ := c["email"].(string)
	if sub == "" {
		return "", "", jwt.ErrTokenInvalidClaims
	}
	return sub, mail, nil
}

// RequireAuth validates the Bearer access token.
func RequireAuth(secret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := r.Header.Get("Authorization")
			tok := strings.TrimPrefix(h, "Bearer ")
			if h == "" || tok == h {
				httpx.Err(w, http.StatusUnauthorized, "unauthorized", "missing bearer token")
				return
			}
			uid, mail, err := parse(secret, tok, "access")
			if err != nil {
				httpx.Err(w, http.StatusUnauthorized, "unauthorized", "invalid or expired token")
				return
			}
			ctx := context.WithValue(r.Context(), KeyUserID, uid)
			ctx = context.WithValue(ctx, KeyEmail, mail)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireOrg resolves X-Org-ID and verifies membership (tenant isolation).
func RequireOrg(pool *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			orgID := r.Header.Get("X-Org-ID")
			if orgID == "" {
				httpx.Err(w, http.StatusBadRequest, "missing_org", "X-Org-ID header is required")
				return
			}
			uid, _ := r.Context().Value(KeyUserID).(string)
			var role string
			err := pool.QueryRow(r.Context(),
				`SELECT role FROM org_members WHERE org_id=$1 AND user_id=$2`, orgID, uid).Scan(&role)
			if err != nil {
				httpx.Err(w, http.StatusForbidden, "forbidden", "not a member of this organization")
				return
			}
			ctx := context.WithValue(r.Context(), KeyOrgID, orgID)
			ctx = context.WithValue(ctx, KeyRole, role)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func UserID(r *http.Request) string { v, _ := r.Context().Value(KeyUserID).(string); return v }
func OrgID(r *http.Request) string  { v, _ := r.Context().Value(KeyOrgID).(string); return v }
func Role(r *http.Request) string   { v, _ := r.Context().Value(KeyRole).(string); return v }

func IsPrivileged(role string) bool { return role == "owner" || role == "admin" }

// ParseRefresh validates a refresh token and returns the subject.
func ParseRefresh(secret, token string) (string, string, error) {
	return parse(secret, token, "refresh")
}
