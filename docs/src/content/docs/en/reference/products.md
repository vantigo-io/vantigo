---
title: "Products module"
description: "The catalog of goods and services the company sells, with prices, variants and their packagings; the catalog contract other modules read and lock; the variants that hold stock; the product images; the API, its permissions and the product list's extra columns."
sources:
  - apps/server/internal/products
  - apps/products/frontend
  - openapi/products.yaml
  - apps/server/internal/db/migrations/00109_products_packagings.sql
  - apps/server/internal/db/migrations/00131_products_images.sql
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
- **Packaging** — a box, a case or a pallet a variant is bought, stored and counted in:
  up to ten per variant, each holding a quantity of the variant's unit, directly or as a
  count of another packaging. See [packagings](#packagings).
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
product's type and status, the product's tax category id (`TaxCategoryID`) and
description (`""` when it has none), the product's category (`CategoryID`, nil when it has
none, and `CategoryName`, the category's own name — Orders' intake by category), the
variant's option values (`OptionValues`, keyed by the option's name as it was typed, nil
when it has none — Orders' variant grid), its own barcode (nil when it has none), its
weight in grams (`WeightGrams`, the variant's weight in kilograms as whole grams, nil when it
has none — Orders proposes a shipment's parcel from it), its standard cost
(`StandardCost`, exact decimal text with two places such as `"12.50"`, `""` when it has
none — the one cost Manufacturing reads, manufacturing design M4) and its
**packagings** (`PackagingEntry`: id, name, kind, quantity as a three-place decimal
such as `"600.000"`, barcode; by sort order then id, empty when it has none) — never
enough to manage the catalog. Every read fills the packagings, `LockVariants` included,
inside the caller's transaction, so what Inventory resolves an entry in packagings
against holds until it commits. A missing row is an absent answer, never an error.

The entry carries the tax category's id, never its kind or rate: the VAT a sale is
invoiced at comes from Invoices' dated VAT codes, which map each tax category to one
code ([Tax categories and VAT codes](/en/reference/invoices/#tax-categories-and-vat-codes)),
and Products' undated rate would be a second, wrong source. **No new float carries money
across a contract**: `ListPrice` answers `Money{Amount, Currency}` with `Amount` as exact
decimal text at two places (`"1234.50"`), the chosen price row's numeric as stored —
the form `WorkSource.Amount` uses. Time's rate chain reads it exactly and applies a
billing line's discount to the exact value; Projects' billing-line pricing turns it into
the JSON number its response carries, which a two-place price survives exactly.

| Method | Answers |
| --- | --- |
| `Variant(id)` | One variant, nil when it does not exist. |
| `Variants(ids)` | The variants that exist among `ids`. |
| `ListPrice(variantID, currency, at)` | The variant's effective price in that currency at that moment, nil when it has none. |
| `VariantsOfProduct(productID)` | Every variant of the product, by id; empty when the product does not exist. |
| `LookupCode(code)` | A `CodeMatch` for every variant whose SKU equals the trimmed code, ignoring case, or whose barcode equals it exactly — the variant matches first — then one per **packaging** whose barcode equals it, with `Packaging` set to the packaging that matched. |
| `LookupSKUs(skus)` | For each SKU, keyed exactly as the caller wrote it, every variant whose SKU equals it trimmed and ignoring case, by id — `LookupCode`'s SKU half for many codes at once, barcodes not matched. A SKU no variant has, or a blank one, is absent. One query per 1,000 distinct SKUs and one load of their packagings. |
| `SearchVariants(q, VariantSearch{Types, Limit, IncludeInactive})` | At most `Limit` (capped at 50) variants of products of `Types` (`Goods`, `Service`; both when empty) whose SKU, product name or barcode contains `q`, ignoring case, by SKU — of **active** products only unless `IncludeInactive`; `%` and `_` are matched literally, and a blank `q` (or a `Limit` of 0) answers nothing. Packaging barcodes are not searched. Inventory's picker asks for `Goods` of every status, as it always has; Orders will ask for both types, active only. |
| `ProductImages(productID, use)` | The product's images marked for a use, in order; see [the catalog's image calls](#the-catalogs-image-calls). |
| `OpenProductImage(imageID)` | One image's normalised bytes and description, nil when it is gone; see [the catalog's image calls](#the-catalogs-image-calls). |
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
locking the variants. Its `VariantHeld(tx, variantID, purpose)` is told which write
asks — `delete` or `type_change` — and answers nil, or a `VariantHold` whose `Kind` is
`stock` — the quantity the holder keeps in the variant's unit and the number of
locations it lies at —, `reserved` — a quantity orders have reserved of it with nothing
on hand, and the number of sites ([Inventory](/en/reference/inventory/#reservations-and-issues)) —,
`in_transit` — a quantity on its way between two sites on an Inventory transfer, with
nothing on hand or reserved, and the number of transfers
([Inventory](/en/reference/inventory/#in-transit)) —
or `open_documents`, the number of open quotes, orders and subscriptions that name the
variant, or of Manufacturing's active bills of materials that name it as their product or
a component ([Manufacturing](/en/reference/manufacturing/#the-variant-holder)). A holder
may stop one write and not the other:
open orders will stop a delete but never a change to Service (orders design O30).
Inventory's stock, its reservations and its stock in transit stop both, and so do
Manufacturing's active bills of materials (manufacturing design M16a).

**Deleting a variant** (`DELETE /api/v1/products/{id}/variants/{variantId}`) is one
transaction:

1. Lock the variant `FOR UPDATE`, scoped to the product — a variant of another product,
   or none, is 404.
2. Refuse the product's last variant: 409 *Last variant*, "A product must have at least
   one variant."
3. Ask every holder. The first hold refuses: 409 *Variant has stock*, "This variant still
   has 34 pcs in stock at 3 locations. Move the stock or adjust it to zero before you
   delete the variant." — the quantity with trailing zeros trimmed, the unit from the
   variant, and *location* in the singular for one. A `reserved` hold is the same 409
   *Variant has stock*, "This variant still has 5 pcs reserved for orders at 1 site.
   Deliver or cancel the orders that reserve it before you delete the variant." An
   `in_transit` hold is the same 409 *Variant has stock*, "This variant still has 12 pcs
   in transit between sites in 1 transfer. Receive or cancel the transfer before you
   delete the variant." (*transfers* and "the transfers" for more than one). An
   `open_documents` hold is 409
   *Variant is on open documents*, "This variant is on 3 open quotes or orders. Finish
   or cancel them before you delete the variant." (*1 open quote or order* for one).
4. Delete the variant and its prices: 204.

A posting holds `FOR SHARE` on the same variant, so it has either committed before the
holders are asked or waits until the delete is done: stock cannot land between the check
and the delete. A variant with movements but no stock may be deleted; Inventory's
movement lines keep their snapshot of it.

**Turning a Goods product into a Service** (`PUT /api/v1/products/{id}` with
`type: Service`) locks every variant of the product `FOR UPDATE`, in id order, asks the
holders variant by variant and refuses on the first that holds: 409 *Product has stock*,
"Variant {sku} still has 34 pcs in stock at 3 locations. Move the stock or adjust it to
zero before you make the product a service." — for a `reserved` hold, "… still has 5 pcs
reserved for orders at 1 site. Deliver or cancel the orders that reserve it before you
make the product a service." —, for an `in_transit` hold, "… still has 12 pcs on its way
between sites in 1 transfer. Receive or cancel the transfer before you make the product a
service." —, or, for an `open_documents` hold, 409
*Product is on open documents*, "Variant {sku} is on 3 open quotes or orders. Finish or
cancel them before you make the product a service." Otherwise the product is updated, and
then, on the same transaction, its variants are listed again and the holders asked about
any variant added meanwhile; a hold refuses with the same 409 *Product has stock* and
the update rolls back. Otherwise it commits. A change from Service to Goods, or one that keeps the type, never asks.
Archiving a product (`Discontinued`) is unaffected.

These problems carry no `code`, as none of Products' does: the app tells *Variant has
stock* from *Last variant* by its title, and shows its own sentence rather than the
server's detail.

## Packagings

A **packaging** (Inventory's design I24, pulled forward from [phase 5 of the
roadmap](https://github.com/vantigo-io/vantigo/blob/main/ROADMAP.md#products)) is a box,
a case or a pallet a variant is bought, stored and counted in — a box of 15, a Euro
pallet of 40 boxes. Products keeps it in `products.variant_packagings`
(`00109_products_packagings.sql`); [Inventory](/en/reference/inventory/#entering-in-packagings)
enters and shows stock in it. **Stock is never touched** by a packaging's write: stock
is always kept in the variant's own unit.

### Fields and rules

- **`name`** — 1–40 characters, trimmed.
- **`kind`** — `inner`, `case`, `pallet` or `other`, in any case, stored lower-case.
- **What it holds**: exactly one of
  - **`quantity`** — of the variant's unit, more than 0, at most three decimals
    (refused, never rounded), at most 99,999,999,999.999; or
  - **`containsPackagingId`** with **`containsCount`** — a count of at least 2 of
    another packaging **of the same variant** (another variant's, or an unknown one, is
    a field error on `containsPackagingId`). Its `quantity` is then **derived**, exact:
    the contained packaging's quantity × the count — 40 × 15 = 600.
- **`barcode`** — optional, a GTIN-8, -12, -13 or -14 with a valid check digit, unique
  across **every variant and every packaging**.
- **`weightKg`**, **`lengthCm`**, **`widthCm`**, **`heightCm`** — optional, 0 or more.
- **`sortOrder`** — a create without it goes last; a `PUT` without it keeps the one it
  had.

**At most ten** per variant: an eleventh is a field error on `packagings`. A change to
what a packaging holds **recomputes every packaging built from it**, transitively,
parent before child, in the same transaction — changing a box of 15 to 12 makes the
pallet of 40 boxes hold 480. A derived quantity, or a dependant's, that would pass the
maximum refuses the change and rolls it back.

### Endpoints and permissions

All under `/api/v1/products/{id}/variants/{variantId}/packagings`:

| Operation | Permission | |
| --- | --- | --- |
| `GET` | `variants-view` | The variant's packagings, by sort order then id. |
| `POST` | `variants-manage` + `variants-view` | Add one; 201. |
| `PUT /{packagingId}` | `variants-manage` + `variants-view` | Replace one whole; 200. |
| `DELETE /{packagingId}` | `variants-manage` + `variants-view` | Remove one; 204. |

A variant that is not the product's, or a packaging that is not the variant's, is a
404. Every variant response — a product's, the product list's, the variant list's, a
variant added or changed — carries the variant's **`packagings`**, by sort order then
id.

### Refusals

Like every Products problem, these carry no `code`; the app tells them apart by title.

| Status | Title | Detail, for example |
| --- | --- | --- |
| 400 | *Invalid packaging* | The field errors: `name`, `kind`, `quantity`, `containsPackagingId`, `containsCount`, `barcode`, a dimension, or `packagings` for the eleventh. |
| 409 | *Barcode taken* | "A variant already carries the barcode '…'." / "A packaging already carries the barcode '…'." |
| 409 | *Packaging cycle* | "'Inner' cannot hold 'Pallet', which is built from it." / "'Box' cannot hold itself." |
| 409 | *Packaging in use* | "'Pallet' is built from 'Box'. Change or remove 'Pallet' first." — on a delete. |

A **variant's** write keeps its title *Duplicate barcode* and now also refuses a GTIN a
packaging carries: "A packaging with barcode '…' already exists." A product's create
answers its detail unchanged.

### Locks

Every packaging write is one transaction, locking in this order:

1. **the variant's row `FOR UPDATE`** — so one variant's packagings change one writer at
   a time, and never while a posting holds the variant `FOR SHARE` through
   `LockVariants`, which therefore reads packagings that hold until it commits;
2. when the write carries a barcode, the transaction-scoped advisory lock
   **`products:barcodes`** (`pg_advisory_xact_lock(hashtext('products:barcodes'))`),
   under which the barcode is checked against every variant and every packaging and then
   written — the cross-table half of the uniqueness, which no index can state; the
   partial unique index `ux_variant_packagings_barcode` states the in-table half;
3. the variant's packaging rows.

The variant writes take the same advisory lock after their own row, never before it: a
variant's change is now one transaction (its row `FOR UPDATE`, the advisory lock, the
update), and a variant's add and a product's create take the advisory lock inside
theirs. So the two orders agree and two writes cannot both claim one barcode.

A packaging is deleted with its variant (`ON DELETE CASCADE`).

## Images

A product has images, which a person shows a customer in a message (Messenger design
N13–N15; Communications reads them through the catalog's two image calls, never through
the `products` schema). Products keeps them in `products.product_images`
(`00131_products_images.sql`): `product_id` (`ON DELETE CASCADE`), the object keys of the
picture and its thumbnail, `content_type`, `byte_size`, `width`, `height`, `sha256`,
`alt_text` (null, at most 250 characters), `sort_order`, `is_primary`, `uses` and
`created_at` / `created_by`. A product is archived, never deleted, so its images go only
when they are deleted one by one.

### Normalisation at upload

`POST /api/v1/products/{id}/images` takes one file in the multipart field `file`, and
**stores only the normalised file**:

1. The type is decided by the bytes: a JPEG or a PNG. Anything else — WebP and HEIC
   included — is **422 `image_type_unsupported`**.
2. More than 10 MiB is **422 `image_too_large`**. `image.DecodeConfig` then reads the size
   before anything is decoded: more than **40 megapixels** is also `image_too_large`, so
   a decompression bomb costs nothing; a side under **200 px** is **422
   `image_too_small`**; a file that cannot be decoded is **422 `image_invalid`**.
3. The EXIF orientation is read by `imagemeta.Orientation`, which walks a JPEG's
   segments (or a PNG's chunks) to the one tag and never decodes the pixels.
4. The picture is decoded, scaled so its longer side is at most **2048 px** (Catmull-Rom,
   never upscaled), turned upright and encoded again: JPEG at quality 90, or PNG. This
   drops every piece of metadata — Exif, XMP, IPTC, a PNG's text chunks — so no camera
   location reaches a customer. The ICC colour profile goes with it, so a wide-gamut photo
   may look slightly less saturated. An orientation that cannot be read is taken as 1
   (upright): the picture has already decoded.
5. A **320 px** thumbnail of the same type is made in the same pass, with
   `golang.org/x/image/draw`.

The two files go in the object store under the scope **`products-images`**, keys
`{productId}/{uuid}` and `{productId}/{uuid}-thumb`, never derived from the file's name,
and written before the row's transaction and removed again if it does not commit. A store
that cannot be written is **503 `storage_unavailable`** with nothing recorded. A body
that is no multipart form, has no file or more than one is 400. The router's body cap for
the upload is 10 MiB and the framing.

**Bounds on the cost of an upload.** A 40 megapixel picture needs hundreds of megabytes
to decode, so at most **3 uploads are normalised at once** per process (a semaphore
around the normalisation alone, never held while the store or the database is called);
the others **wait on the request's own context** rather than failing, and a client that
goes away stops waiting. The upload also carries the rate-limit policy
`ProductsImageUpload`: **600 uploads an hour per client address** (staff's document
upload's number; an office behind one address shares it), 429 with `Retry-After`. Image refusals are the one place Products' problems
carry a `code`.

### The primary image and the order

Every write is one transaction that first takes the product's advisory lock
`pg_advisory_xact_lock(1130, productId)` — not a row lock on the product, which an
Inventory posting holds `FOR SHARE`. The rules:

- **The first image uploaded is primary**, and a new image is last. At most one image of a
  product is primary, by the partial unique index `ux_product_images_primary`.
- **Making another image primary** (`isPrimary: true`) clears the old one first. `false`
  on the primary image is a 400: another image's `true` is what replaces it.
- **Deleting the primary promotes the next** by `sort_order`.
- **`sortOrder` is a place**, not a free number: `PATCH` with it moves the image to that
  place (past the last is the last), and the others close the gap. Every image's
  `sort_order` is kept as 0, 1, 2… with no gaps, renumbered by every move and delete.
- **`uses`** says where the image may be shown: `communications` — to a customer in a
  message; the default for a new image — and `marketing`, for marketing use such as ads,
  which nothing reads yet. Communications reads `communications`: a message's cards are made of a product's
images marked `communications` — `ProductImages(productID, "communications")` — and a reply
that names an image no longer marked is refused as `product_image_unavailable`
([A product in a message](/en/reference/communications/#a-product-in-a-message)). A CHECK keeps the column within those two words; a `PATCH`
  with any other is a 400, and an empty list is allowed.

### Endpoints and permissions

| Operation | Permission | |
| --- | --- | --- |
| `GET /{id}/images` | `products-view` | The images in their order; 404 for an unknown product. |
| `POST /{id}/images` | `products-manage` | Upload, as above; 201 with the image. |
| `PATCH /{id}/images/{imageId}` | `products-manage` | The alt text (an empty text clears it), `sortOrder`, `isPrimary` and `uses`; a field left out is left as it is; 200. |
| `DELETE /{id}/images/{imageId}` | `products-manage` | Deletes the row and, once that has committed, both objects (a failure to remove one is logged and leaves it unreferenced); 204. |
| `GET /{id}/images/{imageId}/content` | `products-view` | Streams the normalised file; `?size=thumb` the thumbnail. `Cache-Control: private, max-age=300`, `X-Content-Type-Options: nosniff`, no `Content-Disposition`. 404 for an image the product does not have or whose object is gone, 503 when the store cannot be read. |

No new permission is added: the ten that exist cover it. The upload and the content are
file operations, never offered to the Assistant.

### The catalog's image calls

`contracts.ProductCatalog` has two more calls, with `int32` ids:

| Method | Answers |
| --- | --- |
| `ProductImages(productID, use)` | The product's images marked for `use` (`communications` or `marketing`), by `sort_order` then id; empty when it has none, the product does not exist or `use` is neither. A `ProductImage` carries `ID`, `ProductID`, `AltText` (`""` when none), `ContentType`, `Width`, `Height`, `Uses` and `SortOrder`. |
| `OpenProductImage(imageID)` | The normalised file's stream and its `ProductImage`; `(nil, ProductImage{}, nil)` when the image does not exist or its object is gone. The caller closes the stream. |

Like the rest of the catalog they run on the pool, before the caller opens a transaction,
and check no user permission: the composer's product picker, which runs on the Products
API with the user's own permissions, is what checks the user.

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
| `GET /api/v1/products/{id}/variants/{variantId}/packagings` | List a variant's packagings |
| `POST /api/v1/products/{id}/variants/{variantId}/packagings` | Add a packaging |
| `PUT /api/v1/products/{id}/variants/{variantId}/packagings/{packagingId}` | Replace a packaging |
| `DELETE /api/v1/products/{id}/variants/{variantId}/packagings/{packagingId}` | Remove a packaging no other is built from |
| `GET /api/v1/products/{id}/images` | List a product's images |
| `POST /api/v1/products/{id}/images` | Upload an image (multipart, normalised) |
| `PATCH /api/v1/products/{id}/images/{imageId}` | Change an image's alt text, order, primary flag and uses |
| `DELETE /api/v1/products/{id}/images/{imageId}` | Delete an image and its files |
| `GET /api/v1/products/{id}/images/{imageId}/content` | Stream an image, or its thumbnail with `?size=thumb` |
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

## The product list's extra columns

The product list takes **`extraColumns`**, a function of the page's rows answering
extra columns, which the host fills. With Inventory on and both `inventory:locations-view`
and `inventory:stock-view`, the host adds **On hand**: each product's total across its
variants, from one `GET /api/v1/inventory/stock` per page — per 100 of the page's
variants — blank for a Service. Products
itself reads nothing of Inventory on the server.

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
