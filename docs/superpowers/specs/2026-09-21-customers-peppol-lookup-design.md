# Can this customer receive EHF? — design

Delivery **B** of the invoice-ready customer (`ROADMAP.md` → Customers → phase 2;
delivery A is `2026-09-21-customers-invoice-ready-design.md`). Delivery A left
`invoiceDelivery` and `peppolId` as fields a person fills in. This delivery asks the Peppol
network itself whether the customer is a registered receiver of Peppol BIS Billing 3.0
(EHF), remembers the answer, warns when the billing profile contradicts it, and lets the
user switch to EHF with one click.

## What the network looks like (verified 2026-09-21)

- **Discovery is NAPTR-only.** The CNAME lookup is gone (records removed Feb–Mar 2026).
  Hostname: `strip-trailing(base32(sha256(lowercase(ID-VALUE))), "=") + "." + ID-SCHEME +
  "." + SML-ZONE`, where `ID-VALUE` is `0192:923609016` (the value only, not the scheme),
  base32 is RFC 4648 upper-case, and `ID-SCHEME` is `iso6523-actorid-upis`.
- **The zone moved.** OpenPeppol insourced the SML: production
  `participant.sml.prod.tech.peppol.org`, test `participant.sml.test.tech.peppol.org`.
  The Commission's old zones are past their switch-over deadline (2026-08-31).
- The NAPTR answer has flags `U`, service `Meta:SMP` (case-insensitive) and a regexp
  `!.*!https://smp.example/!` carrying the SMP base URL. **NXDOMAIN means "not in the
  network"** — a definitive negative. Worked example: `0192:923609016` →
  `XQK4T3FMTEZDUY5BOVJMTNAQ45N7E4TIBYHWFPGOT75BSP7UYG2A.iso6523-actorid-upis.participant.sml.prod.tech.peppol.org`
  → `!.*!https://smp.elma-smp.no/!`.
- **Norwegian `0192:` does not always mean ELMA** (a sample of 40 found 2 at
  `smp.conta.no`): always follow the NAPTR, never hard-code an SMP. `9908:` is retired.
- **One unsigned GET answers the capability question**: `GET <base>/<urlencoded
  iso6523-actorid-upis::0192:…>` returns a `ServiceGroup` (namespace
  `http://busdox.org/serviceMetadata/publishing/1.0/`) whose
  `ServiceMetadataReference/@href` last path segments URL-decode to document type ids. 404 =
  not registered at that SMP. Gotchas seen live: the base URL may or may not end in `/`
  (trim it — `//` answers 400), and `Accept: application/xml` answers 406 on ELMA (send no
  `Accept`).
- Document types that mean "can receive an EHF invoice / credit note" — compared as the
  **full identifier string, exactly**, against this allow-list and nothing looser:
  `busdox-docid-qns::urn:oasis:names:specification:ubl:schema:xsd:Invoice-2::Invoice##urn:cen.eu:en16931:2017#compliant#urn:fdc:peppol.eu:2017:poacc:billing:3.0::2.1`
  and the `CreditNote-2::CreditNote` twin. Two failure modes seen live make anything looser
  wrong: participants that resolve and answer 200 with **no** invoice document type at all
  (IBM Norge, Dell AS — order or response profiles only), and DFØ's EHF **Reminder**
  profile, whose id also starts `…Invoice-2::Invoice##urn:cen.eu:en16931:2017#compliant#urn:fdc:peppol.eu:2017:poacc:billing:3.0#conformant#…reminder…`
  — a substring or prefix match says yes to a reminder-only receiver. The
  `peppol-doctype-wildcard` PINT ids are deliberately not accepted: Norway is not on PINT,
  a PINT specialisation is another country's format, and every Norwegian wildcard
  registrant also publishes the exact id (12,947 of 12,947 measured).
