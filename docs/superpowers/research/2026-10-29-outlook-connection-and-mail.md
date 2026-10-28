# Outlook connection and Microsoft 365 mail — research

Date: 2026-10-29. For two things decided together: moving the calendar's Outlook sync
from the Entra connection's application permission to a **per-person delegated
connection**, and giving Communications its first inbound path through **Microsoft 365
shared mailboxes**. Read with [the calendar ↔ Outlook research](2026-10-27-calendar-outlook-sync.md)
(§2 compared the connection models and the owner then chose app-only),
[the Entra research](2026-10-19-entra-module.md) and the
[communications design](../specs/2026-09-13-communications-design.md) (§1.1: the
consequence of removing inbound).

## 1. The owner's direction (2026-10-28)

- **Mail is wanted in Communications**, as the calendar was in Outlook: shared mailboxes
  first, and later each person sending from their own account.
- **Personal access is delegated**, for the calendar too: a person clicks *Connect
  Outlook* once, and the features that need their mailbox use that connection. The
  reason given: a tenant-wide `Mail.ReadWrite`/`Mail.Send` application grant reaches
  every mailbox in the company, and narrowing it with Exchange's RBAC for Applications to
  a set of people who grows with every opt-in is ongoing admin work. Delegated access
  reaches only people who connected, and only what each of them can do.
- **Shared mailboxes stay application access**, scoped with RBAC for Applications: the
  set is small and rarely changes, so the scope is a one-time step.
