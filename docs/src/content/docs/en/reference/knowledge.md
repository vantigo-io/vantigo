---
title: Knowledge module
description: "The Knowledge module (nb Kunnskap): the articles tree with fractional positions and a depth of 10; the access model — the nearest member row, else the nearest default, bounded by knowledge:articles — materialised under one advisory lock, the Workspace, Shared and Private sections, and what knowledge:manage may and may not do; the document allowlist and the plain text derived from it; autosave with expectedVersion, the lock, the history and its coalescing; the trash and its groups; favourites; search in Norwegian and English; the Assistant's two tools and Hidden from the Assistant; the live topics; the retention worker and the erasure sweep; the editor's licences; the endpoints and their refusals."
sidebar:
  order: 125
sources:
  - apps/server/internal/knowledge
  - openapi/knowledge.yaml
  - apps/server/internal/openapi/specs/knowledge.yaml
  - apps/server/internal/db/migrations/00135_knowledge_baseline.sql
  - apps/knowledge/frontend
---

Knowledge is the company's own handbook: routines, policies and how-tos, written as
**articles** in a tree. This page describes the module as the code implements it; the
screens are in the [user guide](/en/user/knowledge/). The module requires no other module and
mounts at `/api/v1/knowledge` when `knowledge` is in `MODULES`. Its API reference is
generated from `openapi/knowledge.yaml`.

## Permissions

| Permission | Delegable | What it opens |
| --- | --- | --- |
| `knowledge:articles` (**Use Knowledge**) | yes | The module: every operation except the trash's *empty*. What a person may then do with an article is the article's own access. No role has it by default. |
| `knowledge:manage` (**Administer Knowledge**) | no | See and change the access of every article that is **not private**, restore anyone's trashed article that is not private, and empty the trash. It never opens a private article. |

`knowledge:articles` bounds everything below: a person without it is refused 403 before any
article is looked at, whatever the access rows say.

## The schema

Schema `knowledge`; migration `00135_knowledge_baseline.sql`.

| Table | Holds |
| --- | --- |
| `articles` | `id`, `parent_id`, `position`, `title` (at most 200 characters), `icon` (1–16 characters or null), `content` (`jsonb`, the document), `content_text` (the derived text), `version`, `locked`, `ai_hidden`, `internal_permission` (`write`, `read`, `none` or null), `trashed_at` and `trash_root` (both set or both null), the creation and last-change stamps with their users, and `search`, a generated `tsvector`. A root must have an `internal_permission`; an article is never its own parent. |
| `article_members` | The access a person is **given** on an article: `(article_id, user_id, permission)`, at most 500 per article. |
| `article_access_default` | Per article, the **effective default** (`permission`) and the effective `ai_hidden` — materialised. |
| `article_access` | Per article and person, the **effective** access through a member row — materialised. |
| `article_versions` | `(article_id, version)` with the title, the document, who and when. |
| `favourites` | A person's favourites, ordered by a `position` key. |

User ids are stored as plain uuids, with no foreign key into identity; names are looked up
through `contracts.UserDirectory` when an answer needs them, and a user it no longer knows is
shown as a former user.

## The tree

Articles form a tree of **at most 10 levels** (a root is level 1). Siblings are ordered by
`position`, a **fractional base-62 key** compared bytewise (`COLLATE "C"`, digits
`0-9A-Za-z`, never ending in the lowest digit), so a key can always be made between two
neighbours and a move renumbers nothing. A create or move takes `afterId` (the sibling to go
after; null puts it first) and the server makes the key. `afterId` that is not a sibling under
the parent is `400 after_invalid`.

`GET /tree` answers the nodes the caller can read, with their `section` and `permission`; a
subtree whose root the caller cannot read is not shown, and a node whose children are all
unreadable has `hasChildren` false.

## Access

### The rule

For a person and an article, the **effective access** is:

1. the **nearest member row** for that person on the path from the article up to its root — but
   only as far up as the nearest article that **sets its own default** (see below); if there is
   none,
