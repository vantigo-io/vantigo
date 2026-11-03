---
title: "Products module"
description: "The catalog of goods and services the company sells, with prices."
sources:
  - apps/server/internal/products
  - apps/products/frontend
  - openapi/products.yaml
---
The Products module is the catalog of everything the company sells: physical goods
and performed services alike. It is a vertical-slice module inside the single Vantigo
binary (`apps/server/internal/products`), owns the `products` schema in the shared
PostgreSQL database, and is the system of record that other modules read product data
from: [Inventory](/en/reference/inventory/) keeps stock of its goods, Projects and Time
price billing lines with it, and future Orders and Booking modules will read it too.

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

SKU is the stable business key of a variant. Consumers (Inventory, future Orders)
should store the variant id together with a snapshot of the SKU. Prices and the
resolved VAT rate must be snapshotted at transaction time; consumers must not join
historical transactions back to mutable catalog prices or tax categories.

Cross-module reads go through `internal/contracts`, never through another module's
schema or HTTP endpoints — see [module boundaries](/en/contributing/module-boundaries/).

### The catalog

Products provides `contracts.ProductCatalog`
([`catalog.go`](https://github.com/vantigo-io/vantigo/blob/main/apps/server/internal/contracts/catalog.go)).
A `VariantEntry` names a variant — its id, product id and product name, SKU, unit, the
product's type and status, and its own barcode (nil when it has none) — never enough to
manage the catalog. A missing row is an absent answer, never an error.

| Method | Answers |
| --- | --- |
| `Variant(id)` | One variant, nil when it does not exist. |
| `Variants(ids)` | The variants that exist among `ids`. |
| `ListPrice(variantID, currency, at)` | The variant's effective price in that currency at that moment, nil when it has none. |
| `VariantsOfProduct(productID)` | Every variant of the product, by id; empty when the product does not exist. |
| `LookupCode(code)` | Every variant whose SKU equals the trimmed code, ignoring case, or whose barcode equals it exactly. |
| `SearchVariants(q, limit)` | At most `limit` (capped at 50) variants of **Goods** products whose SKU, product name or barcode contains `q`, ignoring case, by SKU; `%` and `_` are matched literally. |
| `LockVariants(tx, ids)` | Takes `FOR SHARE` on the variants' rows and their products' rows, in id order, **on the caller's transaction**, and answers the entries. |

Every method but `LockVariants` runs on the module's pool and is called before the
caller opens a transaction. `LockVariants` is the one locking read run inside the
caller's transaction ([module boundaries, rule
21](/en/contributing/module-boundaries/#the-rules)): it never begins, commits or rolls
back `tx`, locks Products' rows before the caller locks any of its own, and calls
nothing back. Inventory calls it first in every posting, so what the posting read of a
variant — that it exists, is Goods and is not discontinued — holds until it commits,
and a variant's delete or its product's change to Service waits for it.

### Variants that hold stock

A module that keeps stock, or another fact, of a variant in its own schema fills the
many-provider slot `Module.VariantReferences` with a `contracts.VariantReferenceHolder`
([`references.go`](https://github.com/vantigo-io/vantigo/blob/main/apps/server/internal/contracts/references.go)),
collected from every module the binary composes, enabled or not
([rule 20](/en/contributing/module-boundaries/#the-rules)). Only Products calls it,
inside the transaction that deletes a variant or turns a product into a service, after
locking the variants. Its `VariantHeld` answers nil, or the quantity the holder keeps in
the variant's unit and the number of locations it lies at. Today Inventory is the one
holder.

**Deleting a variant** (`DELETE /api/v1/products/{id}/variants/{variantId}`) is one
transaction:

1. Lock the variant `FOR UPDATE`, scoped to the product — a variant of another product,
   or none, is 404.
2. Refuse the product's last variant: 409 *Last variant*, "A product must have at least
   one variant."
3. Ask every holder. The first hold refuses: 409 *Variant has stock*, "This variant still
   has 34 pcs in stock at 3 locations. Move the stock or adjust it to zero before you
   delete the variant." — the quantity with trailing zeros trimmed, the unit from the
   variant, and *location* in the singular for one.
4. Delete the variant and its prices: 204.

A posting holds `FOR SHARE` on the same variant, so it has either committed before the
holders are asked or waits until the delete is done: stock cannot land between the check
and the delete. A variant with movements but no stock may be deleted; Inventory's
movement lines keep their snapshot of it.

**Turning a Goods product into a Service** (`PUT /api/v1/products/{id}` with
`type: Service`) locks every variant of the product `FOR UPDATE`, in id order, asks the
holders variant by variant and refuses on the first that holds: 409 *Product has stock*,
"Variant {sku} still has 34 pcs in stock at 3 locations. Move the stock or adjust it to
zero before you make the product a service." Otherwise the product is updated, and
then, on the same transaction, its variants are listed again and the holders asked about
any variant added meanwhile; a hold refuses with the same 409 *Product has stock* and
the update rolls back. Otherwise it commits. A change from Service to Goods, or one that keeps the type, never asks.
Archiving a product (`Discontinued`) is unaffected.

These problems carry no `code`, as none of Products' does: the app tells *Variant has
stock* from *Last variant* by its title, and shows its own sentence rather than the
server's detail.

## API

Versioned REST endpoints live under `/api/v1/products`, authenticated with the shared
identity session cookie. Mutating browser requests are protected by origin checks
rather than an antiforgery token — see
[identity and authentication](/en/admin/authentication/).

| Endpoint | Description |
| --- | --- |
| `GET /api/v1/products` | List products with pagination, search, filters and sorting |
| `POST /api/v1/products` | Create with at least one variant, optionally with prices |
| `GET /api/v1/products/{id}` | Get one product with variants and effective prices |
| `PUT /api/v1/products/{id}` | Update shared fields (Goods to Service is refused while a variant holds stock) |
| `DELETE /api/v1/products/{id}` | Archive by marking `Discontinued` |
| `GET /api/v1/products/{id}/variants` | List variants |
| `POST /api/v1/products/{id}/variants` | Add a variant |
| `PUT /api/v1/products/{id}/variants/{variantId}` | Update a variant |
| `DELETE /api/v1/products/{id}/variants/{variantId}` | Remove a variant (the last one, and one that holds stock, cannot be removed) |
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
so enabling it later needs no migration. `inventory` requires `products`: a `MODULES`
that lists `inventory` without it refuses to start. Projects and Time use the catalog
when it is there and do without it when it is not.

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
