---
title: Products
description: The catalog of goods and services, categories, tax categories and prices.
sidebar:
  order: 60
sources:
  - apps/products/frontend
---

The **Products** app is the catalog of everything your organisation sells: physical
goods and performed services alike, each with its prices and its VAT rate. Its sidebar
has three areas: **Products**, **Categories** and **Tax categories**. Each area is shown
only to people who hold its permission, so your sidebar may be shorter than a
colleague's — see [Permissions](#permissions) at the end of this page. The rules behind
every screen are in the [Products reference](/en/reference/products/).

## Find a product

Open **Products** in the sidebar. The list shows every product with its name, **SKU /
variants**, **Type**, **Category**, **Status**, **Unit**, **Tax category** (with its
rate) and the **Current NOK price**; the badge next to the heading is the total count.
The list is paged, 25 products at a time.

- **Search by name or SKU...** narrows the list as you type.
- **Status** keeps only **Draft**, **Active** or **Discontinued** products.
- **Category** keeps the products in one category and in its subcategories;
  **Uncategorised** keeps the products that have no category at all.

The filters are part of the page address, so a filtered list can be bookmarked or
shared. Click a row to open the product; the pencil at the end of the row opens
**Edit product** without leaving the list. When nothing matches, the list says **No
products found.**

The search box in the top bar (**Search products...**) finds products by name or SKU
from anywhere in the app after two characters, and opens the one you pick.

## Create a product

Click **New product** on the Products list. The dialog first asks what you are adding,
because the choice shapes which details the product asks for:

- **Goods** — a physical item you stock, ship or hand over.
- **Service** — work or access you deliver, with nothing to ship.

Pick one (you can go back with **Change**) and fill in:

