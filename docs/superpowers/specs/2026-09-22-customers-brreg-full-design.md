# Brreg in full — design

Phase 3 of the Customers roadmap, delivery **A**: the customer's registry record. Today a
Brreg pick stores two fields (organisation number and name) and nothing ever re-reads the
registry. This delivery fetches the full record for a Norwegian business customer, keeps
it beside the customer with its provenance, offers addresses from it, re-reads it on a
click, writes the differences as `registry.change` timeline events, and surfaces the ones
that matter (bankruptcy, dissolution, deletion, a name change) on the dashboard's
attention list — the `/stats/attention` endpoint that has answered an empty list since
the port.

Delivery **B** (own spec, own PR) is the background worker: Brreg's incremental update
feed (`/oppdateringer/enheter`, cursor `oppdateringsid`) and the scheduled Peppol
re-check the roadmap names beside it. Nothing here waits for it; the refresh path built
here is what the worker will call per changed customer.

## What the registry looks like (verified against the live API 2026-09-22)

- `GET /enhetsregisteret/api/enheter/{orgnr}` with `Accept:
  application/vnd.brreg.enhetsregisteret.enhet.v2+json` (pinned — v1 is gone, 406).
  No auth, NLOD 2.0. No ETag/conditional requests (`cache-control: no-store`).
- Always present: `navn`, `organisasjonsnummer`, `organisasjonsform{kode,beskrivelse}`,
  `registreringsdatoEnhetsregisteret`, `maalform`, the booleans `konkurs`,
  `underAvvikling`, `underTvangsavviklingEllerTvangsopplosning`,
  `registrertIMvaregisteret`, `registrertIForetaksregisteret`,
  `registrertIFrivillighetsregisteret`, `harRegistrertAntallAnsatte`, `erIKonsern`.
- Optional (absent when unset): `antallAnsatte`, `hjemmeside`, `epostadresse`, `telefon`,
  `mobil`, `overordnetEnhet`, `naeringskode1/2/3{kode,beskrivelse}`,
  `institusjonellSektorkode`, `stiftelsesdato`, `sisteInnsendteAarsregnskap` (a string
  year), `konkursdato`, `underAvviklingDato`, `forretningsadresse`, `postadresse`.
- Addresses: `adresse` is an **array** of lines; `postnummer`, `kommune`,
  `kommunenummer` are absent on foreign addresses (the post code is inside `poststed`);
  `landkode` is ISO 3166-1 alpha-2 (`GB`, not `UK`).
- **Deleted entity**: HTTP **200** with a reduced body, `respons_klasse: "SlettetEnhet"`
  and `slettedato` — branch on the body, not the status. **Removed from open data**:
  HTTP **410** with `slettedato`; copies must delete their record. **Unknown**: 404, empty
  body. A sub-entity's number on `/enheter`: 404.
- Free-text fields (`vedtektsfestetFormaal`, `aktivitet`) are line-wrapped arrays — not
  stored here.

## Decisions

### D1 — A registry record beside the customer, not on it

New table `customers.customer_registry_records`, one row per customer (PK
`customer_id`, `ON DELETE CASCADE`), holding the registry's view of the entity:

| column | from |
| --- | --- |
| `organisation_number varchar(9)` | `organisasjonsnummer` |
| `name varchar(255)` | `navn` |
| `organisation_form_code varchar(10)`, `organisation_form varchar(100)` | `organisasjonsform` |
| `industry_code varchar(10)`, `industry varchar(255)` | `naeringskode1` |
| `employees integer` (NULL when not registered) | `antallAnsatte` if `harRegistrertAntallAnsatte` |
| `vat_registered boolean` | `registrertIMvaregisteret` |
| `bankrupt boolean`, `under_liquidation boolean`, `under_forced_liquidation boolean` | the three flags |
| `deleted_on date` | `slettedato` |
| `founded_on date` | `stiftelsesdato` |
| `website varchar(2048)`, `email varchar(255)`, `phone varchar(30)`, `mobile varchar(30)` | as named |
| `parent_organisation_number varchar(9)` | `overordnetEnhet` |
| `business_address jsonb`, `postal_address jsonb` | `forretningsadresse`, `postadresse`, each stored as `{lines[], postalCode, city, municipality, countryCode}` |
| `fetched_at timestamptz`, `registry_updated_hint timestamptz NULL` | when we read it; delivery B fills the hint from the feed |

Why a separate table: the customer row stays what the user owns (name, status, the legal
identity they asserted); the registry's record is a **fact about the world with a
timestamp**, kept verbatim and never edited by hand. Nothing here touches `revision`.
The legal identity's `legal_name` keeps being what it was: the name at the time of the
pick. A name change is reported (D4), not silently written over the identity.

### D2 — Fetch on pick, fetch on click

- **On create with a Brreg pick** (`identity.source = "brreg"`, country `no`, business),
  and **on `PUT …/legal-identity` with source `brreg`**: after the write commits, the
  full record is fetched and stored. A fetch failure never fails the create: the customer
  exists, the record is simply absent, nothing is recorded for a transient failure, and
  the card shows "Registry record not fetched yet" with a Refresh action. (A user who
  just picked a company must not be told the create failed because Brreg blinked.)
  The fetch runs after the create's transaction has committed and before the response —
  bounded by `BRREG_TIMEOUT` — so a slow registry delays the create by at most that.