- **No installation has calendar sync on**, so the application path is replaced, not
  migrated. Calendar delivery D (change notifications, #193) is merged; the delegated
  connection goes under it.

## 2. What exists today

### The Entra connection and its Graph client

- One app registration in the customer's tenant; client secrets in the plural, sealed
  under `entra/client-secret` (`internal/entra/credentials.go:33`); the token is client
  credentials with `https://graph.microsoft.com/.default`, cached process-wide per
  credential (`graph_token.go:75`, dropped five minutes before expiry).
- The guarded client (`graph.go`): netguard dialer, no proxy, no redirect, a 30 s budget,
  capped bodies (1 MiB; 8 MiB for a mailbox `$batch`; 16 MiB for a delta page), one wait
  on 429 (`Retry-After`, ≤ 60 s), one retry on 5xx, a dropped token and one retry on 401.
- **The token is coupled to a call in one place**: `send` asks `c.token(ctx, cred)` and
  drops it on a 401 (`graph.go:421`, `:456`); every mailbox call takes its credential
  from `mailboxSession.cred` (`mailbox.go:199-209`). A per-user token source fits there,
  and every guard above applies to it unchanged. The event and calendar markers use the
  credential's client id (`mailbox.go:248-256`) and must take the connection's instead.
- Today's mailbox rule (`mailbox.go:216-244`): the mailbox is Staff's
  `DirectoryAccount.ExternalID`, parsed as an object id, which must be a trusted row in
  `entra.accounts` (made by the module or a confirmed adoption). Otherwise
  `ErrMailboxUnknown`. A 403 is `ErrMailboxDenied`.
- The leaver's steps disable the account and then, with `LeaverRevokeSessions`, call
  `revokeSignInSessions` (`lifecycle_deactivate.go:73`, `:112`). Both end a delegated
  refresh token.

### The calendar's Outlook sync

- `contracts.MailboxCalendar` (`contracts/mailbox_calendar.go`): `Key`, `Status`,
  `EnsureCalendar`, `DeleteCalendar`, `Apply`, `Find`, `Delta`, `Marked`, `Versions`,
  `Events`, `Watch`, `Unwatch`, `Notified`. It is a single-provider slot (module
  boundaries rule 14) that Entra fills; the worker calls it under `contracts.AsUser`
  (rule 12).
- `calendar.mailbox_sync` (00081, 00083, 00084, 00086) holds the person's setting and the
  worker's state. The person's availability is `no_provider`, `switch_off`, `unavailable`
  or `no_account` (`mailbox_sync.go:256-286`).
- The Owner's switch is `entra.connection.calendar_sync` (00082). The connection test
  lists `Calendars.ReadWrite` as the conditional ninth permission (`connection.go:49`,
  `:93-105`). Subscriptions live in `entra.mailbox_subscriptions` (00085), one per
  mailbox on `users/{oid}/events`, with a `lifecycleNotificationUrl`, a hashed
  clientState and the anonymous webhook `POST /api/v1/entra/mailbox-notifications` on
  the public host.

### Identity's sign-in: the pattern for a browser round-trip

- OIDC sign-in is hand-written on `coreos/go-oidc/v3` and `x/oauth2`
  (`identity/oidc.go`). State, nonce and PKCE verifier are sealed into a Lax, HttpOnly
  ten-minute cookie (`identity/oidc-state`). The callback compares state and nonce in
  constant time, redeems with the verifier, and seals the validated identity into a
  second short cookie before a same-origin `complete` step.
- **The session cookie is `SameSite=Strict`** (`identity/cookies.go:101`), so it is not
  sent on Microsoft's cross-site redirect back. A connect callback cannot know the user
  from the session. It must carry the user in its own sealed state and finish with a
  same-origin request that does carry the session.
- SSO has its own client id and secret in `identity.sso_settings`. The admin guide allows
  one registration for both or two separate ones (`admin/entra.md:84`), and nothing ties
  them together. The Entra registration today has **no redirect URI** ("leave Redirect URI
  empty for now", `admin/entra.md:79`).
- A module mounts only under `/api/v1/<name>/`, every operation in its contract with
  `x-vantigo-access`. A redirect callback is an `anonymous` GET answering 302, as
  identity's is. Cross-origin protection guards every `/api` request; a safe GET passes.
- Secrets: one key per purpose from `APP_SECRET` (HKDF-SHA256); a new purpose is a
  `"<module>/<name>"` constant.

### Communications

- Outbound-only by the port's design. The schema already allows `direction='inbound'`
  (00006:166). `channels.provider` has no CHECK and defaults to `smtp`.
  `conversation_messages.rfc_message_id` (non-unique index), `raw_payload_storage_key`
  (never written) and `channel_metadata_json` (read as `{inReplyTo, references[]}` by
  the outbox) exist.
- **Missing for inbound**: a provider message id and a unique dedupe key (.NET's
  `inbound_receipts`: unique on channel + provider + event id, and partial unique on
  channel + provider + rfc message id); an inbound job or sync state; any per-channel
  delta or subscription state; HTML sanitisation (the frontend expects sanitised HTML
  from the API and renders it in a sandboxed iframe only when there is no text body;
  `go.mod` has no sanitiser); MIME parsing into messages, participants and attachments;
  threading by In-Reply-To/References; the contact/customer linker
  (`conversation_customer_candidates`, `participants.contact_id`,
  `customer_association_source='automatic'` are all unwritten).
- **Reply is permanently negative**: `resolveReplyRecipients` needs the latest inbound
  participant (422 `recipients_missing`); `ccAddresses` is nil; `queueReply` writes no
  threading metadata; `replyRecipientsOf` returns a constant. The AI draft reads the 20
  newest inbound text bodies, and there are none.
- **Channels are SMTP-only in code**: `validateCreateChannel` and the update refuse any
  other provider (`channels.go:241`, `:469`). The insert hardcodes `smtp` (`:338`).
  `verifyChannel` refuses non-SMTP (`:724`). The DTO's `settings` requires six SMTP and
  Mailgun keys (`openapi/communications.yaml:125-175`). `ChannelForm.tsx` still offers a
  dead Mailgun branch.
- **The outbox always sends via SMTP**: `deliver` builds a `mail.Outbound` (Message-Id
  `hex(id)@vantigo.invalid`, In-Reply-To, References, To/Cc/Bcc, attachments with
  Content-ID) and calls `Deps.SMTPSend` or `mail.SendOutbound`. The query does not select
  the channel's provider.
- Retention already queues each `raw_payload_storage_key` on the cleanup ledger. An
  inbound message has no outbox job, so it ages out by `created_at` alone. The personal
  data export and erase key on `conversations.customer_id`: an inbound conversation with
  no customer is invisible to both.
- Permissions: `communications:channels-manage`, `conversations-manage`,
  `conversations-reply`, `conversations-view` (sensitive), `suppressions-manage`.

## 3. Microsoft facts the design rests on

Read on learn.microsoft.com on 2026-10-28. Items marked *unverified* need a check in a
dev tenant before the delivery that rests on them.

### The authorization code flow (confidential client)

- `/authorize`: `client_id`, `response_type=code`, `redirect_uri`, `scope`; `state` and
  `code_challenge` (`S256`) recommended. PKCE is recommended for confidential clients
  too. `/token`: the code, the verifier and the client secret (or an assertion). The code
  lives about a minute. A refresh token comes back only when `offline_access` is asked
  for. One `/token` request's scopes belong to one resource.
  ([auth code flow](https://learn.microsoft.com/en-us/entra/identity-platform/v2-oauth2-auth-code-flow))
- **Admin consent for the organisation** grants delegated permissions for every user.
  They then see no prompt.
  ([permissions and consent](https://learn.microsoft.com/en-us/entra/identity-platform/permissions-consent-overview))
- **Incremental consent**: delegated permissions may be asked at sign-in, one at a time.
  A refresh token is valid for every permission the client already has consent for. That
  a new refresh token covers the union is implied, not stated (*unverified*).

### Refresh tokens

- 90 days for a web app ("replace themselves with a fresh token upon every use"). Whether
  the 90 days slide is not stated; AADSTS70008/700082 name inactivity expiry.
  ([refresh tokens](https://learn.microsoft.com/en-us/entra/identity-platform/refresh-tokens))
- **The old token is not revoked when redeemed.** The client is expected to discard it.
  Two workers redeeming the same token at once both succeed.
- **Revoked** by: the user revoking their sessions, an administrator's
  `revokeSignInSessions`, a password reset in the Entra or Microsoft 365 admin centre.
  **Survives**: the user's own password change, password expiry, SSPR. A disabled account
  gets no new token. Continuous Access Evaluation enforces disable, delete, password
  reset, admin revoke and high risk within about 15 minutes, as a 401 with a claims
  challenge.
  ([CAE](https://learn.microsoft.com/en-us/entra/identity/conditional-access/concept-continuous-access-evaluation))
- **Conditional Access is evaluated at every refresh**, with the server's address as the
  location. The codes a refresh can answer, none of which a server can resolve:
  50076 (MFA required), 50079 (MFA registration), 53003 (blocked), 53000 (device not
  compliant), 70043 (sign-in frequency), 50173 (grant revoked), 70008 (expired), 65001
  (no consent), `interaction_required`.
  ([error codes](https://learn.microsoft.com/en-us/entra/identity-platform/reference-error-codes))
  Token Protection was not checked (*unverified*).

### Change notifications and delta, delegated

- Delegated subscriptions are supported on `users/{id}/events` (`Calendars.Read`) and on
  `users/{id}/messages` or `users/{id}/mailFolders/{id}/messages` (`Mail.ReadBasic`), for
  **the signed-in user's own mailbox**. The `*.Shared` permissions do **not** support
  subscriptions: a shared mailbox's notifications need the application permission.
  ([Outlook change notifications](https://learn.microsoft.com/en-us/graph/outlook-change-notifications-overview))
- Lifetime: ≤ 10 080 minutes for Outlook messages and events without resource data.
  `lifecycleNotificationUrl` is set at creation only. `reauthorizationRequired`,
  `subscriptionRemoved` and `missed` apply. Renew with `PATCH expirationDateTime`, which
  also reauthorises, and not within ten minutes of a reauthorize. The `creatorId` of a
  delegated subscription is the user. That a renewal needs that same user's token is not
  stated, so assume it.
  ([lifecycle](https://learn.microsoft.com/en-us/graph/change-notifications-lifecycle-events))
- Delta: `calendarView/delta` needs `Calendars.Read` delegated or application alike.
  `mailFolders/{id}/messages/delta` is per folder, `$filter` only on `receivedDateTime
  ge/gt`, and needs `Mail.ReadBasic` (delegated) or `Mail.ReadBasic.All` (application).
  No difference found beyond the permission names.

### Sending

- `sendMail` needs `Mail.Send` (delegated or application) and saves to Sent Items by
  default. `Mail.ReadWrite` is not needed. It answers 202, which means accepted, not
  delivered. ([sendMail](https://learn.microsoft.com/en-us/graph/api/user-sendmail))
- Attachments up to 3 MB go inline in JSON. 3–150 MB need a draft and an upload session,
  which need `Mail.ReadWrite`.
  ([large attachments](https://learn.microsoft.com/en-us/graph/outlook-large-attachments))
- `sendMail` also takes **a MIME message, base64-encoded, as `text/plain`**. That keeps
  our own Message-Id, In-Reply-To and References, which the JSON form only allows as
  `X-` headers. The MIME form's size limit and whether Exchange keeps our Message-Id are
  *unverified*: check both before delivery C.

### Shared mailboxes and RBAC for Applications

- Delegated access to a shared mailbox needs `Mail.ReadWrite.Shared` and the person's own
  Full Access. The channel would then live or die with that one person's token, and it
  cannot subscribe. **Application access is the only sound model for a channel.**
- RBAC for Applications roles: `Application Mail.Read`, `Application Mail.ReadWrite`
  (no send), `Application Mail.Send`, `Application Mail Full Access` (read/write and
  send). Steps: `New-ServicePrincipal`, `New-ManagementScope` (filters such as
  `CustomAttribute1 -eq '…'` or `MemberOfGroup -eq '<DN>'`, direct members only),
  `New-ManagementRoleAssignment -App … -Role … -CustomResourceScope …`,
  `Test-ServicePrincipalAuthorization`. The cache takes 30 minutes to 2 hours. **The
  grants are a union with Entra's**: a tenant-wide `Mail.*` consent defeats the scope.
  That a shared mailbox matches a `CustomAttribute` filter follows from it being a
  recipient, but is not stated: check it with `Test-ServicePrincipalAuthorization`.
  ([application RBAC](https://learn.microsoft.com/en-us/exchange/permissions-exo/application-rbac))

### Throttling and SMTP AUTH

- Outlook: 10 000 requests per 10 minutes per app and mailbox, 4 concurrent, 150 MB of
  uploads per 5 minutes. `$batch` runs four at a time against Outlook. One mailbox's
  throttling does not touch another's.
  ([throttling](https://learn.microsoft.com/en-us/graph/throttling-limits))
- **SMTP AUTH basic auth**: the March–April 2026 removal was withdrawn. Per Message Center
  MC786329 (secondary sources, *unverified* first-hand), it stays on until the end of
  December 2026, is then off by default for existing tenants (an admin may turn it back
  on) and unavailable for new ones, and a final date comes in the second half of 2027.
  An SMTP channel on Exchange Online has about two months before it needs an admin's
  exception. That is a reason to ship the Graph channel soon.

## 4. What other products do

- **CRMs and help desks** (HubSpot, Pipedrive, Zendesk, Freshdesk, Front): a person
  connects their own mailbox with a delegated OAuth click, and a team inbox is either a
  forwarding address or a connected shared mailbox. Forwarding loses Sent Items and the
  thread's own history. A connected shared mailbox keeps both.
- **Nordic ERPs** (Tripletex, PowerOffice, Visma): no mail sync. Inbound supplier
  invoices arrive at a fixed scanning address. The `faktura@` mailbox as a Communications
  channel that hands attachments to supplier invoices is the gap these leave.

## 5. Recommendations

1. **One Outlook connection per person, owned by Entra.** It holds the sealed refresh
   token, asks for scopes incrementally (the calendar's first, later `Mail.Send`), and
   sits behind the existing slots, so the calendar does not learn about tokens. The same
   app registration, with a web redirect URI added; admin consent for the organisation,
   so a person's click is one redirect that SSO usually answers silently.
2. **Keep the certain-mailbox rule.** The connected account's `oid` must be the account
   Staff names for the person, and `tid` the connection's tenant. A person who signs in
   with another account is refused, never re-pointed.
3. **Any refresh failure is "reconnect", never "failing".** Store the code, show
   *Reconnect Outlook* to the person, and keep the rest of Vantigo working. Treat a CAE
   claims challenge on a 401 the same way.
4. **Shared mailboxes as Microsoft 365 channels on application access**: Entra fills a
   new slot for channel mailboxes; Communications owns the channel, the sync state, the
   import and the threading. Send as MIME, so Message-Id and threading stay ours.
5. **Report a tenant-wide `Mail.*` grant** when the connection test sees it. With RBAC the
   token carries no mail role, so for mail the token's role is the warning sign, not the
   permission missing.
6. **Re-add inbound in full, not halfway**: dedupe receipts, raw payload, MIME parsing,
   sanitised HTML, threading, attachments, the linker. That makes reply, reply-all and the
   AI draft live at once.
