# Products module

The Products module is the catalog of everything the company sells: physical goods
and performed services alike. It is a vertical-slice module inside the single Vantigo
binary (`apps/server/internal/products`), owns the `products` schema in the shared
PostgreSQL database, and is the system of record that future Orders, Warehouse and
Booking modules will read product data from.

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

Cross-module reads go through `internal/contracts`, never through another module's
schema or HTTP endpoints — see [module boundaries](module-boundaries.md).

## API

Versioned REST endpoints live under `/api/v1/products`, authenticated with the shared
identity session cookie. Mutating browser requests are protected by origin checks
rather than an antiforgery token — see
[identity and authentication](customers-authentication.md).

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
| `PUT /api/v1/products/{id}/variants/{variantId}/prices/{priceId}` | Update a price row |
| `DELETE /api/v1/products/{id}/variants/{variantId}/prices/{priceId}` | Delete a price row |
| `GET /api/v1/products/categories` | List categories |
| `POST /api/v1/products/categories` | Create a category |
| `GET /api/v1/products/categories/{id}` | Get one category |
| `PUT /api/v1/products/categories/{id}` | Rename or re-parent a category |
| `DELETE /api/v1/products/categories/{id}` | Delete an unused category |
| `GET /api/v1/products/tax-categories` | List tax categories |
| `POST /api/v1/products/tax-categories` | Create a tax category |
| `GET /api/v1/products/tax-categories/{id}` | Get one tax category |
| `PUT /api/v1/products/tax-categories/{id}` | Update a tax category |
| `DELETE /api/v1/products/tax-categories/{id}` | Delete an unused tax category |
| `GET /api/v1/products/stats/summary` | Catalog summary counts |
| `GET /api/v1/products/stats/attention` | Products needing attention |
| `GET /api/v1/products/stats/timeseries` | Catalog activity over time |

`openapi/products.yaml` is the contract and the source of truth; the router enforces
each operation's access rule from it at runtime. The running server serves the merged
contract of its enabled modules at **`GET /api/openapi.json`, which requires a
session**. There is no `/openapi/v1.json` route and no Scalar or Swagger UI in the
image.

### Permissions

Ten permission keys, all delegable and none sensitive: `products:products-view`,
`products:products-manage`, `products:variants-view`, `products:variants-manage`,
`products:pricing-view`, `products:pricing-manage`, `products:categories-view`,
`products:categories-manage`, `products:tax-categories-view` and
`products:tax-categories-manage`.

## Enabling and disabling

`MODULES` is a positive allowlist of the business modules a deployment serves:

```text
MODULES=customers,products
```

Unset enables every module the binary can mount. Omitting `products` from the list
means the module contributes no route, no permission and no contract path, and its
paths answer the `/api` catch-all 404. The `products` schema is migrated regardless,
so enabling it later needs no migration.

## Development

The backend is `apps/server/internal/products`, with typed queries generated by sqlc
and the server interface generated by oapi-codegen from `openapi/products.yaml`. The
UI lives in `apps/products/frontend` (`@vantigo/products-ui`) and is composed by the
host SPA in `apps/host/frontend`. The single binary serves the module and the SPA from
the same origin; there is no standalone Products frontend or API port.

```bash
bun run --cwd apps/products/frontend test
cd apps/server && go test ./internal/products/
```
