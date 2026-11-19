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
logos and the support contacts. A workspace can have several brands, and rules say who sees
which ([Several brands](#several-brands), [Who sees which brand](#who-sees-which-brand)). They are
edited at **Workspace admin › Brand** by an Owner or by anyone who holds the `identity:brands` permission
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

## Several brands

A workspace can hold any number of brands. The **default brand** is the one a person sees when no
rule matches them; it always exists. On **Workspace admin › Brand**, the **Brands** tab lists them
with their state, the default marked, and how many rules point at each. From the list you can:

- **Create** a brand from a name and a primary colour. It starts as a draft that was never
  published, and matches no rule until you publish it.
- **Duplicate** a brand: a new draft made from the version being edited, logos included, named
  "… (copy)".
- **Make default** a published brand. The previous default stays a brand of its own.
- **Archive** a brand to take it out of use. It can no longer be edited, assigned or chosen, its
  rules are removed and the choice of anyone who had picked it is cleared. The default brand
  cannot be archived (`409 brand_in_use`); make another brand the default first.
- **Restore** an archived brand: published again if it ever was, else a draft. Its rules are not
  restored.
- **Delete** a brand that was never published. A published brand is archived instead, because
  documents may name it. A deleted brand's rules go with it.
- **Export** a brand as a JSON file and **Import** one: the file holds the name, title, colour,
  radius and support contacts, never the logos or any generated colour. An import creates a draft
  and works everything out again, so a brand can move between a test and a production installation.

A brand's name is unique whatever its case (`409 brand_name_taken`).

### Where else a brand applies

Besides who sees which brand in the application, a brand can be a **customer's default brand**:
the look and name a new invoice or order for that customer starts in. It is set on the customer, or
on a customer group for all its members (**Customers**, the **Relationship** card and **Manage
groups**; see [Brand](/en/user/customers/#brand)), by anyone who may edit customers, with no need
for `identity:brands`. Only published brands are offered.

Archiving a brand does not touch any customer that points at it and is not blocked by one. Those
customers fall back to their group's brand, then to the default brand, and say so; restoring the
brand ends the fall-back. A brand changes the look and the name on a document, never who the
seller is: the legal name, organisation number, VAT registration and bank account stay the single
invoice settings.

## Who sees which brand

A person sees one brand: their own choice if they have one and still may, otherwise the first
assignment rule that matches them, otherwise the default brand. It is worked out on every request
from current data and never stored, so a change of group or employment shows without anyone
re-assigning.

### The rules

The **Assignment** tab holds one ordered list of rules for the whole workspace. The first rule
that matches a person decides their brand. A rule has a kind and a target:

| Kind | Matches a person when |
| --- | --- |
| E-mail domain | The domain of their e-mail address equals the rule's domain exactly: no wildcards, and `mail.acme.no` does not match `acme.no`. Written in lower case and punycode. A domain belongs to at most one brand. |
| Access group | They are an effective member of the group, counted as role mapping counts it (forced in, or present upstream with no override forcing them out), and the group is active. |
| Legal entity | They have an employment in force today under a business unit of the Staff legal entity. Offered only while Staff is enabled. |

Drag a rule, or use the arrows, to change the order; **Save rules** replaces the whole list at
once, with the list's revision as `If-Match`, so two administrators cannot interleave half-edits
and a refused list changes nothing. A new rule is placed where its kind belongs (domains, then
legal entities, then groups) and can be moved anywhere. Refusals: `rule_duplicate` (the same
target twice), `domain_taken` (the domain belongs to another brand), `domain_invalid`,
`rule_target_not_found`, `rule_brand_invalid` (no such brand, or archived), `staff_disabled` and
`too_many_rules` (at most 200), all `422`, and `409 stale_revision`.

A rule whose target has gone (a deleted or inactive group, an archived legal entity) is kept,
flagged on the screen, and matches no one. Rules never point at an archived brand. An integration
or an agent is not a person and always gets the default brand.

### A person's own choice

A person matched by several brands may choose among the brands of the rules that match them: the
avatar menu then has a **Brand** section listing them, with **Automatic** to follow the rules
again. The default brand is a candidate only for someone no rule matches. The choice is kept on
the person's account like their colour scheme, beats the rules for as long as it is one of their
candidates, and is not audited. When the rules change so that it no longer is, it is ignored (the
menu says so) and comes back if the rules do. `PUT /api/v1/identity/account/brand` with a brand
that is not a candidate is `409 brand_not_candidate`. Archiving a brand clears the choice of
everyone who made it.

### Who sees what

On the **Assignment** tab, type a person's e-mail address under **Who sees what** to see which
brand they get and why: their own choice, the rule that matched, or the default, and the other
brands they could choose. It needs `identity:brands`. It shows no employment detail: only the
rule that matched is named. A caller who also holds `staff:view`, while Staff is enabled, also
gets the person's e-mail domain, access groups and legal entities.

### When Staff is off

Legal entities come from Staff through a read-only contract, `contracts.BrandTargets`. While
Staff is not enabled the screen offers no legal entity rules, and the ones already in the list
stay, flagged and inert; they work again when Staff is turned on. A failing Staff read fails the
request rather than showing the default to someone who should have another brand.

### How open sessions follow

A publish, a new default, an archive, a restore, a delete and a change of the rules are announced
to open sessions on the live connection (`identity.brand`); a person's own switch is announced to
their other sessions on `identity.brand:{userId}`, which only they may subscribe to. A session
asks for its brand again after a random wait of up to five seconds, so a publish on many open
sessions is not that many requests at once. What Staff says about a person's employment is kept for
30 seconds, and shared between requests that ask at once; everything else is read afresh.

## Sign-in by e-mail domain

The sign-in page starts in the default brand. When a person types an e-mail address whose domain
you attached to a brand with an [e-mail domain rule](#the-rules), the page fades into that brand's
colours, logo and title over about a third of a second, so a customer's staff sees their own
company when they sign in. Forgot password, reset password and accepting an invitation do the same
for the address they know. If the domain is changed to one that is not attached, or the field is
emptied, the page fades back. People who prefer reduced motion get the change at once, with no
fade. The fade is only a look: it never decides whether anyone can sign in, and SSO buttons are
unaffected. Once someone has signed in, their own brand, as [assigned above](#who-sees-which-brand),
takes over.

To use it, attach the customer's domain on the **Assignment** tab and publish the brand. A brand
that is only a draft, or is archived, is never shown here.

**What becomes public.** The brand's name, title, colours and logos are readable by anyone who
types or requests an attached domain; they are public brands. Do not attach a domain whose brand
is confidential. The lookup reveals that a domain is attached, and nothing about the people on it:
it answers the same for a domain with accounts and one without, and an unattached domain gets the
default brand in the same shape.

**What reaches the server.** The browser sends only what follows the `@`, in
`GET /api/v1/identity/brand/public?domain=acme.no`, once the text looks like a domain (a dot, and a
last part of two or more letters) and has been still for 400 ms or the field is left. The part
before the `@` is never sent, and the server refuses a value that holds an `@` with
`400 domain_invalid`. The typed domain is a query parameter, so it appears in the access logs of
the server and of any proxy or CDN in front of it, which are yours to protect and rotate; Vantigo's
own logs and counters record only a hash of the domain. Answers may be cached for five minutes, so
a change of the rules or a publish reaches the sign-in page within that time.

**Rate limit.** The lookup allows 30 requests a minute per client address; past that the answer is
`429` with `Retry-After`, and the page keeps the brand it has. A rising count of
`vantigo.identity.brand.public_lookups` with `result=limited` is the sign of someone probing for
domains. The counter also has the results `matched`, `unmatched`, `invalid` and `default`.

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
3. **Publish** makes the draft the brand everyone assigned to it sees. The version number goes
   up. A person with the application open gets the new brand within a few seconds
   ([How open sessions follow](#how-open-sessions-follow)).
4. **Discard draft** drops the pending version.

Saving and publishing use the brand's revision (`If-Match`): two administrators cannot overwrite
one another; the second is told to reload.

## How the page gets its colours without a flash

The brand reaches the browser as a stylesheet, `GET /api/v1/identity/brand.css`, linked in the
page head so the first paint is already right. Without a session it is the default brand; with a
session it is the signed-in person's brand, the one they are assigned or chose. For a signed-in
person it is `Cache-Control: private, no-store` with an `ETag` and `Vary: Cookie`, so a shared cache never serves one person's brand to
another. If a reverse proxy sits in front, let `Vary: Cookie` through.

If the stylesheet cannot load (offline, a failed request), the browser paints the last brand it
mirrored in local storage, but only for a mirror of a version it understands. The mirror is
removed when the person signs out.

The sign-in page, the setup page, password recovery, accepting an invitation and the signed-out
page show the default brand's title, favicon and colours, from `GET /api/v1/identity/brand/public`
(anonymous, rate limited per address, cached for a minute), until a typed address names another
brand ([Sign-in by e-mail domain](#sign-in-by-e-mail-domain)). The pay page, the RSVP page and
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
| `400 domain_invalid` | A lookup by domain held something that is not a domain, such as an address with an `@`. |
| `409 stale_revision` | Someone else saved the brand, or the rule list, since you opened it. |
| `409 brand_name_taken` | Another brand has the name. |
| `409 brand_archived` | You tried to edit, publish or discard the draft of an archived brand; restore it first. |
| `409 brand_in_use` | You tried to archive the default brand, or to delete a published brand or the default. |
| `409 brand_not_candidate` | A person chose a brand their rules do not give them. |
| `422 rule_duplicate`, `domain_taken`, `domain_invalid`, `rule_target_not_found`, `rule_brand_invalid`, `staff_disabled`, `too_many_rules` | A rule list the server refused; `fields` names the item. |
| `400 logo_*` | The file is not an acceptable PNG or JPEG: `logo_unsupported_type`, `logo_too_large`, `logo_too_wide`, `logo_too_small`, `logo_invalid`, `logo_not_square`. |
| `503 storage_unavailable` | No object store, or it could not be written. |
| A notice about the logo of `APP_LOGO_URL` | The import could not fetch it; upload the logo and publish. Until then the variables stay in effect. |
