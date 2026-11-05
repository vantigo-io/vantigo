---
title: Shipping carriers
description: The carrier port internal/shipping — the Carrier interface and its capabilities, options, bookings, documents, tracking and the three cancel outcomes, the units and the typed errors — the Bring adapter, its products data file and its idempotency by reference, the bringtest fake and the opt-in live test, and how to add a carrier.
sidebar:
  order: 6
sources:
  - apps/server/internal/shipping
---

[`apps/server/internal/shipping`](https://github.com/vantigo-io/vantigo/tree/main/apps/server/internal/shipping)
is the port between a business module and a carrier (orders design O49), shaped like
[the payments port](/en/contributing/platform-packages/#the-payments-port): interfaces and
values only, no SQL — each consumer keeps its shipments in its own schema (module
boundaries rule 4) — importing no module, only the standard library and
`internal/buildinfo`. It is in depguard's platform set, and its purity test fails a build
that imports anything else. Orders is its consumer today
([Shipping](/en/reference/orders/#shipping)); a webshop's checkout would ask the same
`Rates` and `PickupPoints`.

## The port

`shipping.Carrier` is one carrier account — its keys, its customer numbers and whether it
runs in the carrier's test mode:

| Method | Does |
| --- | --- |
| `Capabilities()` | What the carrier can do beyond booking and tracking: rates, pickup points, delivery windows, cancel, webhooks, a coverage check, and its label formats. A caller asks it; an absent capability is never found by an error |
| `Rates(ctx, RateRequest)` | One `Option` per product or delivery window: the price excluding VAT and the VAT, in minor units, an `Estimate` flag, an expected date or a `Window`, and an `ExpiresAt` and opaque `Token` for a quote that must be booked as given. A carrier with no rate call answers none |
| `Book(ctx, BookRequest)` | Sender and recipient (with the phone and e-mail that drive the carrier's notices), the product, value-added services, packages, the chosen option's token, references, the caller's `IdempotencyKey` and `Retry` when an earlier call's outcome is unknown. Answers the consignment number, a package number per package, the product actually booked, the cost, the tracking URL and the document references |
| `Label(ctx, Booking, kind, format)` | The label or the waybill, in a format `Capabilities` lists |
| `Track(ctx, refs)` | Per known reference — consignment, package or the booking's reference — the events folded onto `booked`, `in_transit`, `ready_for_pickup`, `delivered`, `returned`, `exception` and `cancelled`, keeping the carrier's code, text, place and time |
| `ParseWebhook(r, secret)` | A tracking callback authenticated and parsed, for a carrier with the capability |
| `Cancel(ctx, Booking, key)` | `CancelCancelled`, `CancelNotSupported` — void it locally, its label must not be used — or `CancelTooLate` |
| `Services(ctx)` | The products the account may book, from the carrier or the adapter's data file — never Go constants |
| `PickupPoints(ctx, Address)` | Optional: id, name, address, manned or locker, opening hours, maximum weight and size |
| `Verify(ctx)` | The cheapest authenticated call, and whether prices are available |

**Units.** Money crosses the port in minor units (`shipping.Money{Currency, Minor}`),
weights in grams, dimensions in millimetres. The adapter converts to what its carrier
takes.

**Errors.** `ErrUnauthorized` (refused keys), `ErrInvalidRequest` (refused before sending:
nothing was sent), `ErrNotSupported`, `ErrTooLate`, `ErrNotFound` and
`*CarrierRefusal{Code, Message}`, the carrier's own words, shown to the user as they are.
Anything else — a timeout, a 5xx, an answer that does not decode — means the outcome is
unknown. `shipping.IsDefinitiveRefusal` says whether a `Book` error means nothing was
booked.

**Idempotency.** The key is the caller's, stored with the shipment before the call and
reused by every retry; an adapter never invents one and never retries on its own. A
carrier without an idempotency key of its own is made safe by the adapter, as Bring's is.

## The Bring adapter

`shipping/bring` speaks Bring's APIs with the Mybring user's `X-Mybring-API-Uid`,
`X-Mybring-API-Key` and `X-Bring-Client-URL` headers, and `X-Bring-Test-Indicator: true`
for an account in test mode:

- **Rates and Verify** — the Shipping Guide v2 (`POST /shippingguide/api/v2/products`),
  packages in grams and centimetres (millimetres rounded up), its `netPrice` preferred over
  its `listPrice`, both estimates. Verify asks for product 5000 between two Oslo postal
  codes and says prices are unavailable when Bring answers no net price — the user lacks
  the financial role.
- **Book** — `POST /booking/api/create`, packages in kilograms with three decimals. Bring has
  no idempotency key, so the caller's key is the consignment's `correlationId` and the
  sender's reference; with `Retry`, the adapter looks the key up through tracking first and
  answers the booking it finds (`Recovered`, without document links), booking only when it
  finds none. Bring's errors per consignment become a `CarrierRefusal` with the English
  message.
- **Label** — the labels link, followed through its redirect, PDF only; the Mybring keys go
  only to Bring's own host on a redirect.
- **Track** — `GET /tracking/api/v2/tracking.json?q=…`, one reference at a time under one
  deadline; `PRE_NOTIFIED` folds to booked, `READY_FOR_PICKUP` to ready for pickup,
  `DELIVERED` to delivered, `RETURN` and `DELIVERED_SENDER` to returned, `DEVIATION` to an
  exception, and anything else to in transit.
- **Pickup points** — `GET /pickuppoint/api/pickuppoint/{country}/postalCode/{pc}.json`.
- **Cancel** answers `CancelNotSupported`: Bring has no cancel call and bills on hand-in.
- **Services** are `shipping/bring/products.json` — code, English and Norwegian names,
  segment (`b2c`, `b2b`, `cargo`, `return`), whether delivered to a pickup point and whether
  it has a waybill of its own. When Bring withdraws or adds a product, edit the file. A
  cargo product is booked on the account's second customer number when it has one.

Answers are decoded tolerantly — Bring changes its API in place, so an unknown field is
ignored. Certificates are not pinned. Every call has one deadline (30 seconds by default),
and nothing is retried inside the adapter.

## Testing

`shipping/bring/bringtest` is an `httptest` TLS Bring for tests only (depguard keeps it out
of production code): the Shipping Guide, booking, the label behind a redirect, tracking by
consignment, package or reference and pickup points, each answer carrying a field the
adapter does not know. A booking to postal code 0000 is refused in Bring's words;
`SetNoFinancialRole` answers list prices only; `SetEvents` scripts a consignment's tracking;
`Fail` answers an operation with a status and a body, or books and then stalls past the
client's deadline (`Stall`) or answers what the client cannot read (`AfterBook`) — the
unknown outcome a retry must resolve. A harness passes its `Transport()` as
`Deps.HTTPTransport` and its `URL()` as `SHIPPING_BRING_BASE_URL`.

The live test runs the adapter against Bring itself, in test mode:

```bash
BRING_TEST_UID=… BRING_TEST_KEY=… BRING_TEST_CUSTOMER=5 \
  go test -tags bring -run TestBringTestEnv ./internal/shipping/bring/
```

`BRING_TEST_CUSTOMER` is a test customer number (5, 6 or 7) or a real one, comma-separated
when there are two; `BRING_TEST_BASE_URL` overrides `https://api.bring.com`. It skips
without them. Bring notifies real contacts even in test mode: the test uses dummy ones.

## Adding a carrier

1. Add the adapter beside Bring's, `internal/shipping/<carrier>`, implementing
   `shipping.Carrier` with honest `Capabilities`; keep its products in a data file when the
   carrier has no products call. Convert units at its edge.
2. Map its refusals to `CarrierRefusal` with its own words, refused keys to
   `ErrUnauthorized`, and leave everything else an unknown outcome. Send the caller's key as
   its idempotency key when it takes one; otherwise make `Retry` find the earlier booking.
3. Add its fake, `internal/shipping/<carrier>/<carrier>test`, to depguard's test fakes and
   the purity test, and an opt-in live test behind a build tag and its own variables.
4. Add its name to `config.KnownShippingCarriers` — `SHIPPING_CARRIERS` refuses unknown
   names — and, when its base URL is the operator's to change, a setting beside
   `SHIPPING_BRING_BASE_URL`.
5. In Orders, build it in `carrierOf` from the account's sealed keys, and allow it in the
   contract's `carrier` enum; its keys' shape is the account's credentials.
6. Document the carrier on [Shipping](/en/admin/shipping/) (both languages), in
   [the reference](/en/reference/orders/#shipping) and here.
