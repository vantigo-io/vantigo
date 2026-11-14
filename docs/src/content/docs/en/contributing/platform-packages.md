---
title: Platform packages
description: The packages under the server's internal/ that every module may use — the secrets box that seals and MACs values under keys derived from APP_SECRET, the mail package's outbound message, its calendar part and its rendering as MIME, its reading of a received message and the sanitising of its HTML, the AI client with its model-per-task seam and its chat, the operations dispatcher the assistant calls the API through, the payments port with its Vipps MobilePay adapter, the bank feed port with its Folio adapter, the public-holiday calendar, the image metadata stripper, the address rule a directory provider makes an account's address by, and live updates — the topics a module serves, notifying on the transaction that writes, the endpoint and its station path, presence and the limits, and the binary frames and document topics that carry a collaboratively edited document.
sidebar:
  order: 5
sources:
  - apps/server/internal/secrets
  - apps/server/internal/mail
  - apps/server/internal/ai
  - apps/server/internal/operations
  - apps/server/internal/payments
  - apps/server/internal/bankfeed
  - apps/server/internal/holidays
  - apps/server/internal/imagemeta
  - apps/server/internal/directoryaddress
  - apps/server/internal/events
  - apps/server/internal/live
  - apps/server/internal/docmath
---

A module may import the platform but never another module (see
[Module boundaries](/en/contributing/module-boundaries/)). This page describes the
platform packages a module reaches for when it holds something it must not store in
clear, when it sends a mail of its own, when it asks an AI model for something, when it takes a payment, when it reads a bank account's transactions, when it counts days
around the public holidays, when it stores a picture people will see, when it makes an address for an account in a directory, when it tells other modules what happened to one of its own rows, and when it computes a sales document's amounts.

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
| `communications/messenger-ref` | the MAC in the ref of a contact's Messenger link, `v1.<contactId>.<channelId>.<expiresAt>.<mac>`: it makes the link unforgeable and says which contact and channel it was made for ([contacts on Messenger](/en/reference/communications/#contacts-on-messenger)) |
| `invoices/access-point-credential` | the e-invoicing access point's API key |
| `invoices/bank-feed-credential` | the bank feed's API key — Folio's today — sealed with its last four characters kept beside it in clear ([the bank feed](/en/reference/invoices/#the-bank-feed)) |
| `calendar/rsvp-link` | an outside attendee's answer link, sealed so every later mail carries the same link ([the answer by link](/en/reference/calendar/#the-token-and-the-link)) |
| `calendar/feed-address` | a person's calendar address, sealed so the settings screen can show it again; the request is matched by the token's plain hash ([the feed](/en/reference/calendar/#the-address-and-its-two-variants)) |
| `calendar/feed-busy-uid` | the MAC of a person's id and a projected item's kind and id, cut to 32 hex digits, as that item's `UID` in the busy feed — opaque, stable per person and item, and never stored, so a rotation changes the `UID`s rather than orphaning anything ([the feed](/en/reference/calendar/#the-icalendar-object)) |
| `staff/national-id` | a person's national identity number (sealed) |
| `staff/national-id-index` | the MAC of a person's national identity number, in a unique column |
| `staff/bank-account` | a person's bank account (sealed) |

**A purpose either seals or MACs, never both**, so no key both encrypts and
authenticates. A MAC purpose that indexes a value carries the suffix `-index`: a module
that seals a value and indexes it uses two purposes, `module/purpose` for the sealed value and
`module/purpose-index` for its MAC — `staff/national-id` and
`staff/national-id-index` — and pins the two constants distinct in a test.
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

## The mail package

[`apps/server/internal/mail`](https://github.com/vantigo-io/vantigo/tree/main/apps/server/internal/mail)
sends the installation's own mail through the configuration of
[Email delivery](/en/admin/authentication/#email-delivery) — an SMTP server, or
Cloudflare Email Service's REST API when `MAIL_DRIVER=cloudflare` — behind its guard
against private destinations. `mail.SendOutbound` takes the driver from the
`config.MailConfig` it is given: Staff's notices follow the installation's driver, and a
Communications channel's configuration is always `smtp`. `mail.Message` is identity's three-field application mail;
`mail.Outbound` is a full message — recipients, both bodies, a display name,
Reply-To (`ReplyTo` and `ReplyToName`), threading headers and attachments — which Communications sends through each
channel's own SMTP configuration — and, through `OutboundMail`, for Invoices' documents
and Calendar's invitations, from the channel an Owner chose — and Staff through the
installation's own, the system mailbox's, by the seam `Deps.SMTPSend` (or
`mail.SendOutbound` when it is unset).

`mail.RenderMIME(from, out, opts...)` renders an `Outbound` as the RFC 5322 message
`SendOutbound` would send from `from` — the same headers, the `Message-ID`, `In-Reply-To`
and `References` the caller set, the same parts — and answers its bytes instead of
dialling, for a provider that takes a message whole rather than speaking SMTP:
Communications' [Microsoft 365 channels](/en/reference/communications/#delivery-semantics),
whose bytes Entra posts to Graph. It refuses an `Outbound` with no recipient, as
`SendOutbound` does. **The bytes carry no `Bcc:` header** unless the caller passes
`mail.WithBcc()`. go-mail's writer (v0.8.1) never writes one: over SMTP the blind copies
travel in the envelope's `RCPT TO`, never in the message. A provider that reads its
recipients from the headers has no envelope, so it would drop them; `WithBcc` adds the
header once, the addresses comma-joined as go-mail renders them, for a provider that
reads it — Graph's MIME draft — while the caller also hands the blind copies over beside
the bytes. Without blind copies the rendering has the headers and parts of the message SMTP
sends.

`Outbound.Calendar`, a `*mail.CalendarPart{Method, Body}`, carries an iCalendar object
the iMIP way: as the **third alternative** after the text and the HTML,
`text/calendar; method=<Method>` (go-mail completes it with `charset=UTF-8`), and
attached again as `application/ics` — `invite.ics` for a `REQUEST`, `cancel.ics` for a
`CANCEL`, the only two methods it admits. `Method` must be the `METHOD` the body
declares; the body is the whole `VCALENDAR`, CRLF line endings and all, carried as
given. Calendar's invitations are the one user
([invitations](/en/reference/calendar/#the-message)).

**Reading received mail.** `mail.ParseInbound(raw, limits)` reads an RFC 5322 message
into a `mail.Inbound`: `MessageID` and the first `In-Reply-To` bracketed, `References`
bracketed, distinct and oldest first (at most 100, the first and the newest kept; each id
header read for at most 400 ids, and an id with a control character counts as missing),
the `From`, `To` and `Cc` addresses in lower case (one over 320 characters or with a
control character dropped, at most 100 each in `To` and `Cc`), the subject RFC 2047-decoded (at most 998
bytes), `AutoSubmitted` (`Auto-Submitted` other than `no`, `X-Auto-Response-Suppress`,
`Precedence` `bulk`/`auto_reply`/`list`, `X-Autoreply`), the text and HTML bodies in UTF-8,
**unsanitised**, and the attachments with their file names, types, `Content-ID`s and
whether they are inline. It is built on `net/mail` and `mime/multipart`, with
`quoted-printable`, tolerant `base64`, RFC 2231 file names in any charset — which
`mime.ParseMediaType` drops for a charset other than UTF-8 or US-ASCII, so the package
reads those parameters itself — and every charset of `golang.org/x/text`'s `htmlindex`.
`mail.InboundLimits` bounds it — `DefaultInboundLimits` is 200 parts, 10 levels, 100
attachments and 1 MiB for each body (a longer text cut with `…`, a longer HTML dropped and
`HTMLDropped` set); a damaged part is skipped and counted; no header block, or neither a
`From` nor a body, is `mail.ErrUnparseable`, whose text holds nothing of the message.
A forwarded `message/rfc822` and a `winmail.dat` are kept as attachments, not opened; a
part with no `Content-Type` is `text/plain`; no string of the result holds a NUL.
`mail.NormalizeMessageID` is the bracketed, trimmed form both the parser and a caller
compare ids in.

`mail.SanitizeHTML(html, inlineURL)` answers a received message's HTML safe to show, on
[bluemonday](https://github.com/microcosm-cc/bluemonday)'s UGC policy after a pre-pass over
`golang.org/x/net/html`'s tree: `cid:` images rewritten through `inlineURL` (removed when
it answers none), every other image but a base64 `data:` PNG, GIF, JPEG or WebP removed,
`background` and `srcset` dropped, links and image map areas only `http`, `https` and
`mailto` — no relative URL kept but the inline image's download path, carried through
the policy as a `vantigo-inline:` placeholder —, links with
`rel="nofollow noreferrer noopener"` and `target="_blank"`, and a short allow-list of
typographic and box styles — never `url(…)`, `position` or `expression`. `<script>`,
`<style>`, `<iframe>`, `<object>`, `<embed>`, `<form>`, `<input>`, `<meta>`, `<base>`,
`<link>`, `<svg>`, `<math>` and every `on*` attribute go. `mail.TextFromHTML(sanitized)`
is a plain text of it — blocks and `<br>` as line breaks, list items as `- `, white space
collapsed — for a message that has only HTML. Communications'
[receiving](/en/reference/communications/#receiving) is the one user. The package's corpus
of hand-built messages is `testdata/mime/`, each with its expected reading beside it.

## The AI client

[`apps/server/internal/ai`](https://github.com/vantigo-io/vantigo/tree/main/apps/server/internal/ai)
is the one client for the AI provider the installation is configured with, and the one place
its key lives. A module receives it as `Deps.AI` (an `*ai.Client`, nil when no provider is
configured) and calls it with a **task key**, never with a model or a key. It has no SQL and
imports no business module (depguard's `platform` list), and its request and response types
(`ai.Request`, `ai.Response`, `ai.ChatRequest`, `ai.ChatResponse`) are provider-neutral, so a
second provider is one file beside `openai.go` and one word in `config`: `anthropic.go` is the second, on the official Go SDK.

- **Configuration** is the instance's: `AI_PROVIDER`, `AI_API_KEY`, `AI_BASE_URL`, `AI_MODELS` and
  `AI_TIMEOUT` ([Configuration](/en/admin/configuration/#ai)). `ai.New(cfg, transport, resolver)`
  answers nil unless a provider, a model, and a key or a base URL are configured: a key is
  optional once `AI_BASE_URL` names an endpoint, because a local model takes none (no
  `Authorization` header is sent then).
- **`Complete(ctx, task, req)`** resolves the task's model through a `Resolver`
  (`ModelFor(ctx, task)`; `contracts.AIModels`, which identity fills with an Owner's choices,
  satisfies it, and a nil resolver gives every task `AI_MODELS[0]`), **refuses a model outside
  `AI_MODELS`** before any request is made, and calls the provider. The answer names the
  provider and the model it used, also on an error, so a consumer's audit row can say which
  model it asked. `ModelFor(ctx, task)` on the client answers the same model without calling,
  for a consumer that records a call it refused first.
- **`Chat(ctx, task, req, onDelta)`** is the second door, for a conversation. `ai.ChatRequest` is a
  system message, typed messages (`ai.RoleUser`, `ai.RoleAssistant` with its `ToolCalls`,
  `ai.RoleTool` answering a `ToolCallID`) and `ai.Tool`s described by a JSON Schema, with an
  optional `MaxTokens`. The model is resolved and allowed exactly as `Complete`'s is. The answer
  **streams**: `onDelta` (it may be nil) receives each text delta as it arrives, from the
  goroutine that called `Chat`, and the returned `ai.ChatResponse` carries the assembled
  assistant message with its tool calls, the provider's finish reason and the token counts.
  The implementation is the OpenAI-compatible Chat Completions API with `stream: true` and
  `stream_options.include_usage`, read as server-sent events up to `[DONE]`, so one
  implementation serves OpenAI, Ollama, vLLM, LM Studio and Azure OpenAI's compatible
  endpoint. It tolerates what they differ in: a server that reports no usage (the counts are
  then nil), one that sends a whole tool call in a single chunk, and one that numbers every call
  of a parallel batch from 0 (a changed call id at an index starts a new call). A tool call's
  `Arguments` is always valid JSON: text the model sent that is not JSON is carried as a JSON
  string, so a caller can tell the model it did not send an object. A stream that ends before
  `[DONE]` is an `aiDecodeError`; half an answer is never handed on. The budget is `AI_TIMEOUT`
  (default 120 seconds) for the whole call, because local models are slow; `Complete` keeps its
  30. The token limit is sent as `max_completion_tokens` to OpenAI's own endpoint and as
  `max_tokens` to any other, which every compatible server understands.
- **Anthropic** (`AI_PROVIDER=anthropic`, `anthropic.go`) is the second provider, over the official Go SDK
  (`github.com/anthropics/anthropic-sdk-go`), for both `Complete` (one user message, not streamed) and `Chat`
  (streamed, tools). Its settings are `AI_EFFORT` (sent as `output_config.effort`) and `AI_FALLBACKS`
  (`fallbacks: "default"` under beta `server-side-fallback-2026-07-01`, not for Haiku), which the other providers
  ignore. Thinking stays adaptive and is never configured, disabled or budgeted, and no sampling parameter, prefill or forced
  tool choice is sent. The tools and the system block form the stable prefix, the system block marked for caching.
  Tools are sent with `eager_input_streaming`, so **every tool input is validated against its schema** before `Chat` returns it; one
  that is invalid reaches the caller as a JSON string, like text that was no JSON. `max_tokens` and `refusal`
  are the typed errors `aiMaxTokensError` and `aiRefusalError` (`ai.IsMaxTokens`, `ai.RefusalCategory`).
  `ChatRequest.ToolsDisabled` asks for an answer without tool calls while keeping the tool set, which Anthropic needs
  and the others satisfy by sending none. Thinking text is never returned.
- **`Message.Provider`** is an opaque `json.RawMessage` on the neutral type: for an Anthropic assistant turn,
  the turn's content blocks exactly as received (thinking blocks with their signatures included), returned in
  `ChatResponse.Message`. A caller stores it with the turn and hands it back in the next request, and `Chat` replays it verbatim in place of `Text` and
  `ToolCalls`, because a thinking block is valid only unchanged. The other providers leave it empty and ignore it. Every request sends
  `thinking.block_binding.prefix_mismatch_behavior: "drop_block"`, and each `input_transformations` entry in a response
  is logged at warn (type, reason, path) through the logger `ai.WithLogger` gives the client. The history rules that make a block valid are in
  [the Assistant's reference](/en/reference/assistant/#claude-and-preserved-thinking).
- **The guard** every outbound client has: redirects are never followed (a redirect would send
  the key to a host nobody named), the transport is `Deps.HTTPTransport` (nil in production,
  meaning `http.DefaultTransport`; a fake in every test, so no test opens a socket), a 30 second
  budget bounds a `Complete` call, and a response is read up to 4 MiB (a streamed answer up to 8). Over the SDK the guard is the same: it is handed the package's own `http.Client` (redirects refused, the body bounded) over `Deps.HTTPTransport`, its retries are off because the caller owns retrying, it reads no `ANTHROPIC_*` environment variable or profile file, and the context carries the budget.
- **Typed errors**, whose type names are the labels a consumer may record (Communications writes
  them to `ai_interactions.error_summary`) and never a message, which could carry provider or
  customer text: `aiTransportError` (it keeps its cause for `errors.Is`, so a cancelled caller
  can be told from an outage), `aiStatusError`, `aiDecodeError`, `aiEmptyResponseError`,
  `aiModelUnavailableError` and `aiModelNotAllowedError`.
- **Tests** drive the real client through a fake `http.RoundTripper` — the provider is the
  boundary, so the network is what is replaced, never the client, which would leave a seam that
  keeps passing after the implementation is deleted. `modtest.WithAIModels` pins the model per
  task in a module's tests.

Which tasks exist is declared by the modules (`Module.AITasks`) and which model each uses is an
Owner's choice that identity keeps: see [the AI platform](/en/contributing/module-boundaries/#the-ai-platform).

## The operations dispatcher

[`apps/server/internal/operations`](https://github.com/vantigo-io/vantigo/tree/main/apps/server/internal/operations)
is how the Assistant reads and changes things: it calls the operations of the merged contract
in-process, through the composed router, as the person the assistant acts for, or as an agent. It is not
available to a module. `module.Compose` hands it, as `Deps.Operations`, to the modules named
`assistant` and `agents` alone, and depguard refuses every other import ([module boundaries rule
18](/en/contributing/module-boundaries/), which also says how a call is the person's own).

- **Building it.** `operations.New()` is unbound; `Bind(contract, catalog, access, handler,
  opts)` loads the merged contract (the body of `GET /api/openapi.json`), computes every
  operation's **mode** once and keeps the access and the composed handler. Compose does both,
  so the dispatcher is for use while serving, never while mounting.
- **`Mode`** is `auto`, `confirm` or `never`. The **contract's mode**, computed once at `Bind`, is
  the operation's `x-vantigo-assistant`; else `never` for an `anonymous`, `scim` or `session` rule,
  an Owner-tier policy, a permission missing from the catalog, a file upload or download or the
  assistant's own API; else `auto` for `GET` and `confirm` for every other method. It is the widest
  an operation can ever be, and it no longer looks at `Sensitive`: that moved into the gate.
- **`Info`** describes one operation: `ID`, `Method`, `Path`, `Tag`, `Summary`, `Description`,
  `Mode`, `Permissions` — the keys its access rule names, empty for a `policy:` or `session` rule —
  and `Sensitive`, true when any of them is `Sensitive` in the catalog. From `All` and `Info(id)`
  `Mode` is the contract's; from `List` and `Search` it is the effective mode below. `CustomerScope`
  is the operation's `x-vantigo-customer-scope`, `{param}` or `{resource}`, nil without one: a gate
  can read it to allow only operations that carry a scope.
- **`SetGate(kind, Gate)`** installs the function that narrows the contract's mode for one kind of
  principal: `type Gate func(ctx, Info) Mode`, the kind being the grant's (`assistant` or `agent`,
  `contracts.GrantAssistant` and `GrantAgent`). Each of the two modules calls it for its own kind
  while it mounts, and every method takes the gate from the kind of grant on the context (a context
  with no grant is the assistant's). A kind with no gate fails closed (below); the
  dispatcher keeps the **narrower** of the contract's mode and the gate's answer (`never` over
  `confirm` over `auto`), so a gate can never widen the contract, and it is called for every
  operation on every `List` and `Search`, so it must be cheap and cache. **Without a gate — and
  after `SetGate(kind, nil)` — the dispatcher fails closed**: an operation whose `Info.Sensitive` is true
  is `never`; for an agent with no gate **every** operation is. A gate that cannot decide answers `never`. The Assistant's gate applies the Owner's
  permission levels ([the reference](/en/reference/assistant/#permission-levels-and-the-gate)).
- **`Effective(ctx, id)`** is the **effective mode** — the contract's mode narrowed by the gate —
  for the context's workspace, and false for an unknown operation. `List`, `Search` and `Call` all
  work from it: a narrowed operation is absent from the first two and refused by `Call` as
  `operation_never`, before the confirmation check, whether or not anyone confirmed.
- **`List(ctx, userID)`** is the operations whose effective mode is not `never` and whose rule the user
  meets: a `permission:` rule is asked of `Access.UserHolds` (once per key per call), and
  `policy:ActiveAccount` and `policy:Business` are listed, the call deciding. `Search(ctx,
  userID, query, limit)` narrows it by a token match over the summary, description, tag and
  path (never the operationId alone), best first. **`Info(id)`** and `All()` answer for every
  operation, `never` ones included.
- **`Describe(id)`** is the operation as a model reads it, in at most 8 KB of JSON: the
  summary and mode, the path, query and header parameters, the request body and the success
  response, with every `$ref` resolved to a depth limit (a cycle is cut with a note), enums
  inline and descriptions kept. A schema too large is cut to fewer levels, and last to names.
- **`Call(ctx, CallRequest)`** builds the request from the operation's declared path, query and
  header parameters and a JSON body, refuses it with a typed `*operations.Error` (stable
  codes: `operation_unknown`, `operation_never`, `confirmation_required`, `no_grant`,
  `invalid_request`, `not_ready`, `call_failed`) before anything is dispatched when the
  operation is unknown, `never`, or `confirm` without `Confirmed`, when the context carries no
  grant (`contracts.WithGrant`, of either kind) or, for the assistant, user (`contracts.AsUser`),
  when an agent's grant is not current (the run is read from it with `AgentGrants.Describe`,
  given to `Bind` as `Options.AgentGrants`), or when the call does not fit the operation. For an agent's run an operation that declares `x-vantigo-customer-scope` is tied to the
  run's customer (a `param` is filled in or must equal it, a `resource` field of the answer must
  equal it, a run with no customer reads none), and breaking it is a `customer_scope` error. Once dispatched, the router's answer is a `CallResult` — a status and
  a JSON body, truncated to nothing with `Truncated` above 1 MiB, and the response's headers
  (`Header`: an `ETag` a change is guarded with, a `Location`). The request is built on a
  fresh context holding only the grant, with `RemoteAddr` `assistant-<user id>` (`agent-<user id>` for an agent), so the
  per-client rate limits are per person.
- **`Validate(ctx, CallRequest)`** checks a request as `Call` would and dispatches nothing: the
  operation is known and not `never` (a `confirm` one passes, confirmed or not), every path
  parameter is given, the query, header and body are ones the operation declares, and a required body is
  present. It is how a change is checked before the person is asked to confirm it.
- **Where it lives.** Only a process that serves the API has a bound dispatcher. A worker does
  not, so work that must call operations is not a worker's: the assistant's bulk jobs run in the API
  process under a claim ([the reference](/en/reference/assistant/#where-it-runs-and-why)).
- **Tests** run the real router and the real identity: `modtest.Harness.Operations(t)` binds a
  dispatcher to a harness's composed API and `AssistantContext(t, client, ttl)` issues a grant
  for a signed-in client. The package's own tests bind it to a small synthetic contract and
  `TestOperations_TheRealContract` (`internal/integration`) checks the mode of every operation
  of the shipped contract.

## The payments port

[`apps/server/internal/payments`](https://github.com/vantigo-io/vantigo/tree/main/apps/server/internal/payments)
is the port between a module and a payment provider, shared by Invoices and a later
Point of sale so both use the same adapters. It holds interfaces and values only:
no SQL — each consumer keeps its attempts and payments in its own schema (module
boundaries rule 4) — and it imports nothing but the standard library and
`internal/buildinfo`, which `TestPayments_Purity` and depguard's `platform` list
enforce.

### The interface

`payments.Provider` is one provider account (for Vipps, one sales unit):
`Create` a payment request under the caller's reference — `FlowRedirect` with a return
URL, or `FlowQR` answering the QR's target URL — which the caller may keep, never
logged, to show the same code again — and its code as SVG — `Get` its state and its four
amounts (authorized, captured, cancelled, refunded), `Capture`, `Cancel` and
`Refund`, `CaptureTime` (the first capture's time from the provider's event log),
`VerifyWebhook` and `Verify`, the cheapest authenticated call for a settings page.

**Amounts cross the port in minor units only**: `payments.Money{Currency, Minor}`,
øre for NOK. A module keeps its exact decimals inside and converts at the call,
refusing anything that is not a whole number of øre.

### Errors and "outcome unknown"

An adapter answers typed errors: `ErrUnauthorized` (the keys), `ErrNotFound`,
`ErrConflict{Code}` (a 409, or a reference or key already in use), `ErrRefused{Code}`
(a 400), `ErrThrottled{RetryAfter}` (a 429 or 423; the Vipps adapter reads `Retry-After`
as seconds or an HTTP date and answers at most a day, so a huge or overflowing header
is never a wait without end), `ErrInsufficientFunds` and
`ErrCaptureFailed` (a capture that took nothing — **not final**: Vipps asks for a retry,
and the caller decides when to capture again and when to give up),
`ErrInvalidSignature`, and `ErrInvalidRequest` — a request the adapter
refused **before sending it** because it breaks a documented rule of the provider's.
Anything else — a transport failure, a timeout, a 5xx, a redirect, an answer that
does not decode, a payment answered without one of its four amounts or in another
currency — is a plain error meaning **the outcome is unknown**: the request may
have taken effect, and the caller learns which by reading the payment again.

What is final depends on the operation, so there is no generic predicate, only one per
operation: `IsDefinitiveCreateRefusal` (refused keys, a request never sent, or a 400
naming a code — no payment exists, the attempt may fail) and `IsCaptureFailure`
(`ErrInsufficientFunds` or `ErrCaptureFailed` and nothing else). A conflict is never a
definitive refusal: "the reference already exists" may be this very request, made by
an earlier try whose answer was lost — the Vipps adapter reads the payment back then
(4150, 4020) and answers it as created only when it is the one asked for (the same
amount and currency) and still `CREATED` or `AUTHORIZED`. A QR is never read back —
Vipps does not promise that a read's redirect URL is the QR's target — so the outcome
is unknown and the caller polls the reference. A 409, a 401 or a
404 on a capture is **not** a failed capture: its money may have moved; and Vipps'
capture codes count only on the 400 it documents them with.

### Idempotency keys and retries

The `Idempotency-Key` on create, capture, cancel and refund is **the caller's**: it
persists the key with the operation's row and reuses it on every retry, so a timeout
after the provider accepted cannot become a second payment or capture. An adapter
never makes one up and refuses a call without one. It **never retries** either —
not even the transport's own replay of an idempotent request on a broken connection,
which the Vipps adapter turns off. The one repeat is a call answered 401, made once
more under a freshly fetched token.

### The Vipps MobilePay adapter

`internal/payments/vipps` speaks the ePayment API: `vipps.New(Options{BaseURL,
ClientID, ClientSecret, SubscriptionKey, MSN, SystemVersion, Transport, Now})`. It
fetches the access token from `/accesstoken/get`, caches it until a minute before it
expires by `Now` (one fetch at a time), and sends on every call the subscription key,
the `Merchant-Serial-Number` and the four `Vipps-System-*` headers (`vantigo`,
`vantigo-invoices`, the release). It refuses before any request an amount outside
1.00–650 000.00 NOK, a reference outside `^[a-zA-Z0-9-]{8,64}$` and a description
outside 3–100 characters, and a return URL or legal link that is not https or is
longer than 2500 characters. A call is bounded by thirty seconds and never follows a
redirect. A QR is asked for as Vipps' target URL (`TEXT/TARGETURL`) and rendered here
by `internal/payments/qrsvg` — the one QR renderer, which a module's own QR codes use
too — so the adapter never fetches a URL an answer names. The SVG holds only `svg`,
`rect` and `path`, never the encoded text; a page shows it as an image,
`<img src="data:image/svg+xml;base64,…">`, never inlined into its markup.
`RegisterWebhook` and `DeleteWebhook` manage a webhook registration for
`vipps.Events`, the eight ePayment events. **Invoices uses none of the webhook calls
yet**: it registers no webhook and receives none, and its worker polls
([Vipps payments](/en/reference/invoices/#vipps-payments)); they and the verification
below are kept for the webhook receiver that follows.

**Webhooks are verified against the registered URL**, never the request line: the
content hash of the raw body against `x-ms-content-sha256`, then HMAC-SHA256 under the
secret over `POST\n<path and query>\n<x-ms-date>;<host>;<content hash>`, where the
path, query and host are those of the URL the webhook was registered at — a reverse
proxy may rewrite the request line, and what Vipps signs is the URL it was given.
The `x-ms-date` must be an RFC 1123 date at most eight days old — Vipps retries a
callback for seven — and at most five minutes ahead, so a captured callback cannot be
replayed for ever.
Vipps' published test vector's signature does not reproduce, so the hash step is
pinned to that vector (`testdata/webhook-vector.json`), the signature step to a
self-generated one (flagged as such) and, once captured, to a real callback in
`testdata/webhook-captured.json`, which does not exist yet — its test skips without it.

### Testing against it

`internal/payments/vipps/vippstest` is an `httptest` TLS server speaking the token,
ePayment and Webhooks APIs, for every suite that touches a payment: states scripted
per reference, the test environment's special amount 151 øre (insufficient funds),
any status or error code on demand, a reset connection, idempotency by key, partial
captures, a refund made in the business portal and a signed webhook posted to a URL
the test gives. A harness passes its `Transport()` as the module's HTTP transport.
Only tests import it: depguard's `test-fakes` rule refuses it in any other file.

The tagged test `vipps_test_env_test.go` (`//go:build vipps`) runs the adapter
against Vipps' own test environment:

```sh
cd apps/server
go test -tags vipps -run TestVippsTestEnv ./internal/payments/vipps/
```

It needs a test sales unit's five variables — `VIPPS_TEST_CLIENT_ID`,
`VIPPS_TEST_CLIENT_SECRET`, `VIPPS_TEST_SUBSCRIPTION_KEY`, `VIPPS_TEST_MSN` and
`VIPPS_TEST_PHONE` (a test user who has approved one payment in the test app by hand)
— and skips without them; `VIPPS_TEST_BASE_URL` overrides
`https://apitest.vipps.no`. It creates, force-approves, captures (and repeats the
capture under the same key), refunds part, reads the event log, cancels a second
request, tries 151 øre (logging the HTTP status Vipps really answers its capture
with) and a QR. With `VIPPS_TEST_WEBHOOK_URL` — a public https URL
forwarded to `VIPPS_TEST_WEBHOOK_LISTEN` (default `127.0.0.1:8089`) — it also registers
a webhook, captures one signed callback, verifies it, writes it as the fixture and
deletes the registration.

## The bank feed port

[`apps/server/internal/bankfeed`](https://github.com/vantigo-io/vantigo/tree/main/apps/server/internal/bankfeed)
is the port between a module and a bank that answers an account's transactions
through an API, shaped like [the payments port](#the-payments-port): interfaces and
values only, no SQL — the consumer keeps its connections, accounts and lines in its own
schema (module boundaries rule 4) — importing nothing but the standard library and
`internal/buildinfo`, which `TestBankfeed_Purity` and depguard's `platform` list
enforce. Invoices is to be its first consumer; nothing imports it yet.
[Bank feeds](/en/contributing/bank-feeds/) describes Folio's adapter and how to add a
bank.

### The interface

`bankfeed.Feed` is one bank credential: `Verify`, the cheapest authenticated call for a
settings page; `Accounts`, the accounts the credential can read, each with its number,
name and kind (`operational`, `tax`, `savings`, `earmarks`, `card`, or `other` for a
kind the adapter does not know); and `Transactions(ctx, account, from, to)`, the booked
transactions of one account over a range of days, both inclusive.

A `bankfeed.Transaction` carries the bank's id, the booking day at UTC midnight, the
amount in minor units — unsigned — with its currency, a `Direction`, a `Kind`, the KID,
the remittance text, the debtor's name and account, and `Raw`, the bytes the bank sent
for that transaction, which the consumer stores as the documentation of what it books
from it. The adapter cuts nothing: lengths are the consumer's to enforce.

### Direction and kind: the adapter decides, the module judges

**The adapter decides** which way the money went and what moved it, because only the
adapter knows how its bank says so. `Direction` is `credit` (money in), `debit` (money
out) or `unknown` — a transaction the adapter cannot place, which the consumer must
refuse, never guess at. `Kind` is `transfer` (a giro or KID payment), `card` (a card
purchase) or `internal` (between two accounts of the same customer).

**The module judges** what a line means: which transactions it keeps, which it only
counts, and when a batch is refused.

### Errors

An adapter answers three typed errors: `ErrUnauthorized` (a 401 or 403: the bank refused
the credential), `*ErrRateLimited{RetryAfter}` (a 429, with the wait the bank named,
zero when it named none) and `ErrTooLarge` (an answer past the size bound: the consumer
asks again for fewer days — Invoices halves its window). Anything else — a transport
failure, a timeout, a 5xx, a redirect or an answer that does not decode — is a plain
error: the read failed and nothing was learnt.

**An adapter never retries.** A read has no side effect, so the consumer's next cycle
reads again; an adapter neither repeats a call nor caches an answer.

### Testing against it

`internal/bankfeed/folio/foliotest` is an `httptest` TLS Folio for every suite that
reads a bank feed: the accounts a test sets, the transactions it adds filtered by the
range asked for, 401 for any other key, and `Fail` per operation with a status and
`Retry-After`, a reset connection, a redirect, a body of the test's own or one past the
size bound. A harness passes its `Transport()` as the module's HTTP transport. Only
tests import it: depguard's `test-fakes` rule refuses it in any other file.

## The holidays

[`apps/server/internal/holidays`](https://github.com/vantigo-io/vantigo/tree/main/apps/server/internal/holidays),
the public-holiday calendar, is the one place the server knows which days are public holidays; a module that
counts business days counts them with it. Invoices moves a due date and a reminder
letter's deadline off them ([The rules](/en/reference/invoices/#the-rules)); Staff
counts every working day it reckons — the holiday account, the payroll totals, the
sick-leave rate — with `BusinessDay`, and lists the calendar's holidays with
`PublicHolidays` ([Absences](/en/reference/staff/#absences)). It
is a set of pure functions over the standard library's `time` package: it holds no
data, has no table and no migration, and reads no clock — its own purity test fails
on any other import or on a call such as `time.Now`.

**A day is a civil date at UTC midnight.** `DayIn(t, loc)` is the one derivation of
such a day from an instant — the calendar day `t` falls on in `loc`, made
`time.Date(y, m, d, 0, 0, 0, 0, time.UTC)` — so day arithmetic never meets a
daylight-saving shift and two callers agree about what a day is. The business
calendar (`contracts.BusinessCalendar`, identity's) answers `Today` and `Day` through
it in the workspace's business time zone, and Staff and Expenses, which each wrote
the same function for themselves, now mean this one. A time that is not a UTC
midnight is read by the holiday functions as its civil date in its own location —
10:00Z or midnight in Oslo on 17 May is 17 May.

| Function | Answers |
|---|---|
| `DayIn(t, loc) time.Time` | the calendar day `t` falls on in `loc`, at midnight UTC: 22:30Z on 30 June is 1 July in Oslo. |
| `PublicHoliday(d) bool` | whether `d` is a Norwegian public holiday: New Year's Day, Maundy Thursday, Good Friday, Easter Sunday and Monday, 1 May, 17 May, Ascension Day, Whit Sunday and Monday, Christmas Day and Boxing Day — whatever weekday it falls on. Christmas Eve and New Year's Eve are not; an ordinary Sunday, a helligdag in law too, is not listed but left to `BusinessDay`'s weekend test. |
| `EasterSunday(year) time.Time` | the Gregorian Easter Sunday of the year (the anonymous Gregorian algorithm), from which the movable feasts are counted. |
| `BusinessDay(d) bool` | whether `d` is neither a Saturday, a Sunday nor a public holiday. |
| `NextBusinessDay(d) time.Time` | `d` when it is a business day, else the first business day after it. |
| `PublicHolidays(from, to) []time.Time` | the public holidays from `from` to `to`, both counted, in date order and each day once (Whit Monday can fall on 17 May); none when `to` is before `from`. |

The calendar is **Norway's only**. A region beyond it would be a later column on a
module's settings, passed in by the caller; nothing in the package reads a setting.

## The image metadata stripper

`internal/imagemeta` strips a picture's metadata before a module stores or serves it:
a phone's photo carries where and when it was taken, the device and often a thumbnail
of the uncropped picture, and none of it is the person's face. Staff strips HR's photo
of a person before it reaches the object store, and identity strips a user's own avatar
and the managed one Staff sets, since Staff serves either to the person's colleagues.

| Function | What it does |
| --- | --- |
| `Strip(contentType string, data []byte) ([]byte, error)` | for `image/jpeg`, walks the segments from the start-of-image marker and drops APP1 (Exif, XMP) and APP13 (IPTC), copying everything from the start-of-scan marker on as it is; for `image/png`, walks the chunks and drops `eXIf`, `tEXt`, `zTXt` and `iTXt`, stopping after `IEND`; a file without metadata comes back byte for byte. |
| `Orientation(data []byte) (int, error)` | the EXIF orientation of a JPEG or PNG, from 1 to 8, read by walking to the first APP1 Exif segment (or the `eXIf` chunk) and the one Orientation tag in its TIFF structure; 1 when there is none or the value is out of range. Product images use it to turn a photo upright before they re-encode it ([Products](/en/reference/products/#images)). |
| `ErrStructure` | the segments or chunks do not hold together — a truncated or foreign file — which a caller refuses as it refuses a bad image. |

The pixels are never decoded by either function, so a decompression bomb costs the caller nothing here;
judging the type and the dimensions is the caller's (`image.DecodeConfig`, which reads
the header alone). The package imports the standard library only, and any module may
import it.

## The directory address rule

`internal/directoryaddress` is the address a directory provider gives an account it
creates: Microsoft Entra ID's user principal name and Google Workspace's primary email
follow one rule, so a person's address is the same whichever directory the company
uses.

| Name | What it does |
| --- | --- |
| `Rules` | a directory's alphabet for a local part: the ASCII letters and digits and its `Specials`. `UPN` admits `'.-_!#^~` (Entra); `Mailbox` admits `'.-_` (Google). |
| `Fold(s)` | the Norwegian letters and the few Latin letters NFD leaves whole replaced from a table (`ø` → `o`, `æ` → `ae`, `å` → `a`, `ß` → `ss`, …), then every other letter decomposed and its combining marks stripped; nothing dropped. |
| `Rules.CleanName(s)` | a name folded with everything outside the alphabet dropped, the dot and the space included, so a part of a pattern never holds a dot of its own. |
| `Rules.For(spec, pattern, domain, verified)` | the person's work email, lower-cased and folded, when its domain is one of `verified` and its local part is in the alphabet, tried once; else `pattern` — `given.family`, `g.family` or `given` — over the cleaned names at `domain`, at most 62 characters before a suffix, `user` when the names clean to nothing. `ErrNoDomain` when the work email cannot be used and `domain` is empty: which domain to use is the provider's choice. |
| `Address.Attempt(n)` | the `n`-th address to try: the local part as it is, then with 2 to 99 appended when a pattern made it; none past 99, and none after the first for a work email. |
| `TemporaryPassword()` | 32 random characters with an upper-case letter, a lower-case one, a digit and one of `!#%+-`: the password a create must carry and nobody uses. |
| `ReadablePassword()` | 16 random characters a person types once — an upper-case letter, a lower-case one and a digit at least, and none of `0`, `O`, `1`, `l` or `I` — Google's first password, which the person changes at the first sign-in. |

The package is pure — no context, no network, no database — and any module may import
it.

## The domain-events outbox

[`apps/server/internal/events`](https://github.com/vantigo-io/vantigo/tree/main/apps/server/internal/events)
is how a module tells others what happened to one of its rows when they may learn of it
a little later — an order confirmed, an invoice issued — without calling them inside its
own transaction. It was built for Orders
([design](https://github.com/vantigo-io/vantigo/blob/main/docs/superpowers/specs/2026-11-05-orders-design.md), O1–O5)
and is owned by no module: its tables are `platform.events` and
`platform.event_deliveries` (migration `00117_platform_events.sql`), beside
`platform.rate_limit`.

**The outbox carries what may lag, never what must refuse.** A reservation of stock or
the stamp of invoiced work must be able to say no to the transaction that asks for it,
so they stay synchronous contract calls on the caller's transaction
([rule 10](/en/contributing/module-boundaries/#the-rules), rule 20). An event is a fact
already committed: a confirmation, a line on a timeline.

**Publishing.** An event is a DTO in `internal/contracts` (`contracts/events_<module>.go`)
implementing `contracts.Event` — `EventName()` (`"<module>.<aggregate>.<what>"`, as
`orders.sales_order.confirmed`), `EventVersion()` (from 1) and `AggregateID()` (the
aggregate's id as text) — whose JSON tags are its wire form; a breaking change is a new
version, and a handler names the versions it accepts. `events.Publish(ctx, tx, module,
ev, occurredAt)` inserts it on the publisher's own transaction, so it commits with the
state change that caused it or not at all; it never begins, commits or rolls back `tx`
and never takes a pool. **A publisher publishes an aggregate's events only while it
holds that aggregate's row lock**, so event ids follow commit order per aggregate —
identity ids alone do not. **A payload carries ids, numbers, amounts and states, never
personal data** — no names, addresses, e-mails or phones — so a person's erasure needs no
sweep of the outbox; a handler that needs them reads its own copy, or a contract, after
its transaction.

**Handling.** A module declares `Module.EventHandlers`, a many-provider slot of
`events.Handler{Name, Events, Handle}`: `Name` unique across the composition,
`<module>.<what>`; `Events` the `{Name, Version}` pairs it takes; and `Handle(ctx, tx,
ev)`, run on the delivery's own transaction. The handler writes only its own module's
schema on `tx` — so a handler whose effects stay in the database runs exactly once —,
never leaves the process (no `OutboundMail`, no HTTP inside `tx`; mail is queued in its
own table for its module's own worker to send), locks only its own rows and reads no
contract while `tx` holds locks, and never begins, commits or rolls back `tx`
([rule 23](/en/contributing/module-boundaries/#the-rules)). An error rolls the delivery
back and it is retried. Handlers are collected from every module given, **whatever
`MODULES` says**, as the customer reference holders are: a handler that needs its module
on checks the setting itself and does nothing. `module.EventHandlers` collects them and
refuses a handler with no name, no events or no `Handle`, and a name two handlers share,
naming both modules; `Compose` calls it to refuse such a binary at startup, and the
process that runs workers calls it to build the dispatcher, since worker mode never
composes.

**The dispatcher**, `events.dispatch`, is the platform's own worker, run wherever the
workers run — the `worker` command, or `api` with `WORKERS_IN_PROCESS=1` — with no
setting of its own and no `MODULES` entry. Every 5 seconds it:

1. fans the new events out, **once each**: the lowest unmarked ids first, 100 a
   statement until none is left, writing one `pending` row of
   `platform.event_deliveries` for every handler that takes the event's name and
   version and marking the event `fanned_out_at` in the same statement. An event no
   handler takes is marked with no rows, and a handler composed later gets **no
   backfill** of events already fanned out;
2. warns, `events deliveries wait for handlers that are not composed`, when undone
   deliveries name a handler the binary no longer has — they stay for the operator to
   skip;
3. claims due deliveries, 100 at a time until none is left, under a 2-minute lease
   (`SKIP LOCKED`, so any number of instances share the work) and handles each on its
   own transaction, with a timeout of 1 minute, marking it `done` there; a handler's
   panic is an error like any other, and the lease is renewed when handling starts.
   Deliveries to one handler are tried **in event id order per aggregate** — the
   aggregate is the event name less its last segment (`orders.sales_order`) together
   with the aggregate id, so quote 42 and sales order 42 never wait on each other: a
   later delivery waits while an earlier one for the same handler and aggregate is
   `pending`, `failed` or `dead`, or while an earlier event of that aggregate is not yet
   fanned out, so a timeline never shows *changed* before *confirmed*; other aggregates
   are not held up;
4. records a failure — only one after the lease was re-checked: the handler's error,
   panic or timeout, or the commit; a database outage before that counts nothing and
   the delivery is reclaimed when its lease runs out — on a separate short statement — `failed`, retried after 1 minute,
   then 5 minutes, 30 minutes, 2 hours and 12 hours, and 12 hours from then on, logged
   at warn `events delivery failed` — and the eighth failure makes the delivery `dead`,
   logged at error `events delivery is dead`, until the operator retries or skips it;
5. prunes events **written** more than 30 days ago (`created_at`, not the publisher's
   `occurred_at`) that are fanned out and whose deliveries are all `done`, or that have
   none, 1 000 a pass; their deliveries go with them.

A pass that fails is logged at error, `events dispatch failed`, and the loop goes on. It
never holds one pooled connection while it asks for another, so it runs on a pool of
one. The operator's view — the stuck deliveries, a retry, a skip — is on the
[management listener](/en/admin/management-listener/#the-events-outbox); no tenant
permission reaches it.

## Live updates

`internal/live` lets the browser hear that something changed without polling. A module
notifies a topic on the transaction that writes; Postgres delivers the notification to
every HTTP replica when the transaction commits; each replica's `live.Hub` hands it to the
sockets that subscribed to the topic. It is a platform package: it imports no business
module, and it is in depguard's `platform` set in `apps/server/.golangci.yml`.

**Events carry identifiers only, never data.** A screen that hears an event refetches
through the ordinary API, which checks the person's permission as it always does, so the
socket never carries what the person could not fetch. A missed event is harmless once a
`resync` follows.

### Notify

```go
live.Notify(ctx, q live.Execer, topic, kind, id string) error
```

It runs `SELECT pg_notify('vantigo_live', …)` on `q`, with the payload `{"t":topic,"k":kind,"id":id}`.
`q` is anything with `Exec`: a `pgx.Tx`, the pool or a sqlc `DBTX`.

- **Notify on the transaction that writes.** Postgres delivers a notification only when the
  transaction commits and drops it on rollback, the way `events.Publish` behaves. Notify on
  the pool only for a write that is not in a transaction, and then log a failure rather than
  fail the request: the write is made, and the screen's fallback poll repairs a lost event.
- **Notify once per aggregate, never in a loop.** Postgres takes one cluster-wide lock at
  commit for every transaction that notified, held through the WAL flush, so notifying
  commits are serialised across the cluster. That is far below what UI-scale writes cost,
  but a loop over many rows would pay it per row. A bulk change (retention, erasure) sends
  **one** event without an id.
- **Validation.** `Notify` refuses a topic outside the grammar, a kind that is not
  `[a-z][a-z.-]*`, and a payload over 7 KiB, since `NOTIFY` itself refuses one over 8000
  bytes and an id is free text.
- **Several aggregates at once: `NotifyAll`.**

  ```go
  live.NotifyAll(ctx, q live.Execer, events []live.Event) error // Event{Topic, Kind, ID}
  ```

  A write that changes many aggregates of one kind together — a posting's lots, one page
  each — sends them in **one** statement, `SELECT pg_notify('vantigo_live', p) FROM
  unnest($2::text[]) AS p`: one round trip however many events, each judged by `Notify`'s
  rules and the whole batch refused for one that fails them; none sends nothing. A list
  topic in the batch goes once, without an id. It is still one notification per event at
  commit, so it is for a bounded set — a posting's 500 lines at most — never for a sweep
  of a table, which sends one event without an id.
- **Any process may notify.** A `worker`-only process builds no hub but calls `Notify`
  like any other. That is why the fan-out goes through Postgres and not through memory:
  inbound mail and Messenger messages are written by workers, which may run in another
  process than the HTTP replicas.

### Topics and the `LiveTopics` slot

A topic is `<module>.<kind>` or `<module>.<kind>:<key>`, matching
`^[a-z]+\.[a-z-]+(:[A-Za-z0-9_-]{1,64})?$`: `communications.inbox`,
`communications.conversation:<uuid>`.

A module registers the kinds it serves through `Module.LiveTopics func(Deps) []live.Topic`
([the slot](/en/contributing/module-boundaries/#the-rules)):

```go
live.Topic{Kind string, Presence bool, Authorize func(ctx context.Context, key string) error, Doc live.DocStore}
```

- `Kind` is `<module>.<kind>` and its prefix must be the module's name, the way permission
  keys are. `Compose` collects the topics of enabled modules into a `*live.Registry`, refuses
  a duplicate kind and a topic without `Authorize`, and passes the registry on in
  `Deps.Live`. A topic of a disabled module is `unknown_topic`.
- `Authorize` runs when a client subscribes, with a context carrying
  `contracts.AsUser(ctx, principal.UserID)`, so `live.HasPermission` answers through
  `Access.UserHolds`. It returns `nil` or `live.ErrForbidden`; any other error is read as
  forbidden too.
- **No existence oracle.** A key that names nothing and a key the person may not see are
  both `forbidden`. `unknown_topic` is only for a kind no enabled module registered. A
  key-level check uses the same rule the module's `GET` uses.
- `Presence` says whether clients may announce presence on the topic.
- `Doc`, when set, makes the topic a document's: see
  [Binary frames and documents](#binary-frames-and-documents).

### The topics in use

| Topic | Module | Keyed by | Authorised by |
| --- | --- | --- | --- |
| `communications.inbox` | Communications | none | `communications:conversations-view` |
| `communications.conversation:<id>` | Communications | a conversation | `communications:conversations-view`, and the conversation exists (presence) |
| `knowledge.tree` | Knowledge | none | `knowledge:articles` |
| `knowledge.article:<id>` | Knowledge | an article | `knowledge:articles` and read access to the article (presence) |
| `customers.imports` | Customers | none | `customers:import` and `customers:view` |
| `energy.imports` | Energy | none | `energy:import` and `energy:metering-points-view` |
| `invoices.billing-runs` | Invoices | none | `invoices:access` |
| `calendar.mailbox-sync:<userId>` | Calendar | **a person**: the key must equal the caller's own id | `calendar:access` |

The Calendar topic is the first keyed by a person rather than by a thing, and so the
precedent for a per-user topic. The host's notifications (the bell) will need one, once a
notifications design says what a notification is, who may see it and how it is read and
cleared; they are not live yet.

### The endpoint

`GET /api/v1/live?v=1` is mounted by `server.New` outside `Compose`, because the composed
router accepts only contract operations; it is therefore not in the API reference. The
handler answers before upgrading:

| Status | When |
| --- | --- |
| `404` | `LIVE_ENABLED=0`, or a process that builds no hub |
| `405` | not a `GET` |
| `400` | `v` is not `1` |
| `403` | the origin is not the application's: `Sec-Fetch-Site` must be `same-origin`, or `Origin` must equal the application origin; a missing `Origin` is refused |
| `401` | no session, answered by `Access.Reject`; on the station path, `station_unknown` |
| `429` | more than 30 handshakes a minute for the person, or the station (`live.connect`) |
| `503` | the process is shutting down |

The origin is checked by the handler itself because `CrossOriginProtection` lets every
`GET` through; the session cookie's `SameSite=Strict` is the second layer. The endpoint is
left out of the `otelhttp` filter so a socket never becomes an hours-long span. The wire is
JSON text frames, and binary frames for a document topic (below): `sub`, `unsub`, `presence`, `ping`, `doc_sync` and `doc_error` from the client; `ok`, `denied`
(`forbidden`, `unknown_topic`, `limit`, `unavailable`), `event`, `presence`, `resync` (with a
`reason`), `degraded` and `pong` from the server. `unavailable` is a transient server error
while authorising; the client retries the topic with backoff from 2 s to 60 s. A denial
never reveals existence: a missing key and a key the person may not see are both `forbidden`.

### Stations

A shared shop-floor station (manufacturing phase 2 §17) cannot send its operator's token on a
socket — a browser WebSocket sets no `Authorization` — so the handler has a **station path**:
when the hub's `Access` also implements `contracts.StationAccess`, a handshake carrying the
station cookie and no session cookie is resolved by `CheckStation(r)` into the station (a
revoked, expired, unknown station or an address outside its allowlist is `401
station_unknown`), and the socket belongs to the station, whichever operator is active.

- **Only topics marked `Station: true`** admit a station; every other topic refuses it before
  its `Authorize` runs. A station topic's `Authorize` reads `live.StationID(ctx)` instead of a
  person's permission (there is no `contracts.AsUser` on its context) and decides which keys
  the station may hear — Manufacturing's work-center and work-order topics, for the
  station's own work centers. `live.AuthorizeStation` runs one as the hub does, for a module's
  test.
- **The 60-second re-check** asks `StationLive(ctx, stationID, pairing, ip)` instead of a
  session, with the pairing `CheckStation` answered at the handshake and the handshake's
  address: a station that is revoked, expired, paired again in another browser or no longer
  reached from inside its allowlist closes the socket with `4401`, and every subscription is
  authorised again against the current work centers.
- **No presence** is announced from a station's socket, and it writes no document.
- **The limits are per station**: the connect rate limit is keyed by the station, and the
  sockets-per-person bound counts the station's sockets.

### The listener, `degraded` and `resync`

Each hub opens two dedicated connections, not from the pool, so a held connection cannot
starve the lease workers' pool: the **listener** (`LISTEN vantigo_live`) and the
**presence publisher**. Both open `LIVE_DATABASE_URL`, or `DATABASE_URL` when it is unset
(a transaction-mode pooler cannot `LISTEN`).

- The hub does not trust a quiet connection: it waits at most 30 s for a notification, then
  runs `SELECT 1` (5 s limit), and a failure marks the listener down. TCP keepalive (15 s
  idle, 5 s interval, 3 probes) catches a peer that vanished without closing.
- While the listener is down the hub sends `degraded` to every socket and to new ones, and
  the browser resumes its fallback polling.
- It reconnects with backoff, 1 s doubling to 30 s with jitter, and after every reconnect
  sends `resync` with `reason: "reconnect"` to every socket, because it cannot know what it
  missed; the status is `live` again. A slow consumer gets `reason: "slow_consumer"`: the
  screen refetches and the status is unchanged.
- Every 60 seconds the hub re-checks each session through `Access.SessionLive`, a read that
  does **not** slide the session's idle window (`Access.Check` does, which would keep a
  hidden tab signed in for ever). A session that is gone or idle-expired closes the socket
  with `4401`. It then re-runs every subscription's `Authorize`, with `UserHolds` answers
  cached for the cycle, and drops one that now fails with `denied`.
- The fan-out never blocks: each socket has a send queue of 64 frames. A full queue is
  cleared and a single `resync` queued.

### Presence

Presence is per topic, kept in memory on every hub and shared between hubs in batches. A
`presence` frame needs a subscription to a topic whose `Presence` is true; its state is
`viewing`, `editing` or `replying`, and `null` leaves. The hub pushes the topic's whole
member set to its subscribers at once, at most every 500 ms per topic. Between hubs, each
sends one notification every 3 s with the changes since the last (chunked under 7 KiB) on
its presence publisher, and repeats its full set every 15 s as a heartbeat; a remote hub's
members expire 45 s after its last batch, so a hub that dies leaves no ghosts. Clients
repeat their state every 20 s. Names are resolved through `contracts.UserDirectory`, cached
for 10 minutes. The cost is per hub, not per client: a hundred watchers cost at most one
notifying commit per replica every 3 s.

### Limits and close codes

| Limit | Value |
| --- | --- |
| Subscriptions per socket | 200 (`denied`, `limit`) |
| Sockets per person and replica | 20; the 21st is accepted and closed with `4429` |
| Client text frame | 4 KiB; larger closes with `1009` |
| Client binary frame | 4 MiB plus its header (the socket's read limit); larger closes with `1009`. Each kind has its own limit, [below](#binary-frames-and-documents) |
| Keep-alive | a ping every 25 s; a socket silent for 60 s is dropped |
| Send queue | 64 frames; overflowing three times in a minute closes with `1008` |

| Close code | Meaning |
| --- | --- |
| `1001` | the process is shutting down; the hub closes every socket before the pool, and the browser reconnects to another replica |
| `1007` | a binary frame that is not a frame (a header that does not fit, a topic outside the grammar, an unknown kind) |
| `1008` | slow consumer, a socket that cannot take a document's replay within 10 s, or ten binary frames for unsubscribed topics in a minute |
| `1009` | a text frame over 4 KiB, or a binary one over the read limit |
| `1011` | a document replay failed; the browser reconnects and syncs again |
| `4401` | the session ended; the browser does not reconnect |
| `4429` | more than 20 sockets for the person on this replica |

### Binary frames and documents

A topic whose `Topic.Doc` is set carries a document that browsers edit together (knowledge
design K25, K26). The hub relays and stores **opaque bytes**; it does not decode them and
knows no table. The module supplies a `live.DocStore`:

```go
type DocStore interface {
    CanWrite(ctx, key) bool                                  // ctx carries AsUser; false on error
    Replay(ctx, key, after, epoch int64, emit func(seq, data) error) (ReplayResult, error)
    Append(ctx, key, DocWrite) (DocResult, error)            // ErrDocLogFull, ErrDocReadOnly, ErrDocNotSeeded, *StaleError
    Seed(ctx, key, DocWrite) (accepted bool, seq int64, err error)
    Compact(ctx, key, upTo int64, DocWrite) (ok bool, err error)
    Fetch(ctx, key, seq int64) (data []byte, found bool, err error)
}
```

`DocWrite` carries the user, the bytes, the **origin hub id**, the **epoch** the writing
subscription synced at and, for a seed, the article version it was built from. `ReplayResult`
carries the newest `seq`, the log's current epoch, and `Stale` when the epoch the client holds is
not current (no row is emitted). `DocResult` carries the `seq` and whether to ask the writer to
compact: the store decides, so that asks are spaced across replicas. A write runs under the
document's lock, **re-checks that the person may still write** (`ErrDocReadOnly`), compares the
write's epoch with the log's (`&StaleError{Epoch}`), and notifies on the same transaction:
`live.NotifyDoc(ctx, tx, topic, seq, in.Hub)` for a row, `live.NotifyDocReset(ctx, tx, topic,
epoch)` when it deletes the log. It must not call `Notify` itself. The *epoch* counts resets,
from 1, because a `seq` is reused after one; 0 on the wire means "none held".

**The layout of a binary frame** is `kind` (1 byte), the topic's length (2 bytes, big endian),
the topic (UTF-8, the topic grammar, at most 80 bytes), the payload. Client to server: `0x01`
update (at most 256 KiB), `0x02` seed (the article version, 8 bytes, then 2 MiB), `0x03`
awareness (4 KiB), `0x04` compact (`upTo`, 8 bytes, then 2 MiB). Server to client: `0x01` update
(`seq`, 8 bytes, then the update) and `0x03` awareness. A malformed frame closes with `1007`.

**JSON control frames.** Client: `doc_sync {topic, after, epoch}`, with `after` the client's
**contiguous watermark** (the newest `seq` such that it holds every row up to it; a
compaction must also cover only up to it), and `doc_error {topic, message}`. Server:
`synced {topic, seq, epoch}`, `ack {topic, seq}`, `update_rejected {topic, reason}` (`read_only`,
`too_large`, `rate`, `log_full`, `stale`, `not_seeded`, `unavailable`), `seed_accepted`,
`seed_rejected`, `compact_requested {topic, upTo}`, `compact_rejected`, `doc_reset {topic,
epoch}` and `doc_stale {topic}`. Their meaning is in [Knowledge: the
frames](/en/reference/knowledge/#the-frames).

What the hub does for a document topic:

- **Epoch.** A subscription holds no epoch until `synced`, which binds it to the log's. Its
  writes carry that epoch; a store refusal as stale makes the hub answer `stale` and
  `doc_reset {epoch}` and unbind it. A `doc_sync` whose epoch is not current is answered
  `doc_reset` with no rows.
- **Writing.** `CanWrite` is asked on subscribe and at every re-check. A subscription that
  cannot write has its update, seed, compact and awareness frames dropped and counted; the
  first update is answered `update_rejected read_only`. Update, seed and compact frames share
  one rate bucket per connection (50 a second, burst 200); awareness has its own (30, burst 60).
  A store failure on a write is `unavailable` (`seed_rejected`, `compact_rejected`), not a
  close.
- **Order.** Frames are handled in the order they arrive, so `ack`, `update_rejected` and the
  seed and compaction answers come back in the order sent. An update is never echoed to the
  connection that sent it.
- **Replay.** `doc_sync` streams the rows after `after`, then `synced`. Replay frames go
  through a blocking enqueue (10 s), `synced` and `doc_reset` included, instead of the drop-and-resync of the ordinary queue; it
  stops between rows once its subscription is revoked or replaced; live rows that arrive
  meanwhile are held and sent after `synced`, and rows at or below the replay's newest `seq` are
  dropped as duplicates. **Never a silent gap:** when the ordinary queue is cleared (a slow
  consumer), every document subscription of the connection is marked stale and sent `doc_stale`,
  and a replay that overlapped the clearing sends no `synced`; the client syncs again from its
  watermark.
- **Other hubs.** A hub reads the rows other hubs announced on a worker per document that is
  never the listener's loop: one `Fetch` for a single notification, one `Replay` for a burst,
  filtered to the announced seqs. The hub that appended skips its own notification, so a row is
  never pushed back to its sender nor pushed twice. A row `Fetch` cannot find gives `doc_stale`.
- **Awareness** is ephemeral and never stored. It is relayed at once to the other
  subscriptions on this hub; for other hubs the hub keeps each connection's newest frame and
  every 250 ms sends one notification per document on its presence publisher's connection,
  the frames base64 encoded, a new notification starting when the next frame would take the
  encoded payload over 6 KiB. A frame over 4 KiB is dropped and counted, since its encoding
  would not fit.
- **Stray frames.** A binary frame for a topic without a document subscription is dropped and
  counted; the tenth in a minute closes with `1008`.
- **Compaction** is requested of the writer that has just stored, or been refused with
  `log_full`, when `DocResult` says it is demanded.

### Telemetry

An OpenTelemetry meter named `vantigo.live`: the up-down counters
`vantigo.live.connections` and `vantigo.live.subscriptions`, and the counters
`vantigo.live.notifications` (events the listener received),
`vantigo.live.resyncs{reason=reconnect|slow_consumer}` and
`vantigo.live.denied{reason=forbidden|unknown_topic|limit|unavailable}`, and for documents
`vantigo.live.doc_updates{result=stored|read_only|too_large|rate|log_full}`,
`vantigo.live.doc_seeds{result}`, `vantigo.live.doc_compactions{result}`,
`vantigo.live.doc_dropped{reason}` and `vantigo.live.doc_apply_errors`, and the histogram
`vantigo.live.doc_replay_rows`. The management listener's
`/management/status` carries the listener's state
([Management listener](/en/admin/management-listener/#the-endpoint)).

The browser side is described in [Live screens](/en/contributing/frontend-architecture/#live-screens);
the operator's side in [Live updates and your proxy](/en/admin/installation/#live-updates-and-your-proxy).

## The document arithmetic

[`apps/server/internal/docmath`](https://github.com/vantigo-io/vantigo/tree/main/apps/server/internal/docmath)
is a sales document's arithmetic, moved verbatim out of Invoices (orders design O17) so
that Invoices and Orders compute the same numbers by construction. It is pure functions
over exact decimals (`math/big.Rat`): no database, no request, no float, no clock.

| Name | What it does |
| --- | --- |
| `Round2(v)` | `v` at two decimals, the half away from zero. |
| `Line(quantity, unitPrice, discountPercent)` | the line rule: `gross = Round2(quantity × unitPrice)`, `allowance = Round2(gross × discount / 100)`, `net = gross − allowance` — a line stored as gross less allowance rather than one rounded product, so that its net is quantity × price as PEPPOL-EN16931-R120 holds it. |
| `Summarize(lines, exchangeRate)` | the VAT summary: the lines grouped per (category, rate), VAT computed per row on the sum of the nets (Peppol BR-CO-17), never per line — `vat = Round2(taxable × rate / 100)`, `vatNOK = Round2(vat × exchangeRate)` — a 0 % category getting its row too; the four totals, with no øre rounding of the total; and `ambiguous` when two lines share a (category, rate) but carry different SAF-T codes (Invoices' `vat_codes_ambiguous`). |
| `SortSummaries(rows)` | a document's order of the rows: the highest rate first, then by category. |

Invoices' rules for the amounts are described where it applies them
([The model](/en/reference/invoices/#the-model)). The package imports the standard
library only, and any module may import it.