- **`POST /customers/{id}/registry-refresh`** (`customers:legal-identity-manage` +
  `customers:view`): re-reads the record for a customer whose identity is a Norwegian
  business with a valid organisation number (any source — a manual identity with a
  valid number can be enriched too). 200 with the record and a `changes` list; 404; 409
  `no_registry_identity` when the customer has no such identity; 502 when Brreg cannot be
  reached (as the lookup); 200 with `status: "deleted"` for a `SlettetEnhet`, and `status:
  "removed"` for a 410 — in which case the stored record is **deleted** (Brreg's terms)
  and only the fact "removed from open data on <date>" is kept as an event.
- `GET /customers/{id}/registry-record` (`customers:view`; the identity fields it
  repeats — organisation number — are the legal identity's, so **the whole record is
  omitted for callers without `customers:legal-identity-view`**, the way `identity` is on
  the customer response).
- The Brreg client grows `Entity(ctx, orgnr) (Entity, Outcome, error)` with the same
  retry/backoff/timeout policy as the search, the pinned v2 media type, and a 1 MiB body
  cap. `Deps.HTTPTransport` remains the test seam.

### D3 — Addresses are offered, not imposed

A stored record does not write to `customer_addresses`. The Contact & addresses card
offers **"Use registry address"** (business → `visiting`, postal → `postal`) as
prefilled add-address modals: one click reviews, one click saves through the ordinary
address endpoint. Delivery A's rule that the first address of a type is primary applies.
Reason: addresses on file may deliberately differ from the registry (an invoice address
agreed with the customer), and a refresh must never silently move where mail goes.

### D4 — What a refresh compares, and what it reports

After a fetch the new record is compared with the stored one (first fetch: with the
legal identity's name only). Differences are written as **one** `registry.change`
timeline event (the manual-entry type that has existed unused since the port; here
produced with `provenance: generated`, `producer: customers.brreg`, actor = the user who
clicked, or `system` when the worker of delivery B does it) whose payload lists each
changed field `{field, from, to}` and whose summary names the notable ones. Fields
compared: name, organisation form, industry code, employees, VAT registration, the three
status flags, deletion, website, email, phone, mobile, parent, both addresses (line by
line). `fetched_at` moving is not a change.

**Notable** changes — the ones a person should act on — additionally become
`/stats/attention` items, each with a stable `id`, `type`, `title` (the customer's name),
`occurredAt`, `entityId` = customer id, and, as projects' items do, a `count`-free shape:

| type | when |
| --- | --- |
| `registryBankrupt` | `bankrupt` became true |
| `registryLiquidation` | `under_liquidation` or `under_forced_liquidation` became true |
| `registryDeleted` | a `SlettetEnhet` (or removed from open data) |
| `registryRenamed` | `name` differs from the legal identity's `legal_name` |

An item stays on the list until the customer is archived (bankrupt/liquidation/deleted) or
the legal identity's name is updated to the registry's (`registryRenamed`) — the list is
computed from the stored record and the current customer, not from events, so it is
idempotent and needs no "dismiss" state. The host already links a customers item to
`/customers/{entityId}`; the host catalog gets the four sentences (en + nb).

The record is not used to validate the billing profile (e.g. a VAT warning): that is
Invoices' call, later. D4 stays with registry facts.

### D5 — Provenance in the UI

A **Registry** card on the Overview tab (visible with `legal-identity-view`): the
record's fields with a "From Brønnøysundregistrene, fetched {date}" line, status badges
(Bankrupt / Under liquidation / Deleted / Removed) in red, the parent organisation number
as text, **Refresh** (with `legal-identity-manage`), the last refresh's changes shown
inline once ("3 changes — see the timeline"), and the two "Use registry address" offers
handed to the addresses section. When the registry name differs from the legal name, the
card says so with **"Update legal name"** (a `PUT …/legal-identity` with the registry
name, same source; `legal-identity-manage`). The legal badges component's Brreg deep
link stays.

## Out of scope

The update-feed worker and scheduled Peppol re-checks (delivery B); roles (daglig leder,
styre) — personal data, later and deliberately; sub-entities (`underenheter`); the bulk
download; capital, purpose and activity texts; historical names; using the record to
validate the billing profile; auto-archiving on bankruptcy (the attention item asks a
person).

## Configuration

None new: `BRREG_BASE_URL` and `BRREG_TIMEOUT` already exist. The v2 media type is a
constant.

## Testing

Brreg client: entity fetch against `httptest` for the three bodies (full, `SlettetEnhet`,
foreign address), 404, 410, 5xx with retry, 406 (wrong media type must not happen — the
header is asserted), oversized body, malformed JSON. Handlers through `modtest` with
`WithTransport`: create-with-pick fetches and stores, and a Brreg failure still creates;
refresh stores, diffs and records one event with the right payload; deleted/removed
outcomes; permissions and withholding; attention items appear and clear for each of the
four types; addresses offered are never written by a refresh. Frontend tests beside the
card.
