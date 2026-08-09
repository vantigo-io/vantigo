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
one-shot job, and then starts the applications. Each app is served under its
base path (see below):

- **Customers** — <http://localhost:8080/customers>
- **Communications** — <http://localhost:8081/communications>

## First sign-in

Visit <http://localhost:8080/customers/setup> to create the first Owner
account. The setup page asks for a one-time bootstrap secret:

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

## Single domain and base paths

Each application is served under a configurable path prefix so every Vantigo
app can share one public domain:

- Customers defaults to `/customers` (`App__BasePath` in `customers.env`)
- Communications defaults to `/communications` (`App__BasePath` in
  `communications.env`)

The base path is a pure runtime setting: at startup each API rewrites the SPA
entry document (`index.html`) to the configured prefix and injects it for the
frontend to read, so the pre-built images serve any `App__BasePath` — including
an empty value for serving from the domain root — without rebuilding.

A reverse proxy only needs to route by path — no rewriting required, because
each app understands its own prefix. Set `App__PublicOrigin` to the shared
public origin (e.g. `https://vantigo.example.com`) so generated links (email
URLs, the OIDC callback to register) are derived automatically from origin +
base path. Example with nginx:

```nginx
server {
    listen 443 ssl;
    server_name vantigo.example.com;

    location /customers {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    location /communications {
        proxy_pass http://127.0.0.1:8081;
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

Or with Caddy:

```caddy
vantigo.example.com {
    handle /customers* {
        reverse_proxy 127.0.0.1:8080
    }
    handle /communications* {
        reverse_proxy 127.0.0.1:8081
    }
}
```

Requests without the prefix still reach each app's routes directly (the prefix
is optional on the wire), so health checks against the container root keep
working.

## Whitelabeling

Each app can be branded through runtime settings — no rebuild required, because
the API templates the SPA entry document at startup:

| Variable | Default | Effect |
| --- | --- | --- |
| `App__Title` | App name (`Customers` / `Communications`) | Header, browser tab, setup/invitation copy |
| `App__LogoUrl` | Bundled Vantigo logo | Header logo (shown at 32px height, width auto) |
| `App__Support__Email` | unset | Support footer + sign-in page |
| `App__Support__Phone` | unset | Support footer + sign-in page |
| `App__Support__Url` | unset | "Help center" link in the support footer |

The support footer only appears when at least one support setting is
configured. See the commented examples in `customers.env.example` and
`communications.env.example`.

## Production notes

- Serve the applications behind a TLS-terminating reverse proxy on a single
  public origin (see "Single domain and base paths" above), and configure the
  `ForwardedHeaders__*` settings in `customers.env` to trust exactly that
  proxy.
- Set `App__PublicOrigin` to your public origin; mailed invitation and
  password-reset links are derived from it automatically. The explicit
  `Authentication__*Url` templates remain available as overrides.
- The `customers-dataprotection` volume persists the keys that keep sign-in
  cookies and account tokens valid across restarts — keep it.
- Never run the `seed` command in production; it is Development-only.

## Upgrading

```bash
docker compose pull
docker compose up -d
```

Migrations run automatically before each application starts.