- **Name** — required.
- **Description** — free text, optional.
- **Category** — optional; the list shows the whole hierarchy, indented.
- **Tax category** — required; each entry shows its rate. See
  [Tax categories](#tax-categories).
- **Status** — **Draft** until you change it. Only you decide when a product becomes
  **Active**.

Click **Create product**. Vantigo creates the product with one default variant whose SKU
is the product name in upper case with `-001` appended (`OFFICE-CHAIR-001`), and confirms
with **Product created**. The SKU, unit, cost and logistics details live on that variant
and are edited on the product page — see [Variants and units](#variants-and-units).

A product cannot be created when the generated SKU already exists, since SKUs are unique
company-wide; rename the product or edit the SKU of the existing one. Every rule is in
the [reference](/en/reference/products/#domain-model).

## Edit a product

Open the product and click **Edit product**, or use the pencil on the Products list. The
dialog is the same as when creating, with two additions: **Type** can be switched between
**Goods** and **Service**, and **Status** between **Draft**, **Active** and
**Discontinued**. Click **Save changes**; the confirmation is **Product updated**.

Editing a product changes only its shared details; SKU, unit, cost, logistics and prices
belong to the variant and are changed on the product page instead.

## The product page

Clicking a product opens its page, with the status badge next to the name. The
**Product details** card shows **SKU**, **Type**, **Category** (as its full path, for
example *Furniture / Chairs*), **Barcode**, **Unit**, **Standard cost**, **Tax
category** with its rate, **Current price** in every currency that has one, and the
description. For goods, a **Logistics** section shows **Weight** and **Dimensions
(L×W×H)**, or says that none are recorded. Below are the **Prices** card and, when the
product has more than one variant, the **Variants** card.

## Variants and units

A variant is what is actually sold: it carries the **SKU**, the **Unit**, the **Unit
cost** and its own prices. A product always has at least one, and one per combination
when it is sold in several colours or sizes.

When a product has more than one variant, the **Variants** card on the product page
lists them with their **Option values**, **Unit**, **Cost** and **Prices**, and offers
**Add variant**. The pencil opens **Edit variant** and the bin removes a variant. Both
dialogs ask for:

- **SKU** — required and unique across the catalog.
- **Option values** — what makes this variant different, written as
  `Color=Blue, Size=M`.
- **Unit** — how the product is counted, for instance `pcs` or `hour`; this is the
  unit shown on the product and used by other modules.
- **Unit cost** — what the item costs you, optional.

Click **Save variant**. Vantigo refuses to change a SKU once the product is no longer a
draft, refuses a SKU or barcode that another variant already uses, and refuses to remove
the last variant (**A product must keep at least one variant.**).

The barcode, weight and dimensions on the product page are variant fields too, but the
app does not edit them; they are set through the API.

## Prices

The **Prices** card lists the price rows of the product's first variant: **Currency**,
**Amount**, **Valid from** and **Valid to**. Prices are excluding VAT; the VAT follows
the product's tax category. A row with no dates is **Open-ended** — the base price — and
a row with a validity window is a campaign price, which wins over the base price while
it runs. The row in force right now carries the **Current** badge. A product without
rows says **No prices configured.** and shows no price in the list.

Click **Add price** and fill in **Currency** (an ISO 4217 code such as `NOK`),
**Amount (ex VAT)** and, for a campaign, **Valid from** and **Valid to** in your local
time (**Valid to** is exclusive). Leave both dates empty for the base price. Click
**Add price**.

The pencil opens **Edit price**, which warns that **Editing rewrites this price row**:
recorded transactions keep the price they snapshotted, but anything re-reading the row
sees the new values, so for a planned change prefer a new row with a validity window.
The bin asks to confirm **Delete price**, which also removes the row from the history.

Vantigo refuses a row that overlaps another of the same kind in the same currency: two
NOK base prices, or two NOK campaigns whose windows overlap. A base price and a campaign
can coexist.

## Archive a product

A product is never deleted, because other modules may reference it. Instead, open the
product and click **Archive**; the confirmation **Archive product** explains that the
product is marked as discontinued and can no longer be sold, but is kept for historical
references. The status becomes **Discontinued** and the **Archive** button disappears.

Discontinued products stay in the list, where the **Status** filter finds them; to bring
one back, set its **Status** to **Active** in **Edit product**.

## Categories

Open **Categories** in the sidebar. Categories form a hierarchy; each product belongs to
at most one category, and filtering the product list by a category also matches the
products in its subcategories. The cards at the top count **Total categories**, **Root
categories**, the **Max depth** of nesting, **Empty categories** (no products in the
subtree) and **Uncategorised products**. The table lists the tree, indented, with the
number of products in each category and, when subcategories add more, the count **in
subtree**. A category with nothing in its subtree is marked **Empty**.

To create one, click **New category** and fill in **Name** and, optionally, **Parent
category** — leave it at **None (root category)** for a top-level category. Click
**Create category**. The pencil opens **Edit category**, where a category can be renamed
or moved under another parent; the bin asks to confirm **Delete category**.

Vantigo refuses two categories with the same name under the same parent, refuses to
move a category under one of its own descendants, and refuses to delete a category that
still has subcategories or products. Move or reassign them first.

## Tax categories

A tax category is a centrally configured VAT rate. Every product points to one instead
of carrying its own rate, so the rate shown next to a product is always the tax
category's current rate, and changing it changes it for every product at once. Prices
are entered without VAT; a sale adds the rate of the product's tax category at the time.

Open **Tax categories** in the sidebar. The table shows each category's **Name**,
**Kind** and **Rate**. Click **New tax category** and fill in:

- **Name** — required and unique.
- **Kind** — **Standard**, **Reduced**, **Zero** or **Exempt**.
- **Rate (%)** — enter `25` for a 25 % rate; 0 to 100.

Click **Save**; the confirmation **Tax category saved** means the change is live. The
pencil edits a category, the bin deletes one. A tax category that products still use
cannot be deleted (**This tax category is in use and cannot be deleted.**): move those
products to another tax category first.

## Where products are used

- **Projects** — a project's billing lines are pinned to a product variant. The
  **Product variant** picker in a billing line searches **Service** products only, by
  name or SKU, and needs you to have access to products; the line then shows the
  product's name, SKU and unit to everyone on the project, even without a products
  permission. See
  [billing lines](/en/reference/projects/#billing-lines-and-the-optional-products-dependency).
- **Invoices** — invoice lines are written on the invoice itself (description,
  quantity, unit, unit price and VAT code) and do not read the catalog. See the
  [Invoices reference](/en/reference/invoices/).

## Permissions

| What | Permission |
| --- | --- |
| See the Products area, the list and a product page | `products:products-view`, `products:variants-view`, `products:pricing-view`, `products:categories-view` and `products:tax-categories-view` together |
| Create, edit and archive products | `products:products-manage` (creating also needs `products:variants-manage`) |
| Add, edit and remove variants | `products:variants-manage` (editing and removing also need `products:pricing-manage`) |
| Add, edit and delete prices | `products:pricing-manage` |
| See the Categories area | `products:categories-view` |
| Create, edit and delete categories | `products:categories-manage` |
| See the Tax categories area | `products:tax-categories-view` |
| Create, edit and delete tax categories | `products:tax-categories-manage` |

A screen you lack the permission for is not shown in the sidebar; an action you lack the
permission for is refused when you try it. See the
[reference](/en/reference/products/#permissions).
