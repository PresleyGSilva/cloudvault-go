# CloudVault

Cofre de arquivos multi-tenant seguro — monólito **Go (Chi + pgx)**, metadados em
**Postgres 16**, objetos em storage compatível com **S3 (MinIO)** e frontend
**Next.js 15 + Tailwind**.

![Login](docs/screenshots/01-login.png)
![Drive](docs/screenshots/02-drive.png)

## Funcionalidades

- Auth com JWT de acesso (15 min) + refresh em cookie httpOnly; o login retorna
  `user`, `accessToken` e `orgs[]` (o frontend persiste `orgs[0].id`).
- Pastas (criar / renomear / mover / lixeira / restaurar / excluir permanente).
- Upload duplo: arquivos pequenos via multipart (≤ 15 MiB) e grandes via
  **URLs pré-assinadas PUT de curta duração** (15 min) + `POST /files/complete`.
- Download/preview via **URLs pré-assinadas GET de curta duração** (5 min); preview
  inline de imagens e PDFs.
- Lixeira + restauração, busca por nome, filtros por tipo
  (`image|video|audio|pdf|doc|other`).
- Links públicos com expiração (`GET /s/:token`, máx. 168 h, `410 Gone` após expirar).
- Permissões por arquivo/pasta (`viewer|editor`) com herança de leitura da pasta.
- Feed de atividades, quota por org (padrão 5 GiB) com barra de progresso.
- Segurança dura: chaves de objeto geradas no servidor (`<org>/<uuid>-<seguro>`),
  validação anti path-traversal, autorização por objeto, allowlist de MIME, limites
  de tamanho, SQL 100% parametrizado, senhas em bcrypt.

![Upload](docs/screenshots/03-drive-upload.png)
![Preview](docs/screenshots/04-preview.png)
![Link compartilhado](docs/screenshots/05-share.png)

## Stack e portas

| Serviço | Tech | Porta host (override via env) |
|---|---|---|
| API | Go 1.27, Chi, pgx, cliente MinIO | `8087` (`API_PORT`) |
| Web | Next.js 15, TS, Tailwind | `3007` (`WEB_PORT`) |
| DB | Postgres 16 | `5440` (`DB_PORT`) |
| Storage | MinIO (API S3) | `9000` (`MINIO_PORT`), console `9001` |

## Início rápido

```bash
cp .env.example .env   # ajuste portas/secrets se precisar
docker compose up --build
```

- Web: http://localhost:3007 · API: http://localhost:8087/healthz
- OpenAPI: `openapi.yaml` e ao vivo em `GET /openapi.yaml`
- Login demo (seed): `demo@cloudvault.local` / `Demo1234!`

Backend local (precisa de Postgres + endpoint S3):

```bash
go run ./cmd/api   # PORT, DATABASE_URL, JWT_SECRET, MINIO_ENDPOINT, ...
```

Frontend local:

```bash
cd web && npm install && npm run dev   # NEXT_PUBLIC_API_URL=http://localhost:8087
```

> **Nota sobre storage.** O `docker-compose.yml` provisiona **MinIO** de verdade. Em
> redes restritas onde a imagem do MinIO não pode ser baixada, qualquer endpoint
> compatível com S3 funciona: basta `MINIO_ENDPOINT` (ex. S3Mock em
> `localhost:9090`) — o fluxo pré-assinado de up/down é idêntico sobre a API S3.
> Este repositório foi validado de ponta a ponta (upload → download byte-idêntico,
> shares, lixeira, permissões) contra backend compatível com S3 + testes de integração.

## Resumo da API

Todas as rotas de tenant exigem `Authorization: Bearer <access>` + header `X-Org-ID`;
respostas são `{data: …}`, erros `{error: {code, message}}`.

```
POST /api/v1/auth/register|login|refresh|logout
GET  /api/v1/me
GET|POST /api/v1/folders  PATCH|DELETE /api/v1/folders/{id}  POST /.../restore
GET|PUT  /api/v1/folders/{id}/permissions   POST /api/v1/folders/{id}/shares
GET  /api/v1/files?q=&type=&folderId=&trash=
POST /api/v1/files/presign  POST /api/v1/files/complete  POST /api/v1/files/upload
GET|PATCH|DELETE /api/v1/files/{id}  POST /api/v1/files/{id}/restore
GET  /api/v1/files/{id}/download  GET /api/v1/files/{id}/preview
GET|PUT /api/v1/files/{id}/permissions  POST /api/v1/files/{id}/shares
GET  /api/v1/s/{token}  GET /api/v1/s/{token}/download   (público, com expiração)
GET  /api/v1/activities  GET /api/v1/members  GET /api/v1/quota
```

## Testes

```bash
go vet ./... && go test ./...        # unitários + integração (precisa DB + S3, ver CI)
cd web && npx tsc --noEmit && npm run build
```

Os testes de integração cobrem: acesso cruzado entre orgs → 403, nomes/chaves com
path traversal recusados, expiração de link → 410 e round-trip upload→download
byte-idêntico. O CI (`.github/workflows/ci.yml`) roda o backend contra serviços
Postgres + MinIO e o typecheck + build do frontend.

## Postura de segurança (auto-avaliação OWASP)

- **A01**: tudo escopado por org; grants verificados no servidor; CORS reflete
  `Origin` nos fluxos com cookie (sem wildcard+credentials).
- **A02**: bcrypt, JWT curto, refresh httpOnly; nenhum secret de produção no repo.
- **A03**: só SQL parametrizado; nomes/chaves validados; uploads nunca executados.
- **A05/A07**: erros genéricos; limites conhecidos — ainda sem rate limiting ou MFA;
  mínimo de 8 caracteres na senha.
- **A09**: log de atividades append-only + logs estruturados JSON.

## Estrutura

```
cmd/api            entrada (graceful shutdown, healthcheck)
internal/{auth,config,db,handlers,httpx,storage,validate}
migrations/        SQL versionado (idempotente)
web/               app Next.js (drive, login, share público)
docs/              ARCHITECTURE.md + screenshots
```

## Licença

MIT — ver [LICENSE](LICENSE).
