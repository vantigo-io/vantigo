# Transport security and browser hardening

Vantigo treats plaintext transport as a startup failure rather than an operational
detail, and sends a set of browser security headers on every response. This document
states what is enforced, what it costs, and how to get out of it deliberately.

## Status summary

| Control | Development | Outside development |
| --- | --- | --- |
| `APP_URL` scheme | http allowed | **https required** |
| PostgreSQL connection | any `sslmode` | **certificate-verified TLS (`verify-full`, or `verify-ca`)** |
| Application SMTP (`SMTP_*`) | plaintext allowed with an opt-in | **STARTTLS or implicit TLS required** |
| HSTS | not sent | sent on https requests, loopback excluded |
| Security headers, including CSP | sent | sent |
| Host header filtering | `APP_URL`'s host + loopback | `APP_URL`'s host + loopback |
| HTTPS redirection | none | none (HSTS header only) |
| Merged OpenAPI document | `GET /api/openapi.json`, session required | `GET /api/openapi.json`, session required |

## Fail-closed transport

Outside development the process refuses to start when:

- **`APP_URL` is not `https://…`.** Session cookies and the bearer links mailed by
  the invitation and password-recovery workflows are derived from this origin, so an
  http origin hands them to anyone on the network path.
- **A configured PostgreSQL connection would not authenticate the server.** The
  check is made on pgx's own parse of the connection string, so it judges exactly
  what the driver will connect with, and **every fallback must pass too**: libpq's
  default, `prefer` and `allow` add a plaintext fallback and never check a
  certificate even over TLS. Only `verify-full` — or `verify-ca` when the server
  certificate does not name the host you connect on — authenticates the server. Both
  `DATABASE_URL` and `MIGRATIONS_DATABASE_URL` are checked. The error never quotes
  the connection string: it can carry a password.
- **`SMTP_TLS=none` is configured.** `starttls` (the default) and `implicit` are the
  TLS modes; STARTTLS is mandatory rather than opportunistic, so a server that
  advertises no STARTTLS produces an error instead of a plaintext delivery.

A correct production configuration therefore looks like:

```text
APP_URL=https://vantigo.example.com
DATABASE_URL=postgresql://vantigo:<from-secret-store>@db.example.com:5432/vantigo?sslmode=verify-full
MAIL_DRIVER=smtp
SMTP_HOST=smtp.example.com
SMTP_PORT=587
SMTP_TLS=starttls
```

When the server certificate is issued by a private authority, point the connection at
it with `sslrootcert=/etc/ssl/certs/internal-ca.pem` and keep `sslmode=verify-full`.
Use `verify-ca` only when the certificate does not carry the host name you connect on.

SMTP has a second protection that is not about TLS: the destination is resolved and
checked before the socket opens, and private, loopback, link-local,
carrier-grade-NAT and cloud-metadata addresses (including `169.254.169.254`) are
refused. The connection is then made to the address that was checked rather than to a
fresh lookup, which is what defeats DNS rebinding.

## The escape hatch

`ALLOW_INSECURE_TRANSPORT=1` disables all three checks at once. It exists for local
stacks and evaluation deployments — including the bundled
[Docker Compose stack](../deploy/compose/README.md), which serves
`http://localhost:8080` and reaches the bundled PostgreSQL container over a private
compose network with no certificate authority available — and ships enabled in
`deploy/compose/vantigo.env.example` for exactly that reason.

The process says so on every start:

```text
WARN ALLOW_INSECURE_TRANSPORT=1: plaintext HTTP, database and SMTP transport are
accepted; local and evaluation use only
```

With it set, session cookies, invitation and password-reset bearer links, and
database credentials all travel in the clear. It also drops the `Secure` attribute
from session cookies. Anything reachable from a network you do not control must
leave it unset.

Unlike the .NET implementation, plaintext SMTP needs no second, narrower opt-in:
`SMTP_TLS=none` plus either development or `ALLOW_INSECURE_TRANSPORT=1` is the whole
rule, and the SMTP driver refuses the combination defensively even if it is
constructed directly.

## HSTS

Outside development `Strict-Transport-Security: max-age=2592000` (30 days, no
`includeSubDomains`, no preload) is sent on https requests to non-loopback hosts. The
header is set after forwarded-header processing, so a TLS-terminating proxy's
scheme is the one the decision sees. Terminating ingress should still reject
plaintext on its own — the header only helps a browser that has already been there
once.

## Host header filtering

The allowlist is derived from `APP_URL`'s hostname plus `localhost`, `127.0.0.1` and
`::1`; a request carrying any other `Host` is answered with a 400 problem before it
reaches the application. The port is ignored in the comparison.

Loopback is in that list on purpose. The container health check runs
`vantigo healthcheck`, which probes `http://127.0.0.1:$PORT/health/ready` in-process
with the literal address in the `Host` header, and host filtering runs before routing
so it cannot exempt that path by name. A 400 there would mark the container
permanently unhealthy.

This is also why container and orchestrator probes must use the exec form
(`["/app/vantigo", "healthcheck"]`): an HTTP probe that connects from outside sends
its own address as `Host`, which the filter rejects.

Nothing in the pipeline redirects plain HTTP. There is no HTTPS-redirection
middleware, and HSTS only ever adds a response header — and only when the request is
already https — so a probe that treats any non-2xx as failure is never handed a 307.
Terminate TLS and reject plaintext at the ingress instead.

## Browser security headers

Every response — SPA document and API alike — carries:

| Header | Value |
| --- | --- |
| `X-Content-Type-Options` | `nosniff` |
| `X-Frame-Options` | `DENY` |
| `Referrer-Policy` | `no-referrer` |
| `Permissions-Policy` | camera, microphone, geolocation and other unused features denied |
| `Content-Security-Policy` | see below |

The policy is:

```text
default-src 'self'; base-uri 'self'; object-src 'none'; frame-ancestors 'none';
form-action 'self'; script-src 'self' 'sha256-…'; style-src 'self' 'unsafe-inline';
img-src 'self' data: blob: https:; font-src 'self' data:; connect-src 'self';
frame-src 'self'; worker-src 'self'; manifest-src 'self'
```

Two directives deserve explanation.

- **`script-src` carries a hash, not `'unsafe-inline'`.** The SPA entry document is
  templated at startup with one inline script, the `window.__VANTIGO_APP__` runtime
  configuration. The document and the hash are produced from the same string, so the
  policy cannot drift away from the script it allows. Everything else in the
  published Vite bundle is an external module.
- **`style-src` keeps `'unsafe-inline'`.** Mantine renders its theme CSS variables
  and component styles as inline `<style>` elements, and React style props become
  style attributes.

`img-src` allows `https:` so a configured `APP_LOGO_URL` and remote images inside the
sandboxed HTML email preview still load.

If a customized frontend needs a wider policy, set `CSP_REPORT_ONLY=1` to emit
`Content-Security-Policy-Report-Only` while the difference is worked out. That
disables the protection, so it is a diagnostic setting, not a destination.

## The API contract document

The running server serves the merged contract of its **enabled** modules at:

```text
GET /api/openapi.json
```

It requires a session in every environment, development included — it is not
environment-gated, and there is no Swagger, Scalar or other documentation UI in the
image. There is no `/openapi/v1.json` route. The per-module source contracts live in
`openapi/*.yaml` in the repository and are the right input for client generation;
`bun run gen:client` regenerates the typed frontend client from them.
