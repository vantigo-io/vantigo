# Transport security and browser hardening

Vantigo treats plaintext transport as a startup failure rather than an
operational detail, and sends a set of browser security headers on every
response. This document states what is enforced, what it costs, and how to get
out of it deliberately.

## Status summary

| Control | Development | Outside Development |
| --- | --- | --- |
| `App__PublicOrigin` scheme | http allowed | **https required** |
| PostgreSQL connection | any `SSL Mode` | **`SSL Mode=VerifyCA` or `VerifyFull`, no `Trust Server Certificate=true`** |
| Application SMTP (`Email__Smtp__*`) | plaintext allowed with an opt-in | **STARTTLS or implicit TLS required** |
| HSTS | not sent | sent (`UseHsts`, loopback excluded) |
| Security headers, including CSP | sent | sent |
| Host header filtering | derived from `App__PublicOrigin` | derived from `App__PublicOrigin` |
| OpenAPI document at `/openapi/v{n}.json` | served | **not mapped in Production** |

## Fail-closed transport

Outside the Development environment the host refuses to start when:

- `App__PublicOrigin` is not `https://…`. Session cookies and the bearer links
  mailed by the invitation and password-recovery workflows are derived from this
  origin, so an http origin hands them to anyone on the network path.
- A configured PostgreSQL connection string does not require certificate-verified
  TLS. Npgsql defaults to `SSL Mode=Prefer`, which silently falls back to
  plaintext when the server declines TLS and never authenticates the server even
  when it accepts. Only `VerifyCA` and `VerifyFull` validate the chain, and
  `Trust Server Certificate=true` turns that validation back off. Both
  `ConnectionStrings__vantigo` and the optional
  `DataProtection__PostgreSql__ConnectionString` override are checked.
- `Email__Provider=Smtp` is configured without TLS. `Email__Smtp__EnableSsl=true`
  selects STARTTLS and port 465 selects implicit TLS; the sender no longer
  negotiates opportunistically, so a server that advertises no STARTTLS produces
  an error instead of a plaintext delivery.

A correct production configuration therefore looks like:

```text
App__PublicOrigin=https://vantigo.example.com
ConnectionStrings__vantigo=Host=db.example.com;Database=vantigo;Username=vantigo;Password=<from-secret-store>;SSL Mode=VerifyFull
Email__Provider=Smtp
Email__Smtp__Host=smtp.example.com
Email__Smtp__Port=587
Email__Smtp__EnableSsl=true
```

When the server certificate is issued by a private authority, point Npgsql at it
with `Root Certificate=/etc/ssl/certs/internal-ca.pem` and keep
`SSL Mode=VerifyFull`. Use `VerifyCA` only when the certificate does not carry
the host name you connect on.

## The escape hatch

`Security__AllowInsecureTransport=true` disables all three checks at once. It
exists for local stacks and evaluation deployments — including the bundled
[Docker Compose stack](../deploy/compose/README.md), which serves
`http://localhost:8080` and reaches the bundled PostgreSQL container over a
private compose network with no certificate authority available — and ships
enabled in `deploy/compose/vantigo.env.example` for exactly that reason.

With it set, session cookies, invitation and password-reset bearer links, and
database credentials all travel in the clear. Anything reachable from a network
you do not control must leave it unset.

Plaintext SMTP needs a second, narrower opt-in as well:
`Email__Smtp__AllowInsecurePlaintext=true` on top of either the Development
environment or `Security__AllowInsecureTransport=true`. The communications
module's own mailbox delivery has an equivalent switch,
`Smtp__AllowInsecurePlaintext`, which is only honored in Development.

## HSTS

Outside Development the host adds `UseHsts()` after forwarded-header processing,
so a TLS-terminating reverse proxy's scheme is the one the middleware sees. The
ASP.NET Core defaults apply (30 days, loopback hosts excluded, no preload);
adjust them through `HstsOptions` if a longer max-age or preload submission is
wanted. Terminating ingress should still be configured to reject plaintext on
its own — the header only helps a browser that has already been there once.

## Host header filtering

`AllowedHosts` is no longer `*`. When it is unset the host derives the allowlist
from `App__PublicOrigin` and adds `localhost` and `127.0.0.1` so container and
load-balancer probes keep working; a request carrying any other `Host` header is
answered with 400 before it reaches the application. An explicit
`AllowedHosts=vantigo.example.com;vantigo.internal` always wins, and is what to
use when probes reach the app on a name that is not the public origin. With no
public origin configured there is nothing to derive, and the permissive framework
default remains.

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

- **`script-src` carries a hash, not `'unsafe-inline'`.** The SPA entry document
  is templated at startup and has one inline script injected into it, the
  `window.__VANTIGO_APP__` runtime configuration. Both the document and the hash
  are produced from the same string by `SpaIndexDocument`, so the policy cannot
  drift away from the script it allows. Everything else in the published Vite
  bundle is an external module; the bundle contains no `eval`, `new Function`,
  worker or blob-URL usage.
- **`style-src` keeps `'unsafe-inline'`.** Mantine renders its theme CSS
  variables and component styles as inline `<style>` elements, and React style
  props become style attributes. Removing this would require threading a nonce
  through `MantineProvider`.

`img-src` allows `https:` so a configured `App__LogoUrl` and remote images inside
the sandboxed HTML email preview still load.

If a customized frontend needs a wider policy, set
`Security__ContentSecurityPolicyReportOnly=true` to emit
`Content-Security-Policy-Report-Only` while the difference is worked out. That
disables the protection, so it is a diagnostic setting, not a destination.

## OpenAPI

`/openapi/v{n}.json` enumerates every endpoint and payload shape in the
installation. It is mapped in Development and Staging and is **not mapped in
Production**; requests there fall through to the SPA. Run the host with
`ASPNETCORE_ENVIRONMENT=Staging` against a production-shaped configuration when
the document is needed for client generation.