2. the **nearest `internal_permission`** on that path (the article's own default, else its
   parent's, and so on); a root always has one.

**An article that sets its own default is detached.** Its `internal_permission` is not null
(a root's always is), and the member rows on its **ancestors do not apply** to it or to anything
below it: only its own member rows and those of its descendants count. Defaults, by contrast,
are still inherited when an article does not set one. So a private page stays private even if
someone is added as a member on a parent page, and a page with its own access is unaffected by
later changes to its parent's people. An article that leaves its default unset (null) follows
its parent in both respects. The Share dialog says *This page has its own access. People added
on parent pages don't apply here.* for a detached page.

Levels are `write` (can edit), `read` and `none`. A member row can lower as well as raise
(`none` hides the article from one person). The whole is **bounded by `knowledge:articles`**.
`ai_hidden` is separate and **monotone**: an article is hidden from the Assistant when it or any
ancestor is.

### Materialisation

Reading never walks the tree. The effective default of every article is kept in
`article_access_default` and every person's member-derived access in `article_access`. Every
write that can change them — create, move, restore, an access change, a hidden-flag change,
erasure — runs `recompute` for the changed subtree **under one advisory lock for the whole
tree** (`TreeLock`), in the same transaction, from the subtree root's parent's rows (which are
outside the subtree and so current). Writes to a tree are therefore serialised; reads are not
blocked.

### Sections

The sidebar's section is derived, never stored:

| Section | Rule |
| --- | --- |
| `workspace` | The effective default is not `none`: everyone with the permission has at least that access. |
| `private` | The default is `none` and the caller is the **one and only** writer. |
| `shared` | Anything else: the default is `none` and the article reaches the caller through a member row, with other people or other writers. |

An article is **private** (the word the rest of this page uses) when its default is `none` and
it has exactly one writer. A new root is created in `workspace` (`internal_permission` `write`)
or `private` (`none`, with the caller as its one `write` member).

### Who may change what

- A person with `write` on an article may change it, its access, move it and trash it.
- Someone who can read but not write gets **403 `article_read_only`**.
- Someone who cannot read it gets a **bare 404** — never 403 — so an article's existence is not
  revealed; this holds for a trashed article, an unknown id and one the access hides alike.
- With `knowledge:manage`, a person **sees and changes the access** of an article that is not
  private (`GET`/`PUT /access`), restores such a trashed group and empties the trash, **but
  cannot open it**: reading its content is 404 unless they are also a member with access. A
  private article is a 404 to them for everything.

### Changing access

`PUT /articles/{id}/access` replaces the article's own default (`internalPermission`, null to
inherit; **required for a root**: `400 root_permission_required`) and its member list in one
transaction, then recomputes the subtree. The list may name at most 500 distinct, existing
people (`400` with field errors otherwise). After the change the article must keep a writer:
either its effective default is `write`, or at least one person has `write` — otherwise
`400 last_writer`. The check covers the **whole subtree** below the article that the change reaches, so
a change cannot leave a descendant without a writer. A change that makes a **private** article part of the workspace, or the
reverse, answers `409 visibility_change` unless the body carries `confirmVisibility: true`; the
interface asks first. `GET` answers the effective permission, the section, `canManage`, the own
default or `inheritedFrom` (the nearest ancestor that has one, shown only if readable), and the
members with, for an inherited one, the article it comes from.

### Moving and restoring across roots

A move takes `parentId` (null for a root), `afterId`, an optional `section`
(`workspace` or `private`, only for a root) and `confirmVisibility`. The rules, in order:

- the new parent must be readable (`400 parent_invalid` otherwise; an `afterId` the caller cannot read is `400 after_invalid`) and **writable** (403 `article_read_only`);
- not under itself or a descendant: `400 move_into_descendant`;
- the subtree's height plus the new parent's depth must stay within 10: `400 too_deep`;
- an article that **becomes a root keeps its readers**: its effective default becomes its own
  `internal_permission` and the member rows it inherited are copied to its own, unless `section` says
  where it goes — `workspace` sets the default to `write`; `private` sets `none`, copies the
  members and makes the mover a writer;
- a move between private and the workspace is `409 visibility_change` without `confirmVisibility`.

A restore to a root (below) follows the same copy-and-keep rule.

## Documents

An article's content is a **ProseMirror JSON** document. The server **stores no HTML** and never
emits any: the browser renders the JSON through the editor's schema, so an allowlist replaces a
sanitiser.

### The allowlist

A document is a node `doc` and nothing else at the root; a node, mark or attribute not listed is
refused with the JSON path of the first one found (`400 invalid_document`, the path in the field
error, for example `$.content[2].marks[0].attrs.href`). Nesting is limited to 64 levels.

| Node | Attributes | Notes |
| --- | --- | --- |
| `doc`, `paragraph`, `listItem`, `taskList`, `bulletList`, `blockquote` | none | |
| `heading` | `level` 1–3 | |
| `orderedList` | `start` (a whole number), `type` (`1`, `a`, `A`, `i`, `I` or null) | |
| `taskItem` | `checked` (boolean) | |
| `codeBlock` | `language` (1–32 of `A-Za-z0-9_+#.-`, or null) | |
| `horizontalRule` | none | leaf |
| `hardBreak` | none | leaf, inline |
| `text` | none | carries a non-empty string and the marks |

| Mark | Attributes |
| --- | --- |
| `bold`, `italic`, `strike`, `underline`, `code` | none |
| `link` | `href` (required), `target` (`_blank` or null), `rel` (`noopener noreferrer nofollow` or null), `class` (null), `title` (text up to 500 bytes or null) |

A link's **`href`** must be an `https:` or `http:` URL (with a host), a `mailto:` address, or an
application path starting with a single `/`; `//host`, `javascript:`, `data:`, a backslash, a
space or any control character is refused, and an href over 2048 characters too.

A document is at most **2 MiB** as JSON: over that is `413 document_too_large` (the router's
body cap is 3 MiB, so the handler answers rather than the router cutting off). The list is
mirrored in the editor: `apps/knowledge/frontend/scripts/export-schema.ts` exports the editor's
schema and a test fails when it and the allowlist drift apart.

### Plain text

The text derived from a valid document (`content_text`) is what search indexes and what the
Assistant reads. Blocks are separated by a blank line and written Markdown-like: `# ` before a
heading, `- `, `1. ` and `- [x] ` before list items, `> ` before a quote, fences round a code
block, `---` for a rule. Marks add nothing, and **a link contributes only its text**, never its
target. It is computed on every save and restore.

## Saving, the lock and the history

`PUT /articles/{id}/content` (the autosave) takes `title`, `content` and `expectedVersion`. The
caller needs write access. In order: a locked article answers `409 article_locked`; a stale
`expectedVersion` answers **`409 version_conflict`** with `currentVersion`, `updatedAt` and
`updatedBy` (the person who saved), so a client can offer *reload* or *keep mine* — keeping
mine is a save with `expectedVersion` set to `currentVersion`. A save adds a **version**, with
two refinements:

- a save by the **same author within 10 minutes** of their previous save replaces that version
  instead of adding one (coalescing), and still raises the article's `version`;
- a [version restore](#history) is never coalesced.

`PATCH /articles/{id}` changes `title`, `icon` (up to 16 characters), `locked` and `aiHidden`,
and needs write access; it ignores no-ops. A title change on a locked article is
`409 article_locked`. **Changing the title bumps the version** (it is saved
as a version of the same document) and is subject to the same coalescing. Locking stops *content* saves, version restores and title changes; icon, access, move and trash
are still allowed. Locking and unlocking send `meta.changed` and `tree.changed` (the sidebar shows the lock). The
hidden flag is a tree write: it takes the tree lock and recomputes the subtree's `ai_hidden`.

### History

`GET /articles/{id}/versions` lists the versions, newest first, and `GET .../versions/{n}` reads
one with its document. `POST .../versions/{n}/restore` (with `expectedVersion`) saves that
version's title and document as a **new** version, so the version it replaces stays in the
history; it is refused like a content save (locked, stale). Versions older than 365 days are
pruned by the [retention worker](#the-retention-worker), always keeping the newest 20.

## The trash

`POST /articles/{id}/trash` trashes the article **and its subtree** in one transaction, as one
**trash group** (`trash_root` is the article). A trashed article is invisible everywhere
except the trash: not in the tree, search, favourites or the live topic authorisation, and its
access reads as 404.

`GET /trash` lists the groups whose root the caller could write — with `knowledge:manage`,
every group that is not private — newest first, each with `articleCount` (the articles in the group), `parent` and `purgeAt` (30 days after `trashedAt`).

`POST /articles/{id}/restore` restores the **whole group** of the article, whichever member is
named (`groupRestored` says the named one was not the root). The caller must administer the
group's root (write access, or `knowledge:manage` and not private); anyone else gets 404. An
article that is not in the trash is `409 not_trashed`. If the old parent is itself trashed, the
group's root goes to the **root of the tree** keeping who can read it (inherited members copied,
its default made its own), and the answer has `movedToRoot`. `DELETE /trash` (`knowledge:manage`)
deletes every trashed article for good, with its versions.

## Favourites

A person's favourites are theirs alone, ordered by a fractional key. `PUT`/`DELETE
/favourites/{id}` add (last; one already there keeps its place) and remove, `POST
/favourites/{id}/move` puts one after another (first when `afterId` is null), and the list shows
only articles the person can still read. The retention worker removes the ones they cannot.

## Search

`GET /search?q=&limit=` finds articles by title and text, in **Norwegian and English at once**:
`articles.search` is a generated `tsvector` of the title (weight A) and of the first **200 000
characters** of the text (weight B), each in the `norwegian` and `english` configurations,
indexed with GIN. The query is read with `websearch_to_tsquery` (quoted phrases, `or`, `-`) in
both languages, and the **last word is also matched as a prefix**, so a search finds as the
person types; a query ending in a quote, an operator or inside a phrase has no prefix word.
Results are ranked with `ts_rank_cd`. `q` is 1–200 characters and `limit` 1–50 (default 20), else
`400`. Only articles the caller can read are found, never a trashed one; each hit carries its
`path` (the readable ancestors, cut at the first the caller cannot read) and an **excerpt** as
segments `[{text, match}]` from `ts_headline` — the match markers are control characters removed
from the text first, so nothing in an article can pass for a match or for markup.

## The Assistant

The Assistant ([reference](/en/reference/assistant/#knowledge-as-a-source)) reaches Knowledge
through exactly **two** operations, both `GET`s with mode `auto`: `searchKnowledge` and
`getKnowledgeArticleText`. Every other operation is `x-vantigo-assistant: never`, so the Assistant
cannot read a document, the access, the history or the trash, and cannot change anything.

`GET /articles/{id}/text?offset=` answers the plain text in **pieces of 12 000 characters** from
`offset`; `nextOffset` is set while more remains, so a long article is read in parts.

**Hidden from the Assistant** (`ai_hidden`) is enforced when the principal comes through the
Assistant (`Via` = assistant): search leaves hidden articles out, the text operation answers 404
for one, and an article's `path` is cut at a hidden ancestor. Because the flag is monotone down
the tree, hiding a page hides its whole subtree. It is **not an access rule**: nobody who could
read the article loses it, and the interface and the API serve it as before.

## Live

The module declares two live topics. Events carry identifiers only; a
client that hears one refetches.

| Topic | Authorised by | Events |
| --- | --- | --- |
| `knowledge.article:<id>` | `knowledge:articles` and read access to the article, not trashed (an unknown article and an unreadable one are alike refused). Presence is allowed: *viewing* and *editing*. | `content.changed` (a save or a version restore), `meta.changed` (title, icon, lock, hidden flag, access, a move, a restore) and `trashed`, sent for **each article in a trashed group** (a group of more than 200 articles notifies only its root; `tree.changed` is sent either way). |
| `knowledge.tree` (no id) | `knowledge:articles` | `tree.changed`, sent on create, a title or icon change, a lock or unlock, a move, an access change, trash, restore and an erasure. Never for a content save that keeps the title, and never with an id, which would reveal that a hidden article exists. |

Each is notified on the transaction that writes it, so it is delivered only if the write commits.

## The retention worker

`knowledge.retention` runs **every 24 hours**. A cycle takes an advisory lease
(`pg_try_advisory_lock`) on a pooled connection of its own, so one replica runs it and it is
safe on a pool of one, then:

1. **purges the trash** — every trash group trashed more than 30 days ago, deleted for good with its versions, under the tree lock;
2. **prunes the versions** older than 365 days beyond each article's newest 20;
3. **removes the favourites** of articles their person can no longer read;
4. and, after the connection is back in the pool and only on the replica that won the lease, runs the **erasure sweep**.

### Erasure

The sweep asks `contracts.UserDirectory` which of the users Knowledge holds anything of still
exist, 200 ids at a time. For each erased user, in one transaction under the tree lock, it deletes
the **private roots** (and their subtrees and versions) of which they were the **only writer**,
then their **member rows**, their **favourites**, and sets their **authorship to null** (shown as
a former user); what they wrote in shared articles stays. It then sends `tree.changed`. It is
idempotent.

## Endpoints

All under `/api/v1/knowledge`, all requiring `knowledge:articles` except where noted. `{id}` is an
article id; the generated API reference (`/en/reference/api/knowledge/`) has the schemas.

| Method and path | What it does |
| --- | --- |
| `GET /tree` | The sidebar's nodes the caller can read. |
| `POST /articles` | Create (title, icon, content, `parentId`, `afterId`, and for a root `section`). A subpage needs write access to the parent. |
| `GET /articles/{id}` | The article with its document, `section`, `permission`, `aiHidden` and `aiHiddenInherited`, `path` and stamps. |
| `PATCH /articles/{id}` | Title, icon, `locked`, `aiHidden`. |
| `PUT /articles/{id}/content` | The autosave, with `expectedVersion`. |
| `POST /articles/{id}/move` | Move. |
| `GET`/`PUT /articles/{id}/access` | Read and replace the access. |
| `GET /articles/{id}/text` | The plain text in pieces (also the Assistant's). |
| `POST /articles/{id}/trash`, `POST /articles/{id}/restore` | Trash and restore a group. |
| `GET /articles/{id}/versions`, `GET .../versions/{n}`, `POST .../versions/{n}/restore` | The history. |
| `GET /assignable-users?query=` | Active users (display names only, at most 20) for the access dialog's picker, from the user directory (an article's writer need not hold the identity permission that lists users). |
| `GET /favourites`, `PUT`/`DELETE /favourites/{id}`, `POST /favourites/{id}/move` | Favourites. |
| `GET /search` | Search (also the Assistant's). |
| `GET /trash`, `DELETE /trash` | The trash; *empty* needs `knowledge:manage`. |

### Refusals

Refusals are RFC 9457 problems whose `code` names the rule; a client keys off the code, never the
words. Field errors of a 400 are in `errors`, keyed by the request's camelCase field.

| Status | `code` | When |
| --- | --- | --- |
| 400 | `move_into_descendant` | The new parent is the article or below it. |
| 400 | `too_deep` | The tree would be more than 10 levels deep. |
| 400 | `parent_invalid` | The parent is unknown to the caller, unreadable or trashed. An article's `parentId` is shown as null to a caller who cannot read the parent. |
| 400 | `after_invalid` | `afterId` is not a sibling under the parent, or is one the caller cannot read. |
| 400 | `invalid_document` | A node, mark or attribute outside the allowlist; the field error names the path. |
| 400 | `root_permission_required` | A root's `internalPermission` was set to null. |
| 400 | `last_writer` | The change would leave the article without a writer. |
| 400 | — | Field errors: title over 200 characters, icon over 16, more than 500 or duplicate members, an unknown user, a level that is not `write`, `read` or `none`, a search `q` or `limit` out of range. |
| 403 | `article_read_only` | The caller can read the article but not write it. |
| 403 | — | The permission is missing (`knowledge:articles`, or `knowledge:manage` for *empty*). |
| 404 | — (empty body) | No article the caller can read — unknown, unreadable, private to someone else, trashed, or hidden from the Assistant. |
| 409 | `version_conflict` | `expectedVersion` is not current; carries `currentVersion`, `updatedAt`, `updatedBy`. |
| 409 | `article_locked` | A content save, title change or version restore on a locked article. |
| 409 | `visibility_change` | The change moves the article between private and the workspace and `confirmVisibility` was not given. |
| 409 | `not_trashed` | A restore of an article that is not in the trash. |
| 413 | `document_too_large` | The document is over 2 MiB. |

## Limits

| What | Limit |
| --- | --- |
| Title | 200 characters |
| Icon | 16 characters |
| Members per article | 500 |
| Tree depth | 10 levels |
| Document | 2 MiB as JSON; 64 levels of nesting |
| Search | the first 200 000 characters of the text are indexed; `q` 200 characters; `limit` 1–50, default 20 |
| Text piece | 12 000 characters |
| Version coalescing | the same author within 10 minutes |
| Trash | 30 days |
| Versions | 365 days, beyond the newest 20 |

## The editor and its licences

The editor in `apps/knowledge/frontend` is built on TipTap and ProseMirror. Every package it
uses is **MIT-licensed**: `@tiptap/starter-kit`, `@tiptap/react`, `@tiptap/pm`,
`@tiptap/extensions`, `@tiptap/extension-link`, `@tiptap/extension-task-list`,
`@tiptap/extension-task-item` and `@mantine/tiptap`. No paid or source-available TipTap extension
is used.
