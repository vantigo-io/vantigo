> **Deferred (the owner, 2026-11-01).** Do not start this brief. The hosted mail service needs its own research and design before anything here is built; see the spec's T8 banner and delivery C′. What ships meanwhile is [`c-hosted-mail-placeholder.md`](c-hosted-mail-placeholder.md), the greyed entry. This text is kept as the design to pick up; when it is, the brief must be re-read against the spec and against what B and the placeholder landed.

# Brief C′ — Vantigo hosted mail (deferred)

You are implementing delivery **C** of the design
`docs/superpowers/specs/2026-10-31-integrations-and-configuration-tiers-design.md`
(decisions **T8, T9, T10's consumer side, T15**). Read that file first, in full, then
this brief. Delivery **B** must already be on `main` (grep `OutboundMail` in
`apps/server/internal/contracts`; if it is absent, stop and report). You work alone,
from a cold start, in this repository. Model: Claude Sonnet 5.5.

## The outcome

An instance whose environment names the mail service (`MAIL_SERVICE_URL`,
`MAIL_SERVICE_KEY`) offers a fourth channel provider, **Vantigo hosted mail**
(key `hosted`). A hosted channel shows its sending domain, its mode (production or
test with the listed recipients) and its usage, all read from the service and none
editable. A send to a disallowed address in test mode is refused loudly, never dropped;
a quota answer leaves the message queued with the service's `Retry-After`; a repeat
carries the same `Message-ID` and is neither sent nor billed twice. The service's
delivery events arrive on a signed webhook and mark messages failed. The system mailbox
can use the same service through `MAIL_DRIVER=vantigo`. The service itself
(`vantigo-mail`) and the `vantigo-infra` change are **other repositories**: you build
the consumer side against the contract in T10 and a fake.

## Read before you plan

- `AGENTS.md`, `CONTRIBUTING.md`, `docs/src/content/docs/en/contributing/module-boundaries.md`,
  `en/contributing/platform-packages.md`, `en/admin/transport-security.md` (the
  private-address exception).
- The spec's *Readings*, T7–T10, T15; the spot-price service design S3, S7, S9 for the
  shared-service pattern (`docs/superpowers/specs/2026-10-26-energy-spot-price-service-design.md`)
  and its client, `apps/server/internal/energy/spot_price_service_client.go`; the
  Outlook design M13–M14 for how a provider's send and notices were shaped.
- `apps/server/internal/config/config.go` (`ENERGY_SPOT_PRICE_SERVICE_*` loading and
  validation, `MAIL_DRIVER`, `MailConfig`), `config/redact.go`, `mail/mail.go`
  (`Sender`, `New`), `mail/outbound.go`, `netguard/` (`DialContextAllowingPrivate`).
- Communications after B: `contracts/outbound_mail.go`, `channels.go`, `channel_providers.go`,
  `outbox.go` (the per-provider send), `contractscalls.go`, the entra webhook
  `mailbox_notifications_endpoint.go` in `srv/entra` and how it is listed as public
  (grep `mailbox-notifications` in `srv/server` and `srv/web`), `ratelimit` policies,
  `openapi/communications.yaml` (`ChannelResponse`, `ChannelProviderResponse`, the
  provider enum).
- `apps/communications/frontend/src/pages/admin.channels.tsx` and `components/ChannelForm.tsx`.
- `apps/server/internal/entra/graphtest/` — the fake-service pattern your fake follows.
- Docs: `en/admin/installation.md`, `en/admin/transport-security.md`,
  `en/admin/configuration.md` (from A; if A is not merged, add the settings to
  `en/admin/authentication.md`'s configuration list instead and say so),
  `en/user/communications.md`, `en/reference/communications.md`, and the `nb/` twins.

## Build

1. **Config**: `MAIL_SERVICE_URL` (same shape and checks as the spot-price URL) and
   `MAIL_SERVICE_KEY` (required with the URL, ≤ 200 printable bytes, redacted);
   `MAIL_DRIVER` accepts `vantigo` (requires the two settings; `SMTP_FROM` still
   required as the system address; `SMTP_HOST` not required under it).
2. **Platform package `srv/hostedmail`**: a client with S7's guard (private-address
   exception only when the URL is `http`, host pinned, no proxy, no redirects, 1 MiB
   answer cap, keep-alives, `Authorization: Bearer`, `User-Agent: vantigo/<version>`,
   30 s per send); `Tenant(ctx)` (`GET /api/v1/tenant`), `Send(ctx, purpose, mime,
   messageID)` (`POST /api/v1/messages`, `Idempotency-Key`, `X-Vantigo-Purpose`);
   typed errors: `ErrRecipientNotAllowed{Recipients}`, `ErrTooLarge`,
   `ErrRateLimited{RetryAfter}`, `ErrUnauthorized`, a transport failure. Failure
   sentences name the service and never quote a body or the key. No SQL, no module
   import. Document it in `platform-packages.md`.
3. **`MAIL_DRIVER=vantigo`** in `mail.New`: a `Sender` on the client with purpose
   `system`. Identity's and others' `Driver == "smtp"` gates become `Driver != "log"`
   (grep for them; list each in the plan).
4. **The provider** in Communications: key `hosted`, listed by `GET /channel-providers`
   while the client is configured (`displayName` "Vantigo hosted mail", `maxMessageBytes`
   from `Tenant`); a hosted channel has no credential row and no mailbox id; its
   address must be under `sendingDomain` (422 with a code when not). `ChannelResponse`
   gains, for this provider, `hosted: {sendingDomain, mode, testRecipients, usage{sent,
   allowance, periodEnd}}`, read from `Tenant` through a one-minute cache. The outbox
   sends with purpose `channel`, the message's `Message-ID` as the key; maps
   `ErrRecipientNotAllowed` to a failed message with the fixed sentence, `ErrRateLimited`
   to a retry at `Retry-After` with the channel showing the quota sentence, `ErrTooLarge`
   to the existing too-large outcome. `OutboundMail.Send` answers `ErrRecipientRefused`
   for the first and a retryable failure for the second.
5. **The events webhook** `POST /api/v1/communications/hosted-mail-events` on the public
   host's list: body `{messageId, event, reason, at}`, `X-Vantigo-Signature` =
   HMAC-SHA256 over `timestamp "." body` under the service key, `X-Vantigo-Timestamp`
   within five minutes, constant-time compare, rate limit `communications-hosted-events`
   600/min; a `bounced` or `complained` message is marked failed with the reason and a
   hard bounce blocks the address as *Block an address* does, attributed to the
   service; unknown ids answer 202 and are logged at debug.
6. **The channels page**: the provider in the form's radio, the hosted card's facts
   (domain, mode with the test list in an amber alert, usage with the period end), the
   address validation message; nothing editable among the facts.
7. **Documentation**, en and nb: `admin/configuration.md` (the three settings),
   `admin/installation.md` (*Email and observability*: the system mailbox on the
   service), `admin/transport-security.md` (the exception's second caller), a new
   `admin/hosted-mail.md` (what Vantigo provisions, test mode and the list, moving to
   production is Vantigo's act, rotating the key, the events webhook on `APP_URL`, what
   can go wrong with each sentence), `user/communications.md` (*Add a Vantigo hosted
   channel*), `reference/communications.md` (the provider, the fields, the webhook, the
   codes), `platform-packages.md` (`hostedmail`), `ROADMAP.md`.
8. **Tests** against a fake service (`hostedmailtest`, the `graphtest` shape, no real
   socket): a test-mode refusal and its sentence on the message; the same `Message-ID`
   twice reaching the fake once; 429 with `Retry-After` honoured; a 401 sentence
   without the key; the webhook's signature, window and replay; `MAIL_DRIVER=vantigo`
   sending an invitation; the URL `http` to a public address refused at boot; the
   channel form refusing an address off the domain.

## Do not

Build the service or the infra change (other repositories; write what they must
provide in the plan's *Companion changes* section, from T10) · receive mail (T10
reserves `inbound`) · let the Owner edit domain, mode, list or allowance · show the
key anywhere · bypass `netguard` beyond S7's exception.

## How to work

- Worktree on `feat/hosted-mail` cut from `main` after B landed;
  `bun install --frozen-lockfile` first; `mise run server:db` for the test database.
- **Plan first**: `docs/superpowers/plans/2026-10-31-hosted-mail.md` in the house shape
  (`> For agentic workers`, **Goal**, **Architecture**, **Spec**, *Plan readings*,
  *Tasks* as checkboxes, plus *Companion changes* for `vantigo-mail` and
  `vantigo-infra`). Commit it alone with `Docs-Impact: none — plan only`.
- No migration is expected; if you find you need one, take the next free number and
  say why in the report.
- One commit per task, Conventional Commits, SSH-signed through 1Password; if signing
  fails, stop and report — never `--no-gpg-sign` or `--no-verify`.
- Gates: `mise run server:check`, `mise run server:test`, `mise run frontend:check`,
  `mise run docs:check`. The Node 26 quirk applies to `apps/projects/frontend` only.
- No push, no pull request, no merging `main` into the branch.

## Report

Branch and `git log --oneline main..`; gates verbatim; the plan's path; the exact
contract you built against (so the service repository can be checked against it);
departures from the spec; the manual check: point `MAIL_SERVICE_URL` at the fake or
staging, create a hosted channel, send to a listed and to an unlisted address.
