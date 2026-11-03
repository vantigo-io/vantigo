# Energy: a suggested electricity-tax class per metering point

Date: 2026-11-03. Research: [2026-11-03-energy-electricity-tax-class](../research/2026-11-03-energy-electricity-tax-class.md).

## The problem

A metering point's `electricityTaxClass` (`ordinary` | `reduced` | `exempt`) is set by hand
and decides which `electricity_tax` rate a grid tariff bills (tariffs, delivery B). Nothing
tells the person setting it which class the rules point to, and a wrong class misbills
every settlement silently. The owner asked for Energy to show whether forbruksavgift
(elavgift) should apply to a customer's point.

## Decisions (owner, 2026-11-03)

- **S1 — signal.** The suggestion is derived from the installation address (tiltakssonen)
  and the customer's industry code (NACE, from Brønnøysund via the Customers module's
  registry record), together with the point's `household` flag.
- **S2 — a hint, not the truth.** The stored class stays what settlements bill. The
  suggestion is derived on every read, never stored, and shown next to the stored class
  with a warning when they differ — as `vatTreatment` is today.

## The rule (`electricityTaxSuggestion` in `energy/electricity_tax.go`)

Inputs: `household`, `countryCode`, `postalCode` (the point's), `industryCode` (the
current customer's, may be empty), `hasCustomer`.

1. Not Norway (`countryCode` trimmed, case-insensitive ≠ `NO`) → no suggestion
   (`reason: not_norway`, `class` absent).
2. **In tiltakssonen** — the four-digit postal code is one of the 199 codes listed in the
   research (Finnmark: every code ≥ 9501; Nord-Troms: Karlsøy, Kvænangen, Kåfjord, Lyngen,
   Nordreisa, Skjervøy, Storfjord). Kept as an explicit table of ranges in Go, not
   computed from prefixes, since 9049–9059, 9100–9128, 9131, 9141 and 9170–9178 sit
   between zone codes but are not in it.
   - `household` → `exempt`, reason `zone_household` (vedtak § 3 a).
   - industry code in division 84 (public administration) → `exempt`, reason
     `zone_public_administration` (a heuristic: the rule is "offentlig forvaltning").
   - otherwise → `reduced`, reason `zone_business` (§ 1 annet ledd b) — any non-household
     consumer in the zone, with or without a customer or industry code.
   - postal code `9140` (Rebbenes) straddles Karlsøy/Tromsø: same class, `uncertain: true`.
3. **Outside the zone:**
   - `household` → `ordinary`, reason `household`.
   - industry code in sections B (divisions 05–09) or C (10–33), or `38.21` (SN2025
     materialgjenvinning) or `38.32` (its SN2007 code) → `reduced`, reason `industry`
     (saf. § 3-12-4). `uncertain: false` — the rule is the registered code, but the
     rate covers only power used in the production process; the UI says so.
   - industry code that *may* qualify for a reduced rate or an exemption that a code
     cannot decide (35.11, 35.2, 35.30, 01.13, 01.19, 01.30, 49.1, 49.2, 49.31, 52.21,
     88.99) → `ordinary`, reason `industry_may_qualify`, `uncertain: true`.
   - any other industry code → `ordinary`, reason `ordinary`.
   - no customer or no industry code → `ordinary`, reason `no_industry_code`,
     `uncertain: true`.

   Process exemptions (chemical reduction, electrolysis, metallurgy, mineralogy — saf.
   § 3-12-13) are never suggested: they hang on the process, not the code. Divisions 20,
   23 and 24 fall in section C and suggest `reduced`; an `exempt` stored for them is a
   mismatch the reader may well be right about, which the warning text allows for.

Industry codes are compared with dots and whitespace ignored as prefixes of the digits
(`"24.420"` → `24420`).

## Which customer

The point's **current customer**: the customer of the point's active supply period;
failing that, of its latest non-cancelled period by start; none when it has none. The
customer Energy tab uses the customer it shows. Energy reads the industry code through
`contracts.CustomerDirectory`; `CustomerEntry` gains `IndustryCode string` (empty when
the customer has no registry record or no code), filled by the Customers module's three
directory queries joining `customers.customer_registry_records`. A nil directory (never
in production) means no industry code.

## API

`MeteringPointResponse` gains an optional `electricityTaxSuggestion`:

```yaml
ElectricityTaxSuggestion:
  required: [reason, uncertain, matches]
  properties:
    class: { type: string, description: ordinary | reduced | exempt; absent when no suggestion }
    reason: { type: string }   # plain string, not an enum (oapi-codegen enum renaming gotcha)
    uncertain: { type: boolean }
    matches: { type: boolean } # class absent, or class == the stored electricityTaxClass
    customerId: { type: integer, nullable } # whose industry code was read
    industryCode: { type: string, nullable }
```

Sent on `GET /metering-points/{id}`, the create and update responses, and every row of
`GET /customers/{customerId}/metering-points` (that customer's code). The list
`GET /metering-points` does not send it (one directory read per page is fine, but the
list has no column for it; left out to keep the list cheap).

## UI

- **Metering point page, Billing card:** under *Electricity tax*, "Suggested: Reduced
  rate" with the reason sentence, a yellow `Differs` badge and the line "The stored class
  is what settlements bill. Check it against the customer's documentation." when
  `matches` is false, a grey "Check" badge when `uncertain`.
- **Customer Energy tab, meters table:** a new *Electricity tax* column — the stored
  class, and a yellow warning icon with a tooltip naming the suggestion when they differ.
- **Metering point form:** no change (S2).

## Docs

- `en/user/energy.md` + `nb/user/energy.md`: the Billing card's suggestion and the meters
  table column.
- `en/reference/energy.md`: `electricityTaxSuggestion`, the rule above with its sources.
- Customers reference (`en/reference/customers.md`, if it documents the directory
  contract): `CustomerEntry.IndustryCode`.

## Out of scope

Rates as dated data, splitting a point's consumption across classes, the battery and own-
production rules, Enhetsregisteret sector codes for public administration.
