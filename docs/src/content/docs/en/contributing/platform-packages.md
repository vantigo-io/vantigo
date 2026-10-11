---
title: Platform packages
description: The packages under the server's internal/ that every module may use — today the secrets box that seals and MACs values under keys derived from APP_SECRET.
sidebar:
  order: 5
sources:
  - apps/server/internal/secrets
---

A module may import the platform but never another module (see
[Module boundaries](/en/contributing/module-boundaries/)). This page describes the
platform packages a module reaches for when it holds something it must not store in
clear.

## The secrets box

[`apps/server/internal/secrets`](https://github.com/vantigo-io/vantigo/tree/main/apps/server/internal/secrets)
is the one place the server turns `APP_SECRET` into keys. A module receives the box as
`Deps.Secrets` and never sees a key.

The box derives one 32-byte key per **purpose** with HKDF-SHA256: `APP_SECRET` is the
input key material, the salt is fixed, and the purpose string is the HKDF info. A key
is derived once and cached for the life of the process. Two operations use it:

- **`Seal` and `Open`** encrypt with AES-256-GCM under the purpose's key, with a fresh
  random nonce each time and the purpose bound in as additional authenticated data, so
  a value sealed for one purpose cannot be opened under another. The output starts
  with a key-id byte, `0x01` today, which leaves room for a rotation scheme. `Open`
  never says why it failed. `SealString` and `OpenString` are the same operations for
  text contexts such as cookies.
- **`MAC`** returns the HMAC-SHA256 of the data under the purpose's key: 32 bytes,
  the same every time for the same purpose, data and `APP_SECRET`. Sealing is
  randomised, so two seals of one value never match; a MAC is what lets a sealed
  value be found, or refused as a duplicate by a unique column, without opening
  anything. Compare two MACs with **`MACEqual`**, which takes the same time wherever
  they differ. The only error `MAC` returns is the key derivation's.

### Purposes

A purpose is a constant the calling module owns, never input from a request, written
`module/purpose`:

| Purpose | Used for |
|---|---|
| `identity/totp` | a user's TOTP secret |
| `identity/oidc-state`, `identity/oidc-external` | the OIDC flow's state and external-identity cookies |
| `communications/channel-smtp-credential` | a channel's SMTP password |
| `invoices/access-point-credential` | the e-invoicing access point's API key |

A purpose either seals or MACs, never both, so no key both encrypts and
authenticates: a module that seals a value and indexes it uses two purposes, such as
`staff/national-id` for the sealed value and `staff/national-id-index` for its MAC.
When one MAC purpose covers values of several kinds, the caller separates them in the
input itself — kind, then country, then value, with a separator none of them can
contain — so the same characters under two kinds do not collide. The form of that
input is part of every MAC stored: changing it orphans them all.

### Rotating APP_SECRET

There is no key ring. Every key the box derives depends on `APP_SECRET`, so changing it
makes every sealed value unopenable **and** every stored MAC unmatchable: a lookup by
MAC finds nothing, and a unique MAC column no longer refuses a duplicate of a value
written before the change. Nothing re-derives or re-indexes on its own. A rotation
therefore needs a migration that opens and re-seals every sealed column and
recomputes every MAC column under the new secret, with both secrets at hand; until
that exists, `APP_SECRET` is not rotated in place. The operator's side of this is in
[Authentication → Key material](/en/admin/authentication/#key-material).

A module that adds a purpose names it in the table above, and a module that stores
sealed values or MACs says on its own administration page what a rotation costs its
data.