- Roughly one Norwegian participant in ten sits on a foreign SMP (Tickstar, Arratech,
  Seeburger, OpenText); Digdir itself says the only valid lookup is "the PEPPOL way … via
  the SML". ELMA's REST API needs an agreement and says it must not be used for this.
- Three outcomes at the DNS step, never two: NXDOMAIN → not registered (definitive);
  SERVFAIL / REFUSED / timeout → a technical failure, retryable, **never** "not
  registered"; NAPTR records but none with `U` + `Meta:SMP` → registered with no SMP
  service (reported as registered, able to receive nothing).
- Access is open and unauthenticated by specification; SMP URLs are https on 443 only.
  The Peppol Directory is *not* authoritative for a negative and is not used.

## Decisions

### D1 — A shared `internal/peppol` package, native DNS, no new dependency

The lookup lives in `apps/server/internal/peppol` (not inside customers): Invoices will
need the same discovery before it sends. It has three parts, each testable alone:
`ParticipantHost(zone, scheme, value)`; a `Resolver` that performs the NAPTR query with
`golang.org/x/net/dns/dnsmessage` (`Type(35)` + `UnknownResource`, RDATA decoded by hand;
UDP with TCP fallback on truncation; name servers from `/etc/resolv.conf` unless
`PEPPOL_DNS_SERVER` names one) — `golang.org/x/net` is already in the module graph, so
this adds **no third-party dependency**, and no lookup leaves for a public DNS-over-HTTPS
provider that would learn which organisation numbers a tenant asks about; and an SMP
client that fetches and parses the ServiceGroup.

`Lookup(ctx, participant) (Result, error)`:
`Result{Registered bool; SMPHost string; CanReceiveInvoice, CanReceiveCreditNote bool}`.
NXDOMAIN and SMP 404 are `Registered=false`, not errors. Anything else that fails (DNS
timeout, SERVFAIL, REFUSED, non-2xx/404, unparsable XML, a malformed NAPTR) is an error.
NAPTR records with no usable `Meta:SMP` entry are `Registered=true` with no capabilities.

### D2 — The SMP URL comes from DNS, so the fetch is guarded

The SMP base URL is third-party data. Before any request: it must parse, be `https`, have
no userinfo, and use port 443 (explicit or default). The HTTP client dials through a
guard that refuses private, loopback, link-local, CGNAT and metadata addresses and
connects to the very address it checked (the DNS-rebinding defence `internal/mail`
already has for SMTP). The address classification moves from `internal/mail` into a new
`internal/netguard` so there is **one** list of forbidden ranges; mail keeps its own
guard type and delegates. Redirects are not followed. The response body is capped at
1 MiB. Only `href`s are parsed — they are never fetched.

### D3 — `POST /customers/{id}/peppol-lookup`, and the answer is remembered

- Access `customers:billing-manage` + `customers:view` — the people who act on the answer.
- The participant looked up is the billing profile's explicit `peppolId` when set, else
  `derivedPeppolID` (a Norwegian business with a valid organisation number), else there is
  nothing to look up: **200** with `status: "no_identifier"`.
