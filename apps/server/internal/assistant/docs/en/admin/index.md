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
- **Payments.** [Payments from the bank](/en/admin/payments/) is the bank agreement that
  gives you OCR giro or camt.054 files of incoming payments, where each bank lets you
  download them, and the format each account's files are imported in.
- **Staff.** [Staff](/en/admin/staff/) turns the workforce register on, gives people
  access to it with its five permissions, and sets the employee number sequence. The
  business time zone every module judges a day in is the workspace's, set under
  [Workspace administration](/en/user/workspace-administration/#workspace-settings).
- **Orders.** [Orders](/en/admin/orders/) is on when `orders` is in `MODULES` beside
  `products`, `customers` and `invoices`, which it requires; its nine permissions, the
  number series, quote validity, payment terms, the send channel, the mail texts and the
  quote templates. Map Products' tax categories to VAT codes in Invoices before people
  quote.
- **Calendar.** [Calendar](/en/admin/calendar/) is on when `calendar` is in `MODULES`
  (or `MODULES` is unset), with three permissions — `calendar:access` to use it,
  `calendar:view-colleagues` to see colleagues' busy and free times, and `calendar:manage` to
  choose the channel invitations are sent from. Invitations to people outside the company
  need that channel chosen, a sending domain with SPF, DKIM and DMARC, and — for invitees outside your network — the public host; the
  `calendar-cancellations` worker erases a cancelled appointment once its last day has
  passed. A person's calendar address — the feed a phone subscribes to — is on the
  public host's list, rate limited, with its token in the path; the
  `calendar-subscriptions` worker fetches the calendars people subscribe to from the
  internet, through the outbound address guard.
- **Microsoft Entra ID.** [Microsoft Entra ID](/en/admin/entra/) connects Vantigo to the
  company's tenant so the accounts Staff gives are made there: `entra` in `MODULES`
  beside `staff`, which it requires; an app registration with eight application
  permissions, admin consent and the User Administrator role; client secrets added and
  rotated by an Owner; the Temporary Access Pass policy; and what a leaver's steps and a
  delete do in the tenant. Nothing of it is in `vantigo.env`; its page is under
  **Integrations** in Workspace admin.
- **Google Workspace.** [Google Workspace](/en/admin/google-workspace/) connects Vantigo
  to the company's Google Workspace organisation through a service account with
  domain-wide delegation: `google` in `MODULES` beside `staff`, which it requires; a
  Google Cloud project with the Admin SDK API, the service account's JSON key uploaded
  and rotated by an Owner, the scopes delegated in the Admin console, the admin to
  impersonate, and the Internal web client. This release reads the organisation; the
  accounts, calendar and Gmail come later. Nothing of it is in `vantigo.env`; its page
  is under **Integrations** in Workspace admin.
- **Shipping.** [Shipping](/en/admin/shipping/) sets up the carrier accounts Orders books
  shipments with: Bring's Mybring user and API key — with the financial role for prices —,
  the customer numbers, the test mode, the default account per site, the freight product
  and the markup. `SHIPPING_CARRIERS` in `vantigo.env` withholds a carrier; the accounts
  are under **Integrations** in Workspace admin.

## Configuration

[Configuration](/en/admin/configuration/) explains the two tiers every setting belongs
to — the instance's, in the environment, which you own, and the workspace's, in the
database, which the tenant's Owner edits under Workspace admin — lists the instance's
settings by what they decide, and says where each of the workspace's is edited.

## The complete configuration reference

Every setting the application reads is declared in one place,
[`internal/config`](https://github.com/vantigo-io/vantigo/blob/main/apps/server/internal/config/config.go),
which validates the whole environment at start and reports every problem at once. The
example environment file in
[deploy/compose/vantigo.env.example](https://github.com/vantigo-io/vantigo/blob/main/deploy/compose/vantigo.env.example)
lists them with their defaults.
