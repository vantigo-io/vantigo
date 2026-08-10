# Products module

The Products module is the catalog of everything the company sells: physical goods
and performed services alike. It runs in `Vantigo.Host`, owns the `products` schema
in the shared PostgreSQL database, and is the system of record that future Orders,
Warehouse and Booking modules will read product data from.

## Domain model

- **Product** — a distinct sellable unit with an auto-generated integer `id` and a
  company-wide unique `sku`. Fields: `name`, `type` (`Goods` or `Service`), `status`
  (`Draft`, `Active`, `Discontinued`), `unit`, `standardCost`, and `vatRate`.
- **ProductCategory** — a named grouping in a multi-level hierarchy. Each product
  belongs to at most one category; cross-cutting grouping is a future tags concern.
- **ProductPrice** — a sales price in one ISO 4217 currency, excluding VAT, with an
  optional validity window. A bounded campaign row beats the open-ended base row.

Catalog enrichment includes plain-text `description`, optional `categoryId`, GTIN
`barcode`, and optional logistics fields `weightKg`, `lengthCm`, `widthCm` and
`heightCm`. Barcodes are digits-only GTIN-8/12/13/14 values with a valid check digit
and are unique when set.

## Contracts for other modules

SKU is the stable business key. Consumers should store the product id together with
a snapshot of the SKU. Prices and VAT must be snapshotted at transaction time;
consumers must not join historical transactions back to mutable catalog prices.

## API

Versioned REST endpoints are under `/api/v1/products`, authenticated with the shared
Identity cookie and antiforgery model:

| Endpoint | Description |
| --- | --- |
| `GET /api/v1/products` | List products with pagination, search, filters and sorting |
| `POST /api/v1/products` | Create, optionally with initial prices |
| `GET /api/v1/products/{id}` | Get one product with effective prices |
| `PUT /api/v1/products/{id}` | Update fields |
| `DELETE /api/v1/products/{id}` | Archive by marking `Discontinued` |
| `GET /api/v1/products/{id}/prices` | List price rows |
| `POST /api/v1/products/{id}/prices` | Add a price row |
| `GET /api/v1/products/categories` | List categories |
| `POST /api/v1/products/categories` | Create a category |
| `PUT /api/v1/products/categories/{id}` | Rename or re-parent a category |
| `DELETE /api/v1/products/categories/{id}` | Delete an unused category |

The OpenAPI document is exposed at `/openapi/v1.json` and through Scalar during
development. Disable the module with `Modules__Products__Enabled=false`.

## Development

Products is a vertical-slice module under
`apps/products/backend/Products.Module`, with explicit host `api`, `migrate` and
`seed` commands and UI routes in the single `apps/host/frontend` Vite application.
The host serves the module from the same origin; there is no standalone Products
frontend or API port.
