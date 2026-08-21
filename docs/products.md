# Products module

The Products module is the catalog of everything the company sells: physical goods
and performed services alike. It runs in `Vantigo.Host`, owns the `products` schema
in the shared PostgreSQL database, and is the system of record that future Orders,
Warehouse and Booking modules will read product data from.

## Domain model

- **Product** — the shared identity of something the company sells: `name`,
  plain-text `description`, optional `categoryId`, `type` (`Goods` or `Service`),
  `status` (`Draft`, `Active`, `Discontinued`) and a required `taxCategoryId`.
  Every product has at least one variant.
- **ProductVariant** — the sellable identity: company-wide unique `sku`, optional
  GTIN `barcode`, `unit`, `standardCost`, logistics fields (`weightKg`, `lengthCm`,
  `widthCm`, `heightCm`), free-form `optionValues` (for instance `Color=Red`,
  stored as JSONB) and the variant's prices. Barcodes are digits-only
  GTIN-8/12/13/14 values with a valid check digit and are unique when set.
  Single-variant products are additionally flattened in API responses so simple
  products keep an inline UX.
- **ProductCategory** — a named grouping in a multi-level hierarchy. Each product
  belongs to at most one category; cross-cutting grouping is a future tags concern.
- **TaxCategory** — a centrally configured VAT rate (`Standard`, `Reduced`, `Zero`
  or `Exempt` kind plus a fractional rate). Products reference a tax category
  instead of carrying their own rate, so rate changes never touch products.
- **ProductPrice** — a sales price for one variant in one ISO 4217 currency,
  excluding VAT, with an optional validity window. A bounded campaign row beats
  the open-ended base row.

## Contracts for other modules

SKU is the stable business key of a variant. Consumers (future Orders, Warehouse)
should store the variant id together with a snapshot of the SKU. Prices and the
resolved VAT rate must be snapshotted at transaction time; consumers must not join
historical transactions back to mutable catalog prices or tax categories.

## API

Versioned REST endpoints are under `/api/v1/products`, authenticated with the shared
Identity cookie and antiforgery model:

| Endpoint | Description |
| --- | --- |
| `GET /api/v1/products` | List products with pagination, search, filters and sorting |
| `POST /api/v1/products` | Create with at least one variant, optionally with prices |
| `GET /api/v1/products/{id}` | Get one product with variants and effective prices |
| `PUT /api/v1/products/{id}` | Update shared fields |
| `DELETE /api/v1/products/{id}` | Archive by marking `Discontinued` |
| `GET /api/v1/products/{id}/variants` | List variants |
| `POST /api/v1/products/{id}/variants` | Add a variant |
| `PUT /api/v1/products/{id}/variants/{variantId}` | Update a variant |
| `DELETE /api/v1/products/{id}/variants/{variantId}` | Remove a variant (the last one cannot be removed) |
| `GET /api/v1/products/{id}/variants/{variantId}/prices` | List price rows |
| `POST /api/v1/products/{id}/variants/{variantId}/prices` | Add a price row |
| `GET /api/v1/products/categories` | List categories |
| `POST /api/v1/products/categories` | Create a category |
| `PUT /api/v1/products/categories/{id}` | Rename or re-parent a category |
| `DELETE /api/v1/products/categories/{id}` | Delete an unused category |
| `GET /api/v1/products/tax-categories` | List tax categories |
| `POST /api/v1/products/tax-categories` | Create a tax category |
| `PUT /api/v1/products/tax-categories/{id}` | Update a tax category |
| `DELETE /api/v1/products/tax-categories/{id}` | Delete an unused tax category |

The OpenAPI document is exposed at `/openapi/v1.json` and through Scalar during
development. Disable the module with `Modules__Products__Enabled=false`; the
module then registers nothing and its routes answer `404`.

## Development

Products is a vertical-slice module under
`apps/products/backend/Products.Module`, with explicit host `api`, `migrate` and
`seed` commands and UI routes in the single `apps/host/frontend` Vite application.
The host serves the module from the same origin; there is no standalone Products
frontend or API port.