- Outcomes: `registered` (with `canReceiveInvoice`, `canReceiveCreditNote`),
  `not_registered`, `no_identifier`. An upstream failure is **502** (as Brreg's lookup);
  the feature switched off (`PEPPOL_LOOKUP_ENABLED=0`) is **503**. A failure stores
  nothing — the last good answer stands.
- The answer is stored in a new table `customers.customer_peppol_lookups` (one row per
  customer: participant id, status, the two capabilities, SMP host, checked-at). **It is
  not on the customer row**, so recording an answer never bumps `revision` — a lookup must
  not make somebody's open form conflict.
- `GET …/billing-profile` gains an optional `peppolLookup` object (absent when never
  checked). It is dropped from the response when the participant it was made for is no
  longer the one that would be looked up (the org number or `peppolId` changed since).
- **`participantId` is withheld** from both responses when it was *derived* from the legal
  identity and the caller lacks `customers:legal-identity-view` — the organisation number
  is that permission's to show. An explicit `peppolId` is already in the profile.
- A lookup records a timeline event `customer.peppol_lookup` only when the *status or
  capabilities changed* from the stored answer (so re-checking is quiet).

### D4 — The profile is warned, never changed behind the user's back

Two new warning codes on the billing profile, from the stored answer:
`ehf_recipient_not_registered` (delivery is `ehf` and the last lookup for the current
participant says not registered, or registered without the invoice document type) and
`ehf_available` — not a problem but an offer: the last lookup says the customer can
receive invoices and delivery is not `ehf`. Tripletex and Fiken switch the delivery method
silently; here the card says "This customer can receive EHF" with a **Use EHF** button
(a normal billing-profile PUT with `invoiceDelivery: "ehf"` and the current `revision`).
Nothing is looked up automatically — not on create, not on a Brreg pick, not on a
schedule: every lookup is a person's click, so a network failure never blocks a save.
(Scheduled re-checks belong with roadmap phase 3's refresh worker.)

### D5 — Configuration

| Variable | Default | |
| --- | --- | --- |
| `PEPPOL_LOOKUP_ENABLED` | `1` | `0` → the operation answers 503 and the UI hides the action after the first 503 |
| `PEPPOL_SML_ZONE` | `participant.sml.prod.tech.peppol.org` | the test network is `participant.sml.test.tech.peppol.org` |
| `PEPPOL_DNS_SERVER` | *(empty → `/etc/resolv.conf`)* | `host:port` of a resolver to use instead |
| `PEPPOL_TIMEOUT` | `10s` | one lookup end to end (DNS 3 s per attempt inside it) |

`module.Deps` gains `PeppolLookup func(ctx, participant string) (peppol.Result, error)` —
nil in production (the real client, built from config); a test harness sets a fake, the
same seam `HTTPTransport` gives Brreg. No test touches the network.

### D6 — Frontend

On the Billing card: a **Check EHF** action (with `canManageBilling`) beside the Peppol
row; the last answer in words with its date ("Can receive EHF invoices — checked 21 Sep
2026", "Not registered in Peppol", "Registered, but not for invoices"); the two new
warnings explained; **Use EHF** on the `ehf_available` offer; 502 → "The Peppol network
could not be reached. Try again."; 503 → the action disappears until the page is reloaded, with a
one-line note. The lookup mutation refreshes the billing-profile query, and the
customer's timeline query only when the answer changed (the server records an event
only then) — never the customer row.

## Out of scope

Sending anything over Peppol; verifying SMP signatures (needed before sending, not for a
yes/no); the Peppol Directory; scheduled re-checks; consumer eFaktura lookups; non-`0192`
identifier derivation (an explicit `peppolId` of any scheme is looked up as typed).

## Testing

`internal/peppol`: the spec's own hash vectors (`0088:123abc` →
`Y7DZFXAF3D4CJZ4KCGRXTEC6TWVCGA4KY7ZWA5BOIF6MSWD4TDRQ`, and the Equinor example above);
NAPTR RDATA decoding from captured bytes incl. malformed input (fuzz-safe: never panics);
resolver against an in-process UDP/TCP DNS stub (truncation → TCP, NXDOMAIN, SERVFAIL,
timeout); SMP client against `httptest` (trailing-slash base, no `Accept` header sent, 404,
500, oversized body, href decoding, exact doc types only — a reminder-profile id and a wildcard PINT id must NOT count); URL policy (http, port,
userinfo rejected). `internal/netguard`: the range table moves with its tests; mail's
tests stay green unchanged. Customers: handler tests through `modtest` with a fake
`PeppolLookup` for every outcome, the participant-selection rule, withholding, staleness,
the two warnings, quiet re-check vs changed answer event, no revision bump, 502/503,
permissions. Frontend tests beside the card.
