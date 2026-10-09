# CloudVault — Architecture

## Overview
CloudVault is a secure multi-tenant file vault: Go monolith (Chi + pgx) backed by
Postgres 16 for metadata and MinIO (S3-compatible) for blobs, plus a Next.js 15
frontend.

```
browser ──> web (Next.js :3007) ──> api (:8087) ──┬──> postgres (:5440)
                                                  └──> minio (:9000, S3 API)
browser ──(presigned PUT/GET, 5–15 min TTL)───────> minio directly
```

## Backend layout
- `cmd/api/main.go` — wiring, CORS, graceful shutdown, healthcheck.
- `internal/config` — env-based config.
- `internal/db` — pgx pool, versioned SQL migrations, fictional seed.
- `internal/auth` — bcrypt passwords, JWT access (15 min) + refresh (7 d,
  httpOnly cookie), `RequireAuth` / `RequireOrg` middlewares (tenant isolation
  via `X-Org-ID`).
- `internal/validate` — anti path-traversal names, MIME allowlist, size caps
  (200 MiB presigned, 15 MiB proxy), `Kind()` buckets for filters.
- `internal/storage` — MinIO wrapper: short-lived presigned PUT (15 min) /
  GET (5 min), stat/remove/put helpers, bucket bootstrap.
- `internal/handlers` — auth, folders, files, shares, permissions, members,
  activities, quota.
- `internal/httpx` — `{data}` / `{error:{code,message}}` envelope.

## Data model (Postgres)
orgs → org_members ↔ users; folders (self-parent, soft delete); files
(org, folder, display_name, s3_key UNIQUE, mime, size_bytes, soft delete);
file_permissions (file|folder × user, viewer/editor, partial unique indexes);
shares (token UNIQUE, expires_at ≤ 7 d); activities (append-only feed).

Indexes on all FKs, `lower(display_name)` search, partial trash indexes.

## Upload flows
1. **Large/multipart**: `POST /files/presign` → PUT bytes straight to MinIO →
   `POST /files/complete` (server stats the object, validates key prefix
   `<org>/`, enforces quota, inserts row).
2. **Small (≤15 MiB)**: `POST /files/upload` multipart proxied through the API.

Object keys are server-generated (`<org>/<uuid>-<sanitized>`); clients can never
choose paths — this plus strict name validation is the path-traversal defense.

## Authorization
Every query is scoped by `org_id` (cross-org → 403/404). Within an org:
owner/admin bypass; creators own their objects; when permission rows exist on an
object, non-creators need a matching grant (viewer=read, editor=write); folder
viewer/editor grants inherit to contained files for reads.

## Shares
Bearer-free `GET /s/:token` + `/download`; expiry enforced in SQL time
comparison → `410 Gone` past `expires_at` (max 168 h at creation).

## Quota
`orgs.quota_bytes` (default 5 GiB) vs `SUM(files.size_bytes)` of live files;
checked on presign/complete/upload; surfaced at `GET /quota` with a progress bar.

## Frontend
Next.js 15 + TS + Tailwind (`brand` cyan `#0891B2` + slate). Login persists
`cv_token` + `cv_orgs`, auto-selects `orgs[0].id`; all tenant calls send
`X-Org-ID`/`X-User`. Drive page: breadcrumb folders, search + kind filter,
trash toggle, small/large uploads, image/PDF preview modal, expiring share
links, per-file/folder grants, quota bar, activity feed.

## API contract
OpenAPI at `openapi.yaml` and live `GET /openapi.yaml`. All JSON responses are
`{data: …}` and errors `{error:{code,message}}`.
