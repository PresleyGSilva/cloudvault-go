package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/PresleyGSilva/cloudvault-go/internal/auth"
)

// Seed creates a fictional demo workspace (folders + activities, no blobs).
func Seed(ctx context.Context, pool *pgxpool.Pool) error {
	var exists bool
	err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE email='demo@cloudvault.local')`).Scan(&exists)
	if err != nil || exists {
		return err
	}
	hash, _ := auth.HashPassword("Demo1234!")
	var userID, orgID string
	err = pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, name) VALUES ('demo@cloudvault.local',$1,'Demo User') RETURNING id`,
		hash).Scan(&userID)
	if err != nil {
		return err
	}
	err = pool.QueryRow(ctx,
		`INSERT INTO orgs (name) VALUES ('Acme Demo') RETURNING id`).Scan(&orgID)
	if err != nil {
		return err
	}
	if _, err = pool.Exec(ctx, `INSERT INTO org_members (org_id, user_id, role) VALUES ($1,$2,'owner')`, orgID, userID); err != nil {
		return err
	}
	for _, f := range []string{"Projects", "Design Assets", "Invoices"} {
		var fid string
		if err := pool.QueryRow(ctx,
			`INSERT INTO folders (org_id, name, created_by) VALUES ($1,$2,$3) RETURNING id`,
			orgID, f, userID).Scan(&fid); err != nil {
			return err
		}
		_, _ = pool.Exec(ctx,
			`INSERT INTO activities (org_id, user_id, action, entity_type, entity_id, meta) VALUES ($1,$2,'folder.create','folder',$3,$4)`,
			orgID, userID, fid, `{"name":"`+f+`"}`)
	}
	_, _ = pool.Exec(ctx,
		`INSERT INTO activities (org_id, user_id, action, entity_type, entity_id, meta) VALUES ($1,$2,'org.seed','org',$1,'{}')`,
		orgID, userID)
	return nil
}
