---
title: Meta
description: Connecting Vantigo to Facebook Messenger through your own Meta app — what you need first (meta in MODULES beside communications, the public host, the object store), creating the app in Meta's developer portal, App Review, Business Verification and Advanced Access to pages_messaging (to be confirmed), the privacy policy and data-deletion URLs Meta requires, the Meta page under Integrations (app ID, sealed app secret, the verify token and Regenerate, the webhook URL, the Human Agent checkbox), setting the webhook and subscribing to the Page fields in Meta's portal, adding a Facebook Page with a system user's token that does not expire, removing a Page and a rejected token, verifying with the status line and a first message, retention and personal data (raw events 7 days, acknowledged rows 7 days, never-acknowledged rows 30 days, names 30 days), the workers meta-split and meta-retention, the public host's two webhook routes, and what can go wrong.
sidebar:
  order: 50
sources:
  - apps/server/internal/meta/module.go
  - apps/server/internal/meta/core.go
  - apps/server/internal/meta/server.go
  - apps/server/internal/meta/errors.go
  - apps/server/internal/meta/messenger.go
  - apps/server/internal/meta/settings.go
  - apps/server/internal/meta/settings_ops.go
  - apps/server/internal/meta/transport.go
  - apps/server/internal/meta/graph.go
  - apps/server/internal/meta/webhook.go
  - apps/server/internal/meta/inbound.go
  - apps/server/internal/meta/inbound_worker.go
  - apps/server/internal/meta/retention.go
  - apps/server/internal/meta/send.go
  - apps/server/internal/meta/pages.go
  - apps/server/internal/meta/profile.go
---

