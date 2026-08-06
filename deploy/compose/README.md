# Run Vantigo with Docker Compose

Run the pre-built Vantigo container images from
[GHCR](https://github.com/orgs/vantigo-io/packages) — no SDKs or build tools
required, only Docker (or any Compose-compatible runtime).

## Quick start

Download the files in this folder, or grab them directly:

```bash
mkdir vantigo && cd vantigo
base=https://raw.githubusercontent.com/vantigo-io/vantigo/main/deploy/compose
curl -fsSLO "$base/compose.yaml"
curl -fsSLO "$base/.env.example"
curl -fsSLO "$base/customers.env.example"
curl -fsSLO "$base/communications.env.example"
```

Create your local configuration from the examples and set a database password:

```bash
cp .env.example .env
cp customers.env.example customers.env
cp communications.env.example communications.env
```

Start the stack:

```bash
docker compose up -d
```

Compose starts PostgreSQL, runs each application's database migrations as a
one-shot job, and then starts the applications:

- **Customers** — <http://localhost:8080>
- **Communications** — <http://localhost:8081>

## First sign-in

Visit <http://localhost:8080/setup> to create the first Owner account. The
setup page asks for a one-time bootstrap secret:

- If you set `Authentication__Bootstrap__Secret` in `customers.env`, use that
  value.
- Otherwise a secret is generated at startup and printed once in the logs:
  `docker compose logs customers | grep -i bootstrap`

After the Owner account is created, remove or rotate the bootstrap secret.
Owners can invite further users from `/settings`.

## Configuration

| File                 | Purpose                                                       |
| -------------------- | ------------------------------------------------------------- |
| `.env`               | Shared: image tag, PostgreSQL credentials, host ports         |
| `customers.env`      | Customers app: bootstrap secret, public URLs, email, proxy    |
| `communications.env` | Communications app settings                                   |

Pin a specific release by setting `VANTIGO_TAG=v1.2.3` in `.env`. The full
Customers configuration reference lives in
[docs/customers-authentication.md](../../docs/customers-authentication.md).

## Production notes

- Serve each application behind a TLS-terminating reverse proxy on a single
  public origin, and configure the `ForwardedHeaders__*` settings in
  `customers.env` to trust exactly that proxy.
- Update the `Authentication__*Url` templates to your public origin.
- The `customers-dataprotection` volume persists the keys that keep sign-in
  cookies and account tokens valid across restarts — keep it.
- Never run the `seed` command in production; it is Development-only.

## Upgrading

```bash
docker compose pull
docker compose up -d
```

Migrations run automatically before each application starts.
