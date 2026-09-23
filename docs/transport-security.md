# Transport security and browser hardening

Transport — http or https at the edge, TLS or plaintext to PostgreSQL and to the
SMTP relay — is the operator's choice, made per deployment in the environment. The
process does not second-guess it: nothing about transport fails configuration
validation, in any environment. What it does do is send a set of browser security
headers on every response and derive the few things that follow from the choice
(the cookie `Secure` attribute, HSTS). This document states what each setting
means, what the plaintext options cost, and how to configure the common shapes.

## Status summary

| Control | Development | Outside development |
| --- | --- | --- |
| `APP_URL` scheme | http or https | http or https |
| PostgreSQL connection | any `sslmode`, TCP or Unix socket | any `sslmode`, TCP or Unix socket |
| Application SMTP (`SMTP_*`) | `starttls`, `implicit` or `none` | `starttls`, `implicit` or `none` |
| Cookie `Secure` attribute | set when `APP_URL` is https | set when `APP_URL` is https |
| HSTS | not sent | sent on https requests, loopback excluded |
| Security headers, including CSP | sent | sent |
| Host header filtering | `APP_URL`'s host + loopback | `APP_URL`'s host + loopback |
| HTTPS redirection | none | none (HSTS header only) |
| Merged OpenAPI document | `GET /api/openapi.json`, any authenticated session | `GET /api/openapi.json`, any authenticated session |

## What each choice costs

- **`APP_URL` on `http://`.** Session cookies and the bearer links mailed by the
  invitation and password-recovery workflows are derived from this origin, so an http
  origin hands them to anyone on the network path. Cookies are set without the
  `Secure` attribute (a `Secure` cookie never reaches an http origin, so the
  attribute follows the scheme), and HSTS is never sent. Sound only when the path
  between browser and process is one you control end to end. This is also why the
  `Secure` attribute is computed rather than hard-coded, and why code scanning's
  `go/cookie-secure-not-set` on `internal/identity/cookies.go` is a false positive
  to dismiss rather than a finding to fix: the query flags a cookie write its taint
  tracking cannot connect to any `Secure` value, so hard-coding `true` there does
  not clear it — measured — while it would lock every plain-http installation out
  of signing in. The attribute is pinned per scheme by
  `TestSessionCookieAttributes`.
- **A PostgreSQL connection without certificate verification.** libpq's default
  `sslmode` is `prefer`, which falls back to plaintext and never checks a
  certificate even over TLS; `require` encrypts but does not authenticate the server.
  Only `verify-full` — or `verify-ca` when the server certificate does not name the
  host you connect on — authenticates it. Over a network you do not control, the
  database credential and every row of every tenant travel on this connection.
- **`SMTP_TLS=none`.** Mail credentials and message content, including the bearer
  links above, travel in the clear to the relay. `starttls` (the default) is
  mandatory rather than opportunistic, so a server that advertises no STARTTLS
  produces an error instead of a plaintext delivery; `implicit` is TLS from the
  first byte.

Both `DATABASE_URL` and `MIGRATIONS_DATABASE_URL` are still parsed the way pgx will
parse them, so a malformed connection string fails configuration rather than the
first connection. The error never quotes the string: it can carry a password.

## Common shapes

A deployment across a network looks like:

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

A deployment where PostgreSQL runs on the same host has no certificate authority to
verify against and no network to protect. Either say so on a TCP connection:

```text
DATABASE_URL=postgresql://vantigo:<password>@127.0.0.1:5432/vantigo?sslmode=disable
```

or connect over PostgreSQL's Unix domain socket, which never involves TLS at all
(pgx, like libpq, ignores every `ssl*` setting on a socket host). The host is the
directory that holds the socket, not the socket file:

```text
DATABASE_URL=postgresql://vantigo:<password>@/vantigo?host=/var/run/postgresql
```

or, in keyword form, `host=/var/run/postgresql user=vantigo password=… dbname=vantigo`.
The same forms work for `MIGRATIONS_DATABASE_URL`. Two things to know about sockets:

- **From the container, mount the socket directory** — for example
  `-v /var/run/postgresql:/var/run/postgresql` — and keep the mounted path under
  108 bytes, the kernel's limit on a Unix socket path. The image runs as an
  unprivileged user, so PostgreSQL's `peer` authentication (which maps the OS user
  to a role) will not match; give the role a password and let `pg_hba.conf` use
  `scram-sha-256` for `local` connections instead.
- **Bundled Compose stack.** The PostgreSQL container's socket is not shared with the
  application containers; they connect over the private compose network, where
  `sslmode=disable` is the honest setting.

SMTP has a protection that is not about TLS: the destination is resolved and checked
before the socket opens, and private, loopback, link-local, carrier-grade-NAT and
cloud-metadata addresses (including `169.254.169.254`) are refused. The connection is
then made to the address that was checked rather than to a fresh lookup, which is
what defeats DNS rebinding. This applies whatever `SMTP_TLS` says.

## Upgrading from the fail-closed rules

Earlier releases refused to start outside development unless `APP_URL` was https,
the database connection verified the server certificate, and `SMTP_TLS` was not
`none`, with `ALLOW_INSECURE_TRANSPORT=1` as a single escape hatch that relaxed all
three at once. Those rules and that variable are gone; the process reads
`ALLOW_INSECURE_TRANSPORT` no longer and ignores it if it is still set. A deployment
that carried the flag keeps working unchanged. A deployment that met the old rules
also keeps working unchanged — `sslmode=verify-full` and `SMTP_TLS=starttls` mean
exactly what they meant.

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
image. The rule is `session`, which **any authenticated user** satisfies: no
permission, policy or role is checked. The document is therefore protected from
anonymous visitors, not restricted to administrators. There is no `/openapi/v1.json` route. The per-module source contracts live in
`openapi/*.yaml` in the repository and are the right input for client generation;
`bun run gen:client` regenerates the typed frontend client from them.