The Meta module connects Vantigo to **Facebook Messenger**. You bring your own Meta app
and one or more Facebook Pages your business owns; a person who writes to a Page then
starts a conversation in Communications, and your replies go back through the same
Page. The module holds the connection (the app, the Pages' tokens, the webhook); the
conversations are Communications', and a Page becomes a channel there
([Set up a Messenger channel](/en/user/communications/#set-up-a-messenger-channel)).

This page is the operator's side: what Meta needs, the steps in Meta's developer portal
and in Vantigo, and what to do when something fails. The page in Vantigo is **Meta**
under **Integrations** in Workspace admin, and it is an **Owner**'s alone. The rules the
module checks are in [the reference](/en/reference/meta/).

## Before you start

- **`meta` in `MODULES`, beside `communications`.** `MODULES` refuses `meta` without
  `communications` at startup (`meta requires communications`): a Page is only useful
  as a channel.
- **A public host.** Meta posts every message to the instance, so `PUBLIC_BASE_URL`
  must be set and reachable from the internet over https with a certificate Meta
  trusts. The settings **cannot be saved** without it, and the page says so. Publishing
  the host is its own procedure ([The public host](/en/admin/payments/#the-public-host)).
- **The object store.** Files people send are kept there for a short while
  ([Object storage](/en/admin/object-storage/)).
- **Outbound https** from every process that runs the API or the workers to
  `graph.facebook.com` and to Meta's file servers, without a proxy: the module uses none
  and follows no redirect.
- **An Owner** in Vantigo, signed in with a second factor while `OWNERS_REQUIRE_MFA` is
  on.
- **A Meta developer account**, a Facebook Page the business owns, and a Meta business
  portfolio (Business Manager) that owns the Page.
- The migrations create the `meta` schema whether or not the module is enabled; a
  least-privilege application role needs it granted like every other.

## Create the Meta app

1. In [Meta's developer portal](https://developers.facebook.com/apps/), choose
   **Create app** and give it the **Business** type.
2. Add the **Messenger** product.
3. Under the app's settings, note the **App ID** and the **App secret**. You enter both
   in Vantigo below.

## App Review and Business Verification

**To be confirmed.** As far as Meta's documentation indicates, an app that messages
people who have no role on the app needs **Advanced Access** to the `pages_messaging`
permission, and Advanced Access needs **App Review** and **Business Verification**.
Until the app has it, only people with a role on the app (administrators, developers
and testers) can write to the Page and get answers. Vantigo cannot change this. The
behaviour will be verified against a test app and a test Page before this page drops
the qualification.

Plan for this before you promise customers a channel: apply early, test with people who
have a role on the app, and read Meta's current requirements.

## What Meta requires of the app

Meta requires the business that runs the app to publish a **privacy policy URL** and
the instructions for **data deletion** (or a data-deletion callback URL) before the app
can go live. The business supplies both; Vantigo does not write them. Vantigo keeps
personal data in this module only for the periods in [Retention and personal
data](#retention-and-personal-data), and your policy has to describe that.

## Enter the app in Vantigo

1. Open the avatar menu, **Workspace admin**, then **Integrations** › **Meta**.
2. Under **Meta app**, enter the **App ID** (digits only) and the **App secret**.
   The secret is stored sealed and never shown again; leave the field empty on a later
   save to keep it. **Save**.
3. Under **Webhook**, copy the **Callback URL**,
   `https://<public host>/api/v1/meta/webhook`, and the **Verify token**. The verify
   token is generated for you. **Regenerate** makes a new one, and Meta's handshake is
   refused until the new token is in the app's webhook settings.
4. Leave **Meta has granted this app the Human Agent permission** unticked for now. With
   the permission, a reply can follow the person's last message by up to seven days
   instead of 24 hours; Vantigo then sends it with Meta's `HUMAN_AGENT` tag. Tick it
   only once Meta has approved the permission for the app, which you apply for with
   Meta. If Meta refuses a tagged send, the message fails as *Human Agent is not
   granted to the app* and the status line tells you to clear the checkbox.

## Set the webhook in Meta's portal

1. In the app's **Messenger** settings, open the webhook section and choose to add a
   callback URL. Paste the **Callback URL** and the **Verify token** from Vantigo.
2. Meta makes a `GET` request to the URL with the token (the handshake). Vantigo answers
   it when the token is right. If Meta reports that the URL could not be verified, see
   [What can go wrong](#what-can-go-wrong).
3. Subscribe the app to the Page fields **messages**, **messaging_postbacks**,
   **message_reads**, **message_deliveries** and **message_echoes**. Vantigo also
   subscribes each Page to these when you add it.

## Add a Page

A **system user's token** that does not expire is the recommended way. A token from a
person's own login stops working when their password or permissions change.

1. In Meta's **Business Settings**, create a **system user** (**Users** › **System
   users**), and assign it the Page with the task to manage and message it.
2. Generate a token for that system user for the app, with the `pages_messaging`
   permission, and with **Never** as the expiry where Meta offers it.
3. In Vantigo, under **Pages**, paste the token in **Access token** and choose
   **Add Page**.

Vantigo asks Meta which Pages the token manages and keeps those whose tasks include
**MESSAGING**. With one, that Page is connected. With several, Vantigo asks you to
**Choose the Page to connect**: select one and choose **Add Page** again. Then it
subscribes the app to the Page's `messages`, `messaging_postbacks`, `message_reads`,
`message_deliveries` and `message_echoes`, and stores the **Page's own token**, sealed,
not the token you pasted. The Page is listed by its name and id, and no token is shown
again.

A Page's own access token pasted directly also works. Meta does not list Pages for such a
token, so Vantigo reads the Page from the token and checks that it may act for it by
subscribing the app; if Meta refuses that, the Page is refused.

**Remove** a Page to disconnect it. Its token, its queued events and their files are
deleted, and channels on the Page become unavailable. Vantigo also withdraws the app's
subscription from the Page, as far as Meta answers; a token Meta has rejected, or Meta
being down, does not keep the Page from being removed, and then the subscription stays at
Meta until you remove the app from the Page there.

**A rejected token.** When Meta answers a call with error 190 (the token is expired or
revoked, or the person's permissions changed), the Page shows **Token rejected** and its
channels show **Unavailable**; nothing is sent to it. The Page does not recover by
itself. **Add Page** again with a new token for the same Page.

## Verify

1. Look at **Status** on the Meta page: **Last webhook received** shows when Meta last
   posted a message with a valid signature (the time is updated at most once every 30
   seconds), **Last post with a wrong signature** the last refusal, and **Last error
   from Meta** the last refusal of a call to Meta.
2. In Communications, add a channel on **Facebook Messenger** for the Page.
3. From a Facebook account that has a **role on the app**, write to the Page. The
   conversation appears in Communications within a few seconds; reply to it and check
   that the answer arrives in Messenger.

## Retention and personal data

The module holds a person's Page-scoped ID (PSID), their name, the text of their
messages and the files they send, and only for these periods:

| What | Kept |
| --- | --- |
| Raw webhook events | 7 days |
| Inbox rows Communications has acknowledged, and their files | 7 days |
| Inbox rows never acknowledged, and their files | 30 days |
| A person's cached name | 30 days |

`meta-retention` deletes what is past its period once an hour. The module knows no
customer ids, so it cannot erase one customer's data on request; retention is its only
bound. Communications' own copies of a conversation are covered by its erasure
([Communications](/en/reference/communications/)). A participant row there outlives a
customer's erasure until the orphan sweep removes it.

## Workers

The module runs two workers, in the API process and in worker mode alike
([Workers](/en/admin/installation/)):

- `meta-split` turns each stored webhook event into inbox rows, and fetches the files a
  person sent from Meta's servers at once, because those addresses expire. It claims
  events under a lease and retries a failing one with a growing wait.
- `meta-retention` deletes what is past [its period](#retention-and-personal-data).

## The public host's routes

The public host serves three anonymous routes for Meta, exactly these paths
([The public host](/en/admin/payments/#the-public-host)); the proxy must pass all of them:

| Method | Path | Does |
| --- | --- | --- |
| `GET` | `/api/v1/meta/webhook` | Meta's handshake: answers `hub.challenge` when the verify token is right |
| `POST` | `/api/v1/meta/webhook` | Meta's events, up to 1 MiB |
| `GET`, `HEAD` | `/api/v1/communications/public-media/{token}` | A card's image, which Meta fetches when Communications sends a product as cards; one path segment, signed by its token, valid for 7 days |

The third is Communications', not this module's: the pictures of a product sent in a message
must be reachable from the internet, while the installation's storage has no public address.
It serves nothing but card images of messages sent on Messenger, and every other request is a
bare 404. A proxy that caches it may keep an image for up to a day (`Cache-Control: private,
max-age=86400`).

Every post is checked with `X-Hub-Signature-256`, an HMAC-SHA256 over the **raw bytes**
of the body with the app secret. A proxy that re-writes the body (compression,
re-encoding, a body filter) breaks the signature, and every post is refused with 401.

## What can go wrong

| Symptom | Cause and remedy |
| --- | --- |
| Meta cannot verify the callback URL | The verify token differs from Vantigo's, or was regenerated: copy it again from the Meta page. Or the proxy does not pass `GET /api/v1/meta/webhook` on the public host. |
| Meta's delivery log shows 401 | The app secret in Vantigo is not the app's current secret, or something between Meta and Vantigo changes the body. **Last post with a wrong signature** on the Meta page shows when it last happened. |
| Meta's delivery log shows 503 or 5xx | The database was slow or the settings are incomplete (no app secret). Meta retries; save the app secret. |
| Nothing arrives from a person | The app is in development mode and the person has no role on the app; Advanced Access to `pages_messaging` is missing ([above](#app-review-and-business-verification)); or the Page is not subscribed to the fields. |
| A reply fails, *the reply window has closed* | Messenger allows an answer only within 24 hours of the person's last message, seven days with Human Agent. |
| A reply fails, *recipient unreachable* | Meta cannot deliver to the person (they blocked the Page or cannot be reached through it). |
| A reply fails, *Human Agent is not granted to the app* | Meta refused a tagged send. Clear the checkbox on the Meta page, or get the permission from Meta. |
| A Page shows **Token rejected** | See [A rejected token](#add-a-page). |
| The settings cannot be saved | The instance has no public host. Set `PUBLIC_BASE_URL` and restart the server. |
| A file is missing from a conversation | Meta's file addresses expire; `meta-split` fetches the file at once, and gives up on it after repeated failures (outbound https to Meta's file servers is needed). |
| Adding a Page says *The token is neither a Page's access token nor a user's or system user's token…* | The token is expired or revoked, belongs to a user or system user who manages no Page with the **MESSAGING** task, or is a Page token Meta refused the subscription for. Assign the Page to the system user with the task to message it, generate the token again, or paste a valid Page token. |
| Adding a Page says *The token manages several Pages* | Select the Page to connect in the list that appears and choose **Add Page** again. Add each Page in turn. |
| Adding a Page says Meta did not answer | `graph.facebook.com` is unreachable from the server; try again in a while. |
