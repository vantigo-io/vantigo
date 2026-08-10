# Run Vantigo with Docker Compose

Run the pre-built Vantigo container image from
[GHCR](https://github.com/orgs/vantigo-io/packages). Only Docker (or a
Compose-compatible runtime) is required.

## Quick start

Download the files in this folder, or grab them directly:

```bash
mkdir vantigo && cd vantigo
base=https://raw.githubusercontent.com/vantigo-io/vantigo/main/deploy/compose
curl -fsSLO "$base/compose.yaml"
curl -fsSLO "$base/.env.example"
curl -fsSLO "$base/vantigo.env.example"
```

Create local configuration and set a database password:

```bash
cp .env.example .env
cp vantigo.env.example vantigo.env
```

Start the stack:

```bash
docker compose up -d
```

Compose starts PostgreSQL, runs the single application's database migrations as
a one-shot job, and then starts Vantigo:

- **Vantigo** — <http://localhost:8080>
- **API reference** — <http://localhost:8080/openapi/v1.json>

The Customers, Communications and Products modules are enabled by default. They
share one PostgreSQL database named `vantigo`, with independent `identity`,
`customers`, `communications` and `products` schemas and migration histories.

## First sign-in

Visit <http://localhost:8080/setup> to create the first Owner account. The setup
page asks for a one-time bootstrap secret:

- If you set `Authentication__Bootstrap__Secret` in `vantigo.env`, use that value.
- Otherwise a secret is generated at startup and printed once in the logs:
  `docker compose logs vantigo | grep -i bootstrap`

After the Owner account is created, remove or rotate the bootstrap secret. Owners
can invite further users from `/settings`.

## Configuration

| File | Purpose |
| --- | --- |
| `.env` | Image tag, PostgreSQL credentials and host port |
| `vantigo.env` | Application, identity, email, module and proxy settings |

The complete configuration reference is in
[docs/customers-authentication.md](../../docs/customers-authentication.md).
Pin a specific release with `VANTIGO_TAG=v1.2.3` in `.env`.

## Base path and reverse proxy

The whole application can be served below one configurable path prefix. Set
`App__BasePath` in `vantigo.env` and set `App__PublicOrigin` to the public
scheme and host. The API prefixes remain `/api/v1/identity`,
`/api/v1/customers`, `/api/v1/communications` and `/api/v1/products`.

For example, with nginx:

```nginx
server {
    listen 443 ssl;
    server_name vantigo.example.com;

    location /vantigo/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

With `App__BasePath=/vantigo`, generated invitation, password-reset and OIDC
callback URLs include that prefix. The base path is a runtime setting; the
single image serves the matching SPA without a rebuild.

## Email and observability

Identity invitations and password recovery use the `Email__*` settings. The
Communications module uses `Smtp__*` for its default mailbox delivery. Mailgun
mailboxes are configured through the Communications API, where the provider,
domain, region and API key are stored as protected mailbox credentials; there is
no service-to-service API-key environment variable.

Telemetry is off by default. Standard OTLP variables can be set in `vantigo.env`:

```dotenv
OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4317
# OTEL_EXPORTER_OTLP_PROTOCOL=grpc
# OTEL_EXPORTER_OTLP_HEADERS=x-api-key=secret
```

## Production notes

- Put the application behind a TLS-terminating reverse proxy and configure the
  `ForwardedHeaders__*` settings to trust exactly that proxy.
- Set `App__PublicOrigin` to the public origin; mailed links and the OIDC
  callback are derived from it.
- Keep the `vantigo-dataprotection` volume. It persists the keys required for
  sign-in cookies and account tokens across restarts.
- Never run `seed` in production; it is Development-only.

## Upgrading

```bash
docker compose pull
docker compose up -d
```

The migration job runs automatically before the application starts.
