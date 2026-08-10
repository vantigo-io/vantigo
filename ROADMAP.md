# Roadmap

Planned evolution per app. Each phase notes why it exists and what it unblocks.
Other apps add their own sections as their roadmaps solidify.

## Products

### Phase 1 — Catalog enrichment (done)

Table-stakes catalog fields identified from industry research (ERPNext, Odoo,
Business Central, Shopify, Medusa, Akeneo): plain-text description, a multi-level
category hierarchy (single category per product), GTIN barcodes with check-digit
validation and uniqueness, and logistics fields (weight and dimensions).

*Unblocks:* a usable catalog UI, category-based navigation and reporting, barcode
lookup, and shipping-cost estimation groundwork for Orders/Warehouse.

### Phase 2 — Variants

Split the model into **Product** (shared identity: name, description, category,
type, status, VAT) and **ProductVariant** (sellable identity: SKU, barcode, unit,
standard cost, prices, weight/dimensions, and option values such as `Color=Red`).
Every product has at least one variant; single-variant products keep an inline UX.
Migration: each existing product becomes a product with one default variant;
`ProductPrice.ProductId` moves to `VariantId`.

*Unblocks:* selling size/colour assortments without SKU duplication.
**Hard constraint:** must be complete before the Orders service starts — order
lines will reference variants, and retrofitting the split after orders exist would
be a breaking data migration across services.

### Phase 3 — Tax categories

Replace the per-product `VatRate` value with a reference to a **TaxCategory**
(Standard/Reduced/Zero/Exempt) whose rates are configured centrally.

*Unblocks:* rate changes without touching every product, differentiated goods vs.
food vs. exempt handling, and correct tax snapshots for Orders.

### Phase 4 — Pricing depth (align with Orders)

Extend `ProductPrice` with price lists, customer-group prices and quantity breaks,
with an explicit, documented precedence order.

*Unblocks:* B2B negotiated pricing and volume discounts; designed together with
the Orders service so order lines resolve prices the same way the catalog does.

### Phase 5 — Operational readiness (align with Warehouse)

Unit-of-measure conversions (base unit + conversion factor) and supplier records
per product/variant (vendor SKU, lead time, minimum order quantity).

*Unblocks:* purchasing and warehouse receiving; designed together with the
Warehouse service.

### Later

- **Bundles/kits** — explicit composition relationships between products.
- **Media/images** — needs a platform file-storage decision first.
- **Localization** — translated names/descriptions.
- **Typed custom attributes (PIM)** — schema-defined attributes per category.
- **Tags/collections** — the designated answer for cross-cutting, multi-assignment
  grouping (e.g. "Summer sale"), deliberately separate from the single-assignment
  category hierarchy.
