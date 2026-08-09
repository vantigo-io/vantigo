# Products

The Products app is the catalog of everything the company sells: physical goods and
performed services alike. It is the system of record that future services — Orders,
Warehouse and Booking — will read product data from.

## Domain model

- **Product** — a distinct sellable unit with an auto-generated integer `id` and a
  company-wide unique `sku`. Fields: `name`, `type` (`Goods` or `Service`), `status`
  (`Draft`, `Active`, `Discontinued`), `unit` (for instance `pcs` or `hour`),
  `standardCost` (optional), and `vatRate`.
- **ProductPrice** — a sales price in one ISO 4217 currency, excluding VAT, with an
  optional validity window (`validFrom`/`validTo`). The everyday base price is
  open-ended; campaign and sale prices are added as bounded rows next to it, which
  also preserves price history.

### Effective price resolution

The applicable price in a currency at a moment is the row whose validity window
contains that moment. A bounded (campaign) row beats the open-ended base row, and the
latest starting window wins ties. Combinations the rules cannot resolve
deterministically — two open-ended base prices in the same currency, or two
overlapping campaign windows of the same currency — are rejected by the API.

### Lifecycle

Products are never hard-deleted, because other services reference them. `DELETE
/api/v1/products/{id}` archives the product by marking it `Discontinued`. The SKU is
immutable once a product leaves `Draft`.

## Contracts for other services

These rules exist so future integrations do not corrupt historical data:

- **SKU is the stable business key.** Internal integer ids are per-database details.
  Services that reference products (order lines, stock records, bookings) should
  store both the product id *and* a snapshot of the SKU.
- **Snapshot prices at transaction time.** The Orders service must copy the effective
  price (and VAT rate) onto its order lines when an order is placed, never join back
  to the price table afterwards. Prices change; transactions must not.
- **A different pack size is a different product.** A 10-pack of an item is its own
  product with its own SKU and price, not a quantity of the single-unit product.
  Bundle/kit composition may become an explicit relationship later.
- **`standardCost` is indicative.** It is a manually maintained number for margin
  estimates in the company base currency. Actual cost valuation (moving average,
  FIFO, supplier prices) belongs to the future Warehouse/procurement domain.
- **`vatRate` is the current rate.** If differentiated tax categories become
  necessary, the field will migrate to a tax-category reference; consumers should
  compute VAT amounts at transaction time from the snapshot they take.

## API

Versioned REST endpoints under `/api/v1`, authenticated with the same
Identity/cookie + antiforgery model as the Customers app:

| Endpoint | Description |
| --- | --- |
| `GET /products` | List with pagination, search (`name`/`sku`), status filter and sorting |
| `POST /products` | Create, optionally with initial prices |
| `GET /products/{id}` | Get one product with resolved effective prices |
| `PUT /products/{id}` | Update fields (SKU immutable once active) |
| `DELETE /products/{id}` | Archive (mark `Discontinued`), idempotent |
| `GET /products/{id}/prices` | All price rows, including expired and future ones |
| `POST /products/{id}/prices` | Add a base or campaign price row |
| `DELETE /products/{id}/prices/{priceId}` | Remove a price row |

The OpenAPI document is exposed at `/openapi/v1.json` and in the Aspire Scalar
reference during development.

## Development

The app follows the standard Vantigo vertical slice: `Products.Api` (ASP.NET Core
minimal API, EF Core, PostgreSQL) with explicit `api`, `migrate` and `seed` commands,
a React/Vite frontend served under `/products`, Aspire orchestration and Docker
Compose deployment. Development seeding creates a small fixed catalog covering both
product types, all lifecycle statuses, multi-currency prices and a campaign price.

Local ports: API `http://localhost:10020`, frontend `http://localhost:10021`.
