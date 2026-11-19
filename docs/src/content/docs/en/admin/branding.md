---
title: "Branding"
description: "The workspace's brand: its name, colour and logos, draft and publish, the one-time import of APP_TITLE and APP_LOGO_URL, and what stays Vantigo's."
sidebar:
  order: 42
sources:
  - apps/server/internal/identity
  - openapi/identity.yaml
  - packages/frontend-shell/src/brand
  - apps/host/frontend/src/routes/workspace
---
A **brand** is how the workspace looks to its people: a name and a title, one colour, a few
logos and the support contacts. Vantigo has one brand today, the **default brand**, edited at
**Workspace admin › Brand** by an Owner or by anyone who holds the `identity:brands` permission
("Manage brands", delegable like any other permission under **Roles & access**). The screen is
described for the person using it in
[Workspace administration](/en/user/workspace-administration/#brand).

A brand is never free CSS. You give Vantigo **one primary colour** and it generates the rest, in
light and in dark. That is why the colour is checked and why there is no field for anything else.

## What a brand holds

| Part | Content |
| --- | --- |
| Name | 1 to 100 characters. What the administration shows. |
| Title | Up to 60 characters; the name when left empty. The product name in the browser tab, the header and the sign-in page. |
| Primary colour | One `#rrggbb`. Vantigo builds the ten shades from it and keeps your colour exactly as shade 6. |
| Corner radius | `xs`, `sm`, `md`, `lg` or `xl`. |
| Logos | A mark for light backgrounds, a mark for dark backgrounds, a square icon and an optional wordmark. |
| Support | An e-mail address, a phone number and a link, each optional. |

Everything above is part of one **version**. Editing any of it changes the **draft**; the
published version, which is what people see, does not move until you publish.

The generated values are worked out on the server every time a draft is saved. Nothing in the
API accepts a colour scale from a client: a request that carries `tokens`, `shades` or anything
else beyond the inputs is refused with `400 invalid_request`.

## The colour and why it is checked

The primary colour has to be one a scale can be built from: its lightness must lie between 35 %
and 85 % (OKLCH). A colour nearer white or black is refused with `422 brand_primary_range`.

Vantigo then checks every pair of colours the brand puts next to each other, in **both** colour
schemes, against WCAG AA (a ratio of at least 4.5 : 1 for text):

- the text on a filled button, and on the same button hovered;
- a link on the page, on a card and on a tinted table row.

The text colour on a button is chosen, not assumed: white on a dark primary, black on a light one.
A draft may be **saved** while it fails; it can only be **published** when every pair passes. The
editor shows each ratio and, when a pair fails, offers the nearest primary that passes with one
click. A very light primary can fail the links, because they need a darker colour than the primary
itself. The hover colour of a button with black text is kept dark enough to read, so a bright red,
orange or blue does not fail it.

Publishing a failing draft through the API is refused `422 brand_contrast` and names the failing
pairs; use the suggested neighbour or choose a darker colour.

Vantigo's own indigo (`#4f46e5`) keeps the hand-tuned scale the product has always had, so an
installation that never edits its brand looks exactly as before.

## Logos

Logos are uploaded on the **Brand** screen, one per slot, and follow the same rules as the logo on
invoices: a PNG or a JPEG decided by the file's own bytes (never SVG), at most 512 KiB, between
16 and 2,000 pixels on its sides, and re-encoded on upload so no metadata is kept. The **icon**
must be square within 5 %. A brand without a mark for dark backgrounds shows its light mark on a
light chip in dark mode.

An uploaded logo goes into the **draft** and is shown to administrators only; it is served to
everyone, without a session, once the brand is published. Files are stored by the hash of their
bytes and **never deleted**, since a document may carry one. They live in the `identity` scope of
the [object store](/en/admin/object-storage/#brand-logos). Without an object store the brand works
fully, without logos, and the upload controls say why.

## Draft, publish and "preview as me"

1. Change the brand and **Save draft**. Only administrators can see the draft.
2. **Preview as me** puts the whole application in the draft's look for you, in this browser tab
   only, with a banner to stop. It never changes anything for anyone else.
3. **Publish** makes the draft the brand everyone sees. The version number goes up. A person
   with the application open gets the new brand the next time the page loads.
4. **Discard draft** drops the pending version.

Saving and publishing use the brand's revision (`If-Match`): two administrators cannot overwrite
one another; the second is told to reload.

## How the page gets its colours without a flash

The brand reaches the browser as a stylesheet, `GET /api/v1/identity/brand.css`, linked in the
page head so the first paint is already right. Without a session it is the default brand; with a
session it is the signed-in person's brand. It is `Cache-Control: private` with an `ETag` and
`Vary: Cookie` for a signed-in person, so a shared cache never serves one person's brand to
another. If a reverse proxy sits in front, let `Vary: Cookie` through.

If the stylesheet cannot load (offline, a failed request), the browser paints the last brand it
mirrored in local storage, but only for a mirror of a version it understands. The mirror is
removed when the person signs out.

The sign-in page, the setup page, password recovery, accepting an invitation and the signed-out
page show the default brand's title, favicon and colours, from `GET /api/v1/identity/brand/public`
(anonymous, rate limited per address, cached for a minute). The pay page, the RSVP page and
stations ask for no brand at all and keep the default brand until invoices carry their own.

## The one-time import of `APP_TITLE`, `APP_LOGO_URL` and `APP_SUPPORT_*`

These variables used to brand the whole installation. They are now **imported once into the
default brand**, the way `OIDC_*` became the single sign-on card:

- On the first start of a version that has brands, the API creates the default brand, published,
  from Vantigo's own values and whichever of the variables are set: the title (and the name, from
  it) and the three support contacts. The same validation as the editor applies; a contact that is
  not valid is left out and named in the log.
- `APP_LOGO_URL` is **fetched after the application is up**, never while it starts. The address
  must be `http` or `https`; no redirect is followed; it must answer within 15 seconds with at
  most 2 MiB; and it may not point at a private, loopback, link-local or cloud-metadata address.
  A path beginning with `/` (such as `/logo.png`) names one of the application's own files and is
  read from them directly, without a request to the installation's address; the base path, if
  there is one, may be included or left out. The whole import, the object store included, must
  finish within 90 seconds. The picture must pass the logo rules above and becomes the mark for
  light backgrounds. An SVG is not accepted.
- If the logo cannot be imported (unreachable, refused address, not an image, over the limit, no
  such file, no object store, too slow) the title and support contacts are still in the brand, a
  warning is logged and the **Brand** screen shows a notice asking you to upload the logo. The
  import is then **not complete**: `APP_TITLE` and `APP_LOGO_URL` stay in effect as before until
  you upload a logo and publish the brand, and the next start fetches the logo again.
- Several replicas starting together create **one** default brand and only one of them fetches
  the logo; a replica that stops mid-fetch is replaced after two minutes.
- **Until the import has completed**, the variables stay honoured exactly as before, so a rollback
  to an older release loses nothing. It completes when the logo has been imported, or when an
  administrator publishes the default brand with a logo of their own. After it, the application
  ignores the variables from the next start and logs a line while they are still set. You can
  remove them from your environment.

## What stays Vantigo's

The documentation site at `/docs` and the API reference keep Vantigo's own look. A brand changes
the shades of the primary colour, links and tinted backgrounds, the corner radius and the logos;
the page, card, text and border colours stay Vantigo's in both schemes.

## What can go wrong

| Sign | Cause |
| --- | --- |
| `422 brand_primary_range` | The colour is nearly white or nearly black. |
| `422 brand_contrast` | A pair is below 4.5 : 1; the response names the pairs. |
| `422 brand_support_invalid` | The e-mail has a display name or holds `?`, `&` or `%`, the phone has other characters than digits, spaces and `+ - ( ) .` or is over 30 long, or the link is not `http(s)` or a path starting with a single `/`, or holds a control character such as a tab or a line break. The same rule applies to `APP_SUPPORT_URL` and `APP_LOGO_URL` at start. |
| `409 stale_revision` | Someone else saved the brand since you opened it. |
| `400 logo_*` | The file is not an acceptable PNG or JPEG: `logo_unsupported_type`, `logo_too_large`, `logo_too_wide`, `logo_too_small`, `logo_invalid`, `logo_not_square`. |
| `503 storage_unavailable` | No object store, or it could not be written. |
| A notice about the logo of `APP_LOGO_URL` | The import could not fetch it; upload the logo and publish. Until then the variables stay in effect. |
