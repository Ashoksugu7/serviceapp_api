# ServiceOps360 API

Go REST API for ServiceOps360, covering authentication, master data, dynamic forms and service workflows. The implementation has been validated against its database migrations and OpenAPI contract; see [backend verification](docs/backend-verification.md).

## Layout

```text
api/
  cmd/api/              Application entry point and server lifecycle
  cmd/bootstrap-super-admin/ Local interactive first-admin command
  internal/config/     Validated environment configuration
  internal/database/   PostgreSQL pool and startup connectivity check
  internal/httpapi/    Versioned routing, JSON responses and middleware
  internal/service/    Reserved for business rules
  internal/repository/ Reserved for tenant-scoped persistence
  tests/               All unit, HTTP, integration, contract and migration tests
  migrations/          Up/down migrations 000001–000009 and SQL verification
  scripts/migrate.sh   Applies pending migrations and records them in schema_migrations
```

## Foundation decisions

- Module path: `serviceops360/api`, a local module name until a repository URL is chosen.
- Go 1.26 with standard-library `net/http` routing and `log/slog` logging.
- PostgreSQL access through [pgxpool](https://pkg.go.dev/github.com/jackc/pgx/v5/pgxpool).
- Synchronous business handlers; transactions complete before success responses. The HTTP server handles concurrent traffic. Background jobs are deferred until a workflow requires them.
- The process verifies database connectivity before serving and drains HTTP requests on SIGINT/SIGTERM before closing the pool.
- Requests have a context deadline. All future database operations must receive the request context. Timeout responses use HTTP 503 with `REQUEST_TIMEOUT`.
- JSON requests are limited to 1 MiB, require `application/json`, reject unknown properties and accept exactly one object.
- PostgreSQL startup errors identify common configuration categories without exposing connection details.
- No company data routes are exposed before authentication and tenant enforcement are implemented.

## Database migrations

Apply pending migrations before starting a new version of the API. See [migrations/README.md](migrations/README.md#apply-migrations):

```sh
DATABASE_URL='postgres://…' scripts/migrate.sh status
DATABASE_URL='postgres://…' scripts/migrate.sh up
```

If login or other requests return HTTP 500 after an update, check `logs/api.log`. A `column … does not exist` error means migrations are pending.

## Configuration

The process reads environment variables. `.env.example` is a template and is not automatically loaded.

Generate a development JWT secret with `openssl rand -base64 32` and export it as `JWT_SECRET`. Keep the generated value outside source control.

| Variable | Default | Purpose |
| --- | --- | --- |
| `HTTP_ADDR` | `127.0.0.1:8080` | Listen address |
| `DATABASE_URL` | Required | PostgreSQL connection string; configure TLS for the target environment |
| `JWT_SECRET` | Required | HS256 and rate-key secret; at least 32 random bytes |
| `DB_MAX_CONNS` | `10` | Maximum pool size |
| `DB_CONNECT_TIMEOUT` | `5s` | Startup connection deadline |
| `HTTP_REQUEST_TIMEOUT` | `10s` | Handler deadline and request read timeout |
| `HTTP_SHUTDOWN_TIMEOUT` | `15s` | Graceful HTTP shutdown deadline |
| `AUTH_RATE_WINDOW` | `15m` | Failed-login counting window, from 1m to 1h |
| `AUTH_EMAIL_FAILURE_LIMIT` | `5` | Failed attempts allowed per normalized email/window |
| `AUTH_IP_FAILURE_LIMIT` | `20` | Failed attempts allowed per direct client IP/window |

Use the local example connection string only with your own development database credentials. Credentials and local `.env` files are ignored by the root `.gitignore`.

## Available routes

| Method | Route | Behavior |
| --- | --- | --- |
| GET / HEAD | `/api/v1/health/live` | Process liveness; no database query |
| GET / HEAD | `/api/v1/health/ready` | PostgreSQL ping; 503 if unavailable |
| GET / HEAD | `/api/v1/enums` | Fixed UI dropdown values; no authentication required |
| POST | `/api/v1/auth/login` | Authenticate and create a 30-minute database-backed session |
| GET / HEAD | `/api/v1/auth/me` | Return the current database-verified user/company identity |
| POST | `/api/v1/auth/logout` | Revoke the presented session |

Health routes are operational additions to the source specification. They expose no company data. Unknown routes return JSON 404; unsupported health-route methods return JSON 405 with an `Allow` header. Database errors returned to clients omit connection details.

## MCP

The API exposes a Streamable HTTP MCP server at `/mcp`. It provides the
`serviceops_api_request` tool, which can call every published `/api/v1/` REST
operation using its method, path, optional query/body, and (when needed) a
ServiceOps access token. The tool delegates to the ordinary API handler, so all
existing authentication, authorization and tenant checks still apply.

For example, an MCP client can call the tool with:

```json
{
  "method": "GET",
  "path": "/api/v1/companies/<company-id>/customers",
  "access_token": "<access-token>"
}
```

Do not put a token in a tool call for public routes such as `/api/v1/enums`.
The MCP endpoint follows the Streamable HTTP protocol; use an MCP client rather
than treating it as an ordinary REST endpoint.

## Docker deployment

The `Dockerfile` builds a minimal non-root API image. `compose.yaml` runs only
the API and connects it to the externally managed PostgreSQL instance named by
`DATABASE_URL`. It never runs database migrations at startup.

```sh
cp docker.env.example docker.env
# Edit docker.env: use a strong PostgreSQL password and a 32+ byte random JWT secret.
docker compose --env-file docker.env build
docker compose --env-file docker.env --profile tools run --rm migrate
docker compose --env-file docker.env up -d api
docker compose --env-file docker.env --profile tools run --rm -it bootstrap-super-admin
```

Your database administrator must apply the project migrations before starting
the API; the `migrate` service runs the included migration script with
`up --yes`. Back up the database before running it, especially when migration
000008 is pending. The optional
`bootstrap-super-admin` command connects to the external database interactively.
Put the API behind a TLS-terminating reverse proxy before serving it publicly.

## Verification

`go test ./...`, `go test -race ./...`, `go vet ./...`, formatting and module verification have passed. Tests cover configuration, safe database errors, JSON decoding/body limits, health routing, HEAD responses, timeout cancellation, server startup/shutdown, authentication, HTTP handling, and disposable-schema repository integration. Database migration constraint/down/reapply checks passed on PostgreSQL 14.20 after contract reconciliation. The application database has not been migrated.

When compilation and runtime checks are authorized, run from this directory:

```sh
go test ./...
go vet ./...
# Set the required environment variables before starting:
./run.sh
```

`run.sh` creates `logs/` when needed and appends API output to `logs/api.log`.

After applying migrations, create the first SUPER_ADMIN in an interactive terminal:

```sh
go run ./cmd/bootstrap-super-admin
```

The [requirements baseline](docs/t01-requirements.md) and validated [OpenAPI contract](contracts/openapi.json) define the API behavior. Transaction behavior is summarized in the [Out-Store workflow](docs/out-store-workflow.md) and [stand-by workflow](docs/standby-workflow.md). See [backend verification](docs/backend-verification.md) and [migration instructions](migrations/README.md) for operational details.
