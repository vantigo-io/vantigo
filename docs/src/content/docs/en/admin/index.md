---
title: Administration
description: Installing, configuring and running Vantigo yourself.
sidebar:
  order: 0
  label: Overview
---

This section is for the person who runs Vantigo: installs it, configures it, keeps it
up and upgrades it. Vantigo is one container image and one PostgreSQL database; which
business modules a deployment serves is chosen with the `MODULES` setting, and
identity (accounts, sign-in, MFA, RBAC, OIDC and SCIM) is always part of the
application.

## Where to start

- **Install.** The quickest route is the ready-made Docker Compose stack: pre-built
  images, a one-shot migration job and the application on port 8080. The
  [installation guide](/en/admin/installation/) takes you from the first start to
  production: reverse proxies, health probes, the database connection budget,
  background workers and upgrades.
- **Identity and sign-in.** [Identity, authentication and deployment](/en/admin/authentication/)
  explains local accounts, the environment-configured OIDC provider, email, proxy trust
  and the key material derived from `APP_SECRET`. [SSO and SCIM operations](/en/admin/sso-scim/)
  is the production guide for static OIDC and SCIM: configuration, rotation, migration,
  lifecycle and recovery.
- **Security.** [Transport security and browser hardening](/en/admin/transport-security/)
  walks through https, PostgreSQL TLS, SMTP TLS, HSTS, host filtering and the content
  security policy, and what each choice costs.
- **Operations.** The [management listener](/en/admin/management-listener/) is the
  private status endpoint a control plane polls, and the way the first Owner is seated by
  invitation. [Object storage](/en/admin/object-storage/) describes where receipts, PDFs
  and attachments live.
- **E-invoicing.** [E-invoicing](/en/admin/e-invoicing/) sets up sending invoices as EHF
  over the Peppol network: the Storecove account and its credentials, the switches, the
  two workers that carry each document, what to do when one is unconfirmed or failed,
  and the KID agreement to ask the bank for.

## The complete configuration reference

Every setting the application reads is declared in one place,
[`internal/config`](https://github.com/vantigo-io/vantigo/blob/main/apps/server/internal/config/config.go),
which validates the whole environment at start and reports every problem at once. The
example environment file in
[deploy/compose/vantigo.env.example](https://github.com/vantigo-io/vantigo/blob/main/deploy/compose/vantigo.env.example)
lists them with their defaults.
