# Owner and tags — design

Phase 4 of the Customers roadmap ("Light CRM"), delivery **A**: who owns the relationship,
and how customers are classified. Two features that answer the first question a sales
or account manager asks of a customer list — *which ones are mine?* — and the first one
an administrator asks — *which ones are of this kind?* Delivery B (own spec) is typed
contact roles with a primary contact; C is follow-ups; D is customer groups with
defaults. Attachments wait for the storage module's model.

## Decisions

### D1 — One owner, a user, on the customer row

`customers.customers` gains `owner_user_id uuid NULL` (migration `00024`). The owner is
a **single user** of this installation (`contracts.UserDirectory`), never a free text and
never a team: the roadmap's "account manager" is one person accountable for the
relationship, and every system compared (Salesforce's Account Owner, HubSpot's Company
owner, Business Central's Salesperson code) models it as exactly that.

- `PUT /customers/{id}/owner` (`customers:update`), body `{ownerUserId: uuid | null,
  revision?: int}`: sets or clears the owner. The row is written, so `revision` bumps
  and the optional expected revision is honoured exactly as contact info's PUT does
  (409 "Customer revision conflict" when stale; a no-op writes nothing). The user must
  **exist and be active** to be assigned — a field error on `ownerUserId`, worded as
  projects words it (`User <id> does not exist`, `User <id> is disabled and cannot own a
  customer`); an owner who is disabled *afterwards* keeps the customer (nothing is
  silently revoked; the UI shows the name with an "inactive" hint and the picker offers
  a replacement). Answers the customer (`SafeCustomerResponse`).
- `GET /customers/assignable-users?query=&limit=` (`customers:update`): the directory's
  `SearchUsers` — active users only — capped at 20, as projects' assignable-users
  endpoint. Answers `[{userId, displayName}]`.
- `SafeCustomerResponse` (list and detail) gains `owner?: {userId, displayName, active}`
  — omitted when unowned. Display names come from one batched `Users(ids)` call per
  response, **outside any transaction**; a user the directory no longer knows is shown as
  `Unknown user`, `active: false` (the `actorFor` precedent).
- List filter `ownerId`: a user id, or the literal `me` (the caller), or `none`
  (unowned). Validated in Go with the list's own wording (`'ownerId' must be a user id,
  'me' or 'none', but was '…'.`). "My customers" in the UI is `ownerId=me`; the same
  filter is what a manager uses for "unassigned".
- Timeline: `customer.owner_changed`, `{customerId, before: {userId, displayName} |
  null, after: …}`, recorded only when the owner actually changed, with the acting user.
- The directory stays as it is: no consumer needs the owner today
  (`CustomerEntry` is "enough to name a customer, never enough to manage it"). When a
  cross-module "my customers" view or Invoices asks, `OwnerUserID` is one added field.

### D2 — Tags, the way communications already has them

Two tables (migration `00024`): `customers.tags(id uuid PK, name varchar(100) NOT NULL,
color varchar(20) NULL)` with a unique index on `lower(name)` (a tag is a vocabulary;
`VIP` and `vip` are one word), and `customers.customer_tags(customer_id, tag_id, PRIMARY
KEY (customer_id, tag_id), ON DELETE CASCADE both ways)`. Colour is one of Mantine's
named colours (`gray red pink grape violet indigo blue cyan teal green lime yellow
orange`) or null — validated in Go (`A tag colour must be one of … , but was '…'`), so the
UI never has to sanitise it.

- `GET /customers/tags` (`customers:view`): every tag, name-ascending.
  `POST /customers/tags` (`customers:update`) `{name, color?}` → 201 `Tag`; 409 with
  `code: tag_exists` on a duplicate name. `PUT /customers/tags/{tagId}` renames or
  recolours (same 409). `DELETE /customers/tags/{tagId}` removes the tag from every
  customer (204; the cascade). Name: 1–100 UTF-16 units, trimmed.
- `PUT /customers/{id}/tags` (`customers:update`) `{tagIds: uuid[]}` **replaces** the
  customer's set — the natural write for a multi-select — and answers `{tags: Tag[]}`.
  An unknown id is a field error on `tagIds`. Off the customer row: no `revision`, no
  customer lock beyond the row's existence check; two concurrent replaces are last-wins,
  which is what a set-replace means.
- `SafeCustomerResponse` gains `tags: Tag[]` (always present, `[]` when none), loaded
  in one query per list page.
- List filter `tagId` (one tag, as communications' inbox filters by one tag; a customer
  matches when it carries the tag). Multi-tag filtering is not built until someone
  asks.
- Timeline: `customer.tags_changed`, `{customerId, added: [{tagId, name}], removed:
  [{tagId, name}]}`, only when the set changed. Renaming a tag records nothing on the
  customers that carry it (the tag is the vocabulary, not the customer).

### D3 — What the user sees

- **Customer list**: an **Owner** column (display name, or "—"), tag chips after the
  name, and two new filters beside status and type: **Owner** (All / Mine / Unassigned)
  and **Tag** (All / one tag). The host passes nothing new: "Mine" is the API's `me`.
- **Overview tab**: an **Owner** row in the summary card with the name (and "inactive"
  when the directory says so) and, with `canEdit`, a picker — a `Select` fed by the
  assignable-users search with a debounce, the current owner kept in the options even
  when the search would drop it (projects' assignee picker), plus **Clear**. A **Tags**
  row with the chips and, with `canEdit`, a `MultiSelect` over every tag with
  create-on-the-fly (typing a new name offers *Create "x"* → `POST /customers/tags`,
  then the set replace). Both writes reload the customer the delivery-A way
  (`syncCustomerRevision` after the owner PUT; `invalidateQueries` after tags).
- **Tags management**: a small **Manage tags** modal from the list page's Tag filter
  (with `canEdit`): the table of tags with rename, colour and delete (delete confirms
  and says how many customers carry it — the count comes with the list:
  `GET /customers/tags` answers `{id, name, color, customerCount}`).
- Both catalogs (en + nb) for every string.

### D4 — Search and permissions

`search` does not match owner names or tag names (search stays what it is: the
customer's own fields). Everything here is readable with `customers:view` and writable
with `customers:update`: an owner is not sensitive data and tags are classification; a
narrower key would be one more thing to configure for no protection gained. The list's
`ownerId=me` needs a principal — an unauthenticated call is already a 401 before any of
this.

## Out of scope

Teams or several owners; ownership history beyond the timeline event; auto-assignment
rules; notifying a new owner (the timeline is the record; notifications are a platform
concern); tag hierarchies or tag-based permissions; exposing owner/tags through
`CustomerDirectory` (until a consumer asks); "Assign to me" as a one-click (the picker
finds the caller like anyone else — a `currentUserId` prop from the host can add the
shortcut later); customer groups with defaults (delivery D).

## Testing

Backend through `modtest` with the users fake: owner PUT sets/clears/validates
(missing, disabled, stale revision, no-op), the owner appears on list and detail with the
name from the directory and `Unknown user` when it is gone, `ownerId=me|none|uuid|junk`,
assignable users capped and active-only, the timeline event only on change. Tags: CRUD
with the duplicate 409 (case-insensitive), colour validation, set replace (unknown id,
idempotent, event only on change, cascade on tag delete), `tagId` filter, `customerCount`.
Frontend: list filters drive the URL and the request (filtered by method+URL, never "the
last fetch"), owner picker and tags multi-select against wire-shaped fixtures (one
literally the body with nothing set), the manage-tags modal, both catalogs.
