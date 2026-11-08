---
title: Knowledge module
description: "The Knowledge module (nb Kunnskap): the articles tree with fractional positions and a depth of 10; the access model — the nearest member row, else the nearest default, bounded by knowledge:articles — materialised under one advisory lock, the Workspace, Shared and Private sections, and what knowledge:manage may and may not do; the document allowlist (callouts, toggles, tables, images, attachments, mentions, article and record links) and the plain text derived from it; files in articles; links, backlinks and mentions; Markdown export and import; autosave with expectedVersion, the lock, the history and its coalescing; the trash and its groups; favourites; search in Norwegian and English; the Assistant's two tools and Hidden from the Assistant; the live topics; live co-editing — the update log, the binary frames, the seed, compaction, the reset endpoint and the collaborative save; the browser's side of co-editing; the retention worker and the erasure sweep; the editor's and the server's licences; the endpoints and their refusals."
sidebar:
  order: 125
sources:
  - apps/server/internal/knowledge
  - openapi/knowledge.yaml
  - apps/server/internal/openapi/specs/knowledge.yaml
  - apps/server/internal/db/migrations/00135_knowledge_baseline.sql
  - apps/server/internal/db/migrations/00136_knowledge_files_links.sql
  - apps/server/internal/db/migrations/00140_knowledge_coediting.sql
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

Schema `knowledge`; migrations `00135_knowledge_baseline.sql`, `00136_knowledge_files_links.sql` (files, links and mention notices) and `00140_knowledge_coediting.sql` (the update log).

| Table | Holds |
| --- | --- |
| `articles` | `id`, `parent_id`, `position`, `title` (at most 200 characters), `icon` (1–16 characters or null), `content` (`jsonb`, the document), `content_text` (the derived text), `version`, `locked`, `ai_hidden`, `internal_permission` (`write`, `read`, `none` or null), `trashed_at` and `trash_root` (both set or both null), the creation and last-change stamps with their users, and `search`, a generated `tsvector`. A root must have an `internal_permission`; an article is never its own parent. |
| `article_members` | The access a person is **given** on an article: `(article_id, user_id, permission)`, at most 500 per article. |
| `article_access_default` | Per article, the **effective default** (`permission`) and the effective `ai_hidden` — materialised. |
| `article_access` | Per article and person, the **effective** access through a member row — materialised. |
| `article_updates` | The update log of [live co-editing](#live-co-editing): `(article_id, seq)` primary key, `data` (`bytea`, an opaque Yjs update), `created_by` (null for an erased user, indexed where set) and `created_at`. `seq` is per article, `max(seq) + 1` under the article's row lock; rows go with their article (`ON DELETE CASCADE`). The log's state is on the `articles` row: `doc_epoch` (from 1, raised by every reset), `update_rows` and `update_bytes` (the log's size, so a write never scans it) and `last_compaction_ask_at`. |
| `article_versions` | `(article_id, version)` with the title, the document, who and when. |
| `article_files` | A file uploaded into an article: `id`, `article_id`, `object_key` (a random name in the object store, never derived from the file name), `filename` (1–255 characters), `content_type`, `size`, `is_image`, `created_by` and `created_at`. Deleted with its article. |
| `file_object_deletions` | The object keys whose file rows are gone, queued by a trigger on `article_files` and emptied by the [retention worker](#the-retention-worker). |
| `article_links` | `(from_id, to_id)`: the article links each article's document holds, rewritten on every save. `to_id` has no foreign key. |
| `mention_notices` | `(article_id, user_id, notified_at)`: when a person was last told they were mentioned in an article. |
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

Every block node also carries the optional attribute `id` (1–64 characters of `A-Za-z0-9_-`, or
null): the editor's block identity, a UUID by default. `hardBreak`, `detailsSummary`,
`detailsContent`, `tableRow`, `text`, and the inline atoms below do not carry it.

| Node | Attributes | Notes |
| --- | --- | --- |
| `doc` | none | |
| `paragraph`, `listItem`, `taskList`, `bulletList`, `blockquote`, `table` | `id` | |
| `heading` | `level` 1–3 | |
| `orderedList` | `start` (a whole number), `type` (`1`, `a`, `A`, `i`, `I` or null) | |
| `taskItem` | `checked` (boolean) | |
| `codeBlock` | `language` (1–32 of `A-Za-z0-9_+#.-`, or null) | |
| `horizontalRule` | `id` | leaf |
| `hardBreak` | none | leaf, inline |
| `text` | none | carries a non-empty string and the marks |
| `callout` | `kind` (`info`, `tip`, `warning` or `danger`), `emoji` (up to 32 characters or null) | |
| `details` | `open` (boolean) | a toggle; holds a `detailsSummary` and a `detailsContent` |
| `detailsSummary`, `detailsContent`, `tableRow` | none | |
| `tableHeader`, `tableCell` | `colspan` 1–100, `rowspan` 1–100, `colwidth` (a list of up to 100 widths of 1–10 000, or null) | |
| `tableOfContents` | `id` | leaf |
| `image` | `src` (required), `alt` (up to 1000 characters or null), `title` (up to 500 or null), `width` (1–10 000 or null) | leaf. `src` must be exactly `/api/v1/knowledge/files/` followed by a file's id. |
| `fileAttachment` | `fileId` (required, a UUID), `filename` (up to 255 or null), `size` (0 to 2^40 or null), `contentType` (up to 255 or null) | leaf |
| `mention` | `id` (required, a UUID), `label` (up to 200 characters or null) | leaf, inline: a person. The label is what the editor showed; it renders the current name. |
| `articleLink` | `id` (required, a UUID), `label` (up to 200 or null) | leaf, inline: a link to an article. **No title is stored**: the server sets the label to null on every save path (save, restore, import, create) and lower-cases the id. |
| `recordLink` | `path` (required), `label` (up to 200 or null) | leaf, inline: a link to a record elsewhere in Vantigo; the editor makes a chip only for a path whose first segment is an enabled module's route, and a URL with a query or fragment stays a plain link. `path` is an application path of letters, digits, `/`, `_` and `-` (1–500 characters after the first `/`), never starting with `//`. |

| Mark | Attributes |
| --- | --- |
| `bold`, `italic`, `strike`, `underline`, `code` | none |
| `link` | `href` (required), `target` (`_blank` or null), `rel` (`noopener noreferrer nofollow` or null), `class` (null), `title` (text up to 500 bytes or null) |

A link mark's **`href`** must be an `https:` or `http:` URL (with a host), a `mailto:` address, or an
application path starting with a single `/`; `//host`, `javascript:`, `data:`, a backslash, a
space or any control character is refused, and an href over 2048 characters too.

An `image` or `fileAttachment` may name only **its own article's files**: a save whose document
names any other file id (another article's, an unknown one, a malformed one) is
`400 invalid_document`, with the JSON path of the node in the field error.

A document is at most **2 MiB** as JSON: over that is `413 document_too_large` (the router's
body cap is 3 MiB, so the handler answers rather than the router cutting off). The list is
mirrored in the editor: `apps/knowledge/frontend/scripts/export-schema.ts` exports the editor's
schema and a test fails when it and the allowlist drift apart.

### Plain text

The text derived from a valid document (`content_text`) is what search indexes and what the
Assistant reads. Blocks are separated by a blank line and written Markdown-like: `# ` before a
heading, `- `, `1. ` and `- [x] ` before list items, `> ` before a quote, fences round a code
block, `---` for a rule. Marks add nothing, and **a link contributes only its text**, never its
target. Of the grown allowlist, a callout, a toggle and a table cell contribute their content (a
table is a line per row, cells separated by ` | `); a `mention` contributes `@` and its label (people's
names are not secret within the company); an `image` contributes its alt text and a
`fileAttachment` its file name; an **`articleLink` and a `recordLink` contribute nothing**, since
a label could be a title the reader may not see; the table of contents contributes nothing. It is
computed on every save and restore.

## Files in articles

`POST /articles/{id}/files` uploads one file into an article as `multipart/form-data` (the part
`file`). The caller needs write access and the article must not be locked (`409 article_locked`).
A file is at most **25 MiB**: over it is `413 file_too_large`. The request must carry exactly one
non-empty file, else `400`. The answer is `201` with `id`, `url` (`/api/v1/knowledge/files/{id}`,
exactly what an image's `src` holds), `filename`, `contentType`, `size` and `isImage`.

- A **JPEG or a PNG, judged by its bytes** and not by the declared type, is an **image**: its
  metadata (EXIF and the like) is stripped before it is stored. Anything else — SVG and HTML included — is an
  **attachment**, stored as sent and always served as a download of `application/octet-stream`;
  the declared type is kept for display only.
- The file name is the client's, without any path or control character, at most 255 characters
  (`file` when nothing is left).
- The bytes go to the object store under the module's scope at a random key; the row is written
  after. If the row's transaction does not commit the object is removed again. A store that cannot
  be reached is `503 storage_unavailable`, and nothing is changed.

`GET /files/{fileId}` serves a file to anyone who can read its article; a file of an article the
caller cannot read is the bare 404 an unknown id gets, and a missing object is 404 too. An image is
served **inline** with its type, an attachment as an `attachment` download. Every response carries
`X-Content-Type-Options: nosniff`, `Cache-Control: private, max-age=300` (named by a random id and
never replaced, but its article's access can change, so only the browser's own cache, and briefly) and
`Content-Security-Policy: default-src 'none'; sandbox`.

`POST /articles/{id}/files/copy` (`copyKnowledgeFile`, body `{fileId}`) copies a file of another
article into this one, as a new file with its own id and object, so a picture pasted from another
page keeps working. It answers `201` with the new `KnowledgeFile`. The caller needs write access to
the target and read access to the source file's article, else 404; it is never for the Assistant.

A file has no delete operation. The [retention worker](#the-retention-worker) deletes the files
that no article content and no version names, once they are **older than 7 days** (the editor may
upload and save a while later). A file row is also deleted with its article; either way a database
trigger queues its object in `file_object_deletions`, and the worker removes the object from the
store after it has released its lease (10 seconds allowed per delete) and then the queue row, trying again next cycle if the store refuses.

## Links, backlinks and mentions

**Links.** On every save and restore the server rewrites the article's rows in `article_links`
from the `articleLink` nodes its document holds, **to targets that exist**. A link to an article
deleted for good has no row; the editor shows it as *Deleted*.

`GET /articles/{id}/backlinks` lists the articles that link to the article, **only those the
caller can read** (a trashed article is not listed), each with `id`, `title`, `icon` and the
readable `path` of its ancestors (the article itself not included). The caller must be able to read the
article, else 404.

`POST /links/resolve` takes `{ids}` (at most 200, else `400`) and answers, in the order asked, one
`{id, state, title, icon}` per id: `ok` with its title and icon, `no_access`, or `deleted`. An
article the caller cannot read — **a trashed one included** — is `no_access`, and **its title and
icon are never answered**, so resolving reveals nothing a person could not read. Only an id that
no article has is `deleted`.

**Mentions.** When a save adds a `mention` to the document, the people **newly** mentioned (not
in the previous version of the document) are told, after the save has committed, if all of these hold:

- they hold `knowledge:articles` and can read the article;
- they have not been told of a mention in **that article within the last hour** (`mention_notices`);
- they are not the author of the save.

Notices are sent on create and on import as well as on save. Anyone else is skipped silently. The notice goes through the user directory in English and
Norwegian bokmål ("*Name* mentioned you in “*Title*”") with a link to the article when the installation has
an origin. A notice that cannot be sent is logged and never fails the save.

## Markdown export and import

### Export

`GET /articles/{id}/export?subtree=` answers the article as Markdown. The caller needs read access.

- One article with no files is a single `Title.md` (`text/markdown`).
- With `subtree=true`, or when the article uses files, the answer is a **zip** laid out the way
  Obsidian keeps a vault: `Title.md` beside a folder `Title/` holding its children's `.md` files
  in turn. **Only descendants the caller can read** are included. Names are the titles made safe
  (no separator or reserved character, at most 100 characters, `Untitled` for none); siblings that
  collide get ` (2)`, ` (3)`.
- The files a document uses are in `assets/<fileId>-<name>`, linked with a relative path; a file
  whose object is gone is left out. A store that cannot be reached is `503 storage_unavailable`.
- The zip is streamed. An export of more than **2000 entries** or an estimated **1 GiB** is `413 export_too_large`, checked before anything is written.
- The response is `Cache-Control: private, no-store` with `nosniff` and a download name.

The Markdown is CommonMark with GFM tables, task lists and strikethrough, and Obsidian's callouts:

| Document | Markdown |
| --- | --- |
| callout | `> [!info]`, `> [!tip]`, `> [!warning]`, `> [!danger]` |
| toggle | `> [!note]-` (closed) or `> [!note]+` (open) with the summary as its title |
| table | a GFM table |
| article link | `[[Title]]` when the caller can read the target, else `No access` |
| mention | `@Name` |
| record link | a Markdown link to its path |
| image, attachment | a relative link into `assets/`; the alt text or the file name when the file is not exported |
| table of contents, underline | dropped |

### Import

`POST /import` (`multipart/form-data`) takes `file` (a `.md` or a `.zip`), and optionally `parentId`
(an article to import under) and `section` (`workspace`, the default, or `private`, for imports at the root).
The caller needs write access to the parent; the tree depth limit applies. Answer `201`:
`created` (`id` and `title` of each article, in order) and `warnings` (messages, each once).

| Limit | Value |
| --- | --- |
| Upload | 50 MiB (`413 file_too_large`) |
| Entries in a zip | 500 |
| Uncompressed, counted as read, never trusted from headers | 200 MiB |
| Folder depth | 10 |
| A `.md` | 4 MiB (larger is imported empty, with a warning) |
| An embedded image | 25 MiB (larger is left out, with a warning) |

A zip over those limits, one that is not readable, or **one with an entry that reaches outside it
(path traversal, an absolute path, a backslash)** is `400` with a field error on `file`; so is a zip
with no `.md`. The rules:

- Dot-files and dot-folders (`.obsidian`, `.DS_Store`) and `__MACOSX` are skipped. A **single top folder** that holds everything is dropped.
- The folders become the tree. A **folder and a `.md` of the same name beside it are one article**
  (as Obsidian and Notion lay them out); a folder with no `.md` of its own becomes an empty article.
- The title is the file name; Notion's trailing 32-character id is stripped. A **first heading that
  repeats the title is dropped**.
- `[[wiki links]]` and relative links between the imported `.md` files become article links.
  Embedded **JPEG and PNG** images become the article's files (metadata stripped).
- What is not carried over is a **warning**: remote images, HTML, other file types, a file that is not
  UTF-8 text or that could not be converted (imported empty).
- A node, mark or attribute the allowlist refuses is dropped with a warning (labels are clamped to 200 characters, alt text to 1000). Images inside headings and table cells become their alt text and are not uploaded.
- At most **2 imports run at once per process**; another is `429 import_busy` with `Retry-After: 10`.
- It is **one transaction**: all the articles or none. Objects written before it are removed again
  if it does not commit. A store that cannot be reached is `503 storage_unavailable`. It sends `tree.changed`.

## Saving, the lock and the history

`PUT /articles/{id}/content` (the autosave) takes `title`, `content`, `expectedVersion` and
`mode`. The caller needs write access. `mode` is `single` (the default; the save described here)
or `collab` (a save by a client in [live co-editing](#the-collaborative-save)). With `single`,
`expectedVersion` is required (a missing one is `400`), and the save also
[deletes the article's live log](#the-log-is-reset). In order: a locked article answers `409 article_locked`; a stale
`expectedVersion` answers **`409 version_conflict`** with `currentVersion`, `updatedAt` and
`updatedBy` (the person who saved), so a client can offer *reload* or *keep mine* — keeping
mine is a save with `expectedVersion` set to `currentVersion`. A save adds a **version**, with
two refinements:

- a save by the **same author within 10 minutes** of their previous save replaces that version
  instead of adding one (coalescing), and still raises the article's `version`;
- a [version restore](#history) is never coalesced.

A `collab` save skips the version comparison and keeps everything else of this section.

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

The module declares three live topics. Events carry identifiers only; a
client that hears one refetches. The third, `knowledge.doc`, carries a document's bytes and
is described under [Live co-editing](#live-co-editing).

| Topic | Authorised by | Events |
| --- | --- | --- |
| `knowledge.article:<id>` | `knowledge:articles` and read access to the article, not trashed (an unknown article and an unreadable one are alike refused). Presence is allowed: *viewing* and *editing*. | `content.changed` (a save or a version restore), `meta.changed` (title, icon, lock, hidden flag, access, a move, a restore) and `trashed`, sent for **each article in a trashed group** (a group of more than 200 articles notifies only its root; `tree.changed` is sent either way). |
| `knowledge.tree` (no id) | `knowledge:articles` | `tree.changed`, sent on create, a title or icon change, a lock or unlock, a move, an access change, trash, restore and an erasure. Never for a content save that keeps the title, and never with an id, which would reveal that a hidden article exists. |

| `knowledge.doc:<id>` | The same as `knowledge.article:<id>`. No presence. | The document protocol of [Live co-editing](#live-co-editing): binary update frames and JSON control frames, not events. |

Each is notified on the transaction that writes it, so it is delivered only if the write commits.

## Live co-editing

The browser's editor merges concurrent edits itself (Yjs). **The server is an opaque relay:**
it stores and forwards update bytes and never decodes one, so there is no Yjs in Go. Search,
history, links, mentions, the Assistant and export keep reading the article's JSON
(`articles.content`), which a writer's client saves on a timer
([the collaborative save](#the-collaborative-save)); the live text is the log. The log is never
needed for safety: a seed always rebuilds from the JSON.

The platform side (the frame codec, the hub, `live.DocStore`) is in
[Platform packages](/en/contributing/platform-packages/#binary-frames-and-documents). This
module supplies the store (`doc_store.go`) behind the topic `knowledge.doc:<articleId>`, which
is authorised like `knowledge.article:<id>`.

### Who may write

A subscription is stored for when the person has **write access, the page is not locked and
not trashed**. The hub asks when the person subscribes and at every 60-second re-check (a failed
check counts as no), and the store asks **again inside every write**, under the article's row
lock, so a person demoted, a page locked or trashed a moment ago is refused at once
(`update_rejected read_only`).
A reader receives the log, live updates and others' awareness, and sends nothing: their update,
seed, compact and awareness frames are dropped and counted
(`vantigo.live.doc_dropped{reason=read_only}`); the first update answers `update_rejected
read_only`.

### The log

| Rule | Value |
| --- | --- |
| One update | at most 256 KiB |
| A seed, or a compacted row | at most 2 MiB |
| Awareness frame | at most 4 KiB |
| Compaction is **demanded** | the log is over 4 MiB or 2000 rows |
| Updates are **refused** (`log_full`) | the log would be over 8 MiB or 4000 rows, until a compaction lands; the refusal also asks for a compaction |
| Update, seed and compact frames | **one** bucket per connection: 50 a second, burst 200 (`update_rejected rate`, `seed_rejected`, `compact_rejected`) |
| Awareness frames | their own bucket: 30 a second, burst 60; over it they are dropped and counted |
| Binary frames for topics not subscribed to | the tenth in a minute closes the socket with `1008` |

The size of the log is read from the `articles` row (`update_rows`, `update_bytes`), maintained by every append, seed, compaction and reset. An append, a seed, a compaction and a reset each take the article's **row lock** (the one a
content save takes) for their short transaction and never the tree's advisory lock, so they
serialise with a content save of the same article and never with a move. An append notifies
`knowledge.doc:<id>` (`update`, the `seq`, the origin hub) on its own transaction.

### The frames

Binary frames: `kind` (1 byte), the topic's length (2 bytes, big endian), the topic (UTF-8,
at most 80 bytes), then the payload.

| Kind | Direction | Payload |
| --- | --- | --- |
| `0x01` update | client to server | a Yjs update, at most 256 KiB |
| `0x01` update | server to client | `seq` (8 bytes, big endian), then the update |
| `0x02` seed | client to server | the article `version` the document was built from (8 bytes, big endian), then a whole document, at most 2 MiB |
| `0x03` awareness | both | y-protocols' awareness update, at most 4 KiB; relayed as received |
| `0x04` compact | client to server | `upTo` (8 bytes, big endian), then a whole document, at most 2 MiB |

JSON control frames (text). The client sends `{"type":"doc_sync","topic","after","epoch"}` and
`{"type":"doc_error","topic","message"}` (an update it could not apply, counted as
`vantigo.live.doc_apply_errors`). `after` is the client's **contiguous watermark**: the newest
`seq` such that it holds every row up to it. `epoch` is the epoch it last synced at, 0 for none
(the first sync). The server sends:

| Frame | Meaning |
| --- | --- |
| `synced {topic, seq, epoch}` | the log up to `seq` has been sent; the subscription is bound to `epoch` and may write |
| `ack {topic, seq}` | the sender's update, seed or compaction is stored at `seq`; in the order sent |
| `update_rejected {topic, reason}` | in place of an `ack`, in order. `read_only`, `too_large`, `rate`, `log_full`, `stale` (the log was reset since the connection synced), `not_seeded` (the log is empty: seed first), `unavailable` (the store failed or timed out: keep the update and retry with backoff) |
| `seed_accepted {topic}`, `seed_rejected {topic}` | the answer to a seed (rejected: the log is not empty, the article moved on from the seed's `version`, a stale epoch, a rate or a store failure) |
| `compact_requested {topic, upTo}` | the log is over the demand size, or full; sent to the writer that has just stored or been refused, spaced a minute apart per document across replicas |
| `compact_rejected {topic}` | the compaction changed nothing |
| `doc_reset {topic, epoch}` | the log was deleted and is now at `epoch`: discard the copy, reopen, `doc_sync` with `epoch` 0. Also the answer to a `doc_sync` whose `epoch` is not current (a `seq` is reused after a reset), and sent with a `stale` refusal |
| `doc_stale {topic}` | rows may be missing (a row could not be read, or frames of the connection were discarded): `doc_sync` from the watermark. A replay during which frames were discarded sends no `synced` |

A subscription holds **no epoch until a `synced`**: updates, seeds and compactions before it, or
after a `doc_reset`, are refused `stale`. An append, a seed and a compaction compare the
subscription's epoch with the log's under the article's row lock and refuse a mismatch (and
push `doc_reset`), so a write racing a reset can never land in the new log. An append to an
empty log is `not_seeded`.

A malformed binary frame closes the socket with `1007`. A frame over its limit is answered
(`update_rejected too_large`, `seed_rejected`, `compact_rejected`) and not stored. A store
failure on a write is answered `unavailable` (`seed_rejected`, `compact_rejected`); only a
failed **replay** closes the socket with `1011`, and the client reconnects.

### Joining and relaying

After `ok`, the client sends `doc_sync` with `after` 0, or the newest `seq` it has. The server
streams the rows after `after` as update frames in `seq` order, then `synced`. The
subscription is registered before the log is read and the read is one statement, so a row
appended meanwhile is either in the replay or arrives as a later update; rows that arrive
during a replay are held and sent after `synced`. Replay frames wait up to 10 seconds for room
in the socket's queue; a socket that does not read its replay is closed with `1008`. A
`doc_sync` whose `after` is past the log's newest `seq`, or whose `epoch` is not the log's, is answered `doc_reset` and sent no row. A replay stops between rows when its subscription is gone.

An update is stored, acknowledged to its sender, and put at once on the queue of every other
subscription **on this hub**; it is never echoed to the sender's connection. Other replicas read
the row when the notification arrives (one by `(article, seq)`, a burst with one replay filtered
to the announced seqs), off the listener's loop. The replica that appended skips its own
notification and never reads its rows back, so an update is never echoed to its sender and a row
is pushed once. A row that compaction removed first gives `doc_stale`. Yjs is idempotent and commutative, so a duplicate
or a reordering is harmless.

### Seed

An article that was never co-edited has an empty log and a JSON document. The first writer to
open it sends **one seed**. The server accepts it **only if the log is empty**
(`insert … where not exists` under the row lock), so of many simultaneous seeds exactly one is
stored; it answers `seed_accepted` and `ack` with `seq` 1 and fans the seed out as an update.
The others get `seed_rejected` and must `doc_sync` instead. A seed also carries the article
`version` it was built from, and is rejected when the article is at another version: someone
saved the page since the client fetched it, and it must fetch again.

### Compaction

When the log is over 4 MiB or 2000 rows after a store, the writer gets `compact_requested
{upTo: newest seq}`. Its client answers only if it has applied every row up to `upTo`, with a
compact frame holding the whole document; it must cover **only up to its contiguous watermark**,
never beyond a gap. The server, in one transaction under the row lock,
deletes the rows with `seq` up to `upTo` and inserts one row at `upTo`, then acknowledges it. It
refuses (`compact_rejected`, nothing changed) when `upTo` is not above the log's first `seq` (a
late compaction: the log is contiguous from its first `seq`, and a compaction leaves its row at
`upTo`) or is above its newest, the data is over 2 MiB, or the sender cannot write. A
compaction is not fanned out: its state is the same as the rows it replaces.

### The log is reset

The JSON is the truth again, and the log is deleted, its counters zeroed and `doc_epoch` raised by one, in the same transaction, by:

- a **`single` save** of the content (the K-A fallback);
- a **version restore**;
- `POST /articles/{id}/collab/reset` (`resetKnowledgeArticleLog`): a writer deletes the log by
  hand, the way out of a log a client cannot apply. It answers `204`, works on a locked page and
  is idempotent; `403 article_read_only` for a reader, `404` for an article the caller cannot read.

Each notifies `doc_reset` with the new epoch on `knowledge.doc:<id>`; every subscriber discards
its copy and reopens the article.

A `single` save and a restore are refused with `409 collab_active` while the log is **active**:
not empty and with a row stored (an append, a seed or a compaction, kept as `last_append_at` on the
article row) within the last **2 minutes**. Someone is co-editing, and the write would delete their
log. A log with no row for 2 minutes is stale and these two reset it as before; the endpoint above
is unchanged and resets an active log too.

### The collaborative save

A writer's client saves the JSON with `PUT /articles/{id}/content` and `"mode": "collab"`. The
server takes the article's row lock, checks write access, the lock (`409 article_locked`),
that the article has a **live log** (`409 collab_inactive` while it is empty: nobody has opened the
page live, and the save would race a single-editor one) and the document (`400 invalid_document`,
`413 document_too_large`), compares **no version** (the
version conflict is not shown in collaborative editing), saves, coalesces the version as any
save does, and leaves the log. It cannot check that the JSON matches the log: whoever saves is
recorded as the author, so a version may be attributed to the person whose browser saved last.
Search, history, the Assistant and export therefore trail the live text.

### The browser's side

`apps/knowledge/frontend/src/editor/collab` holds the client of the protocol above. The shell
(`packages/frontend-shell/src/live`) carries the frames and knows nothing of Yjs
([Frontend architecture](/en/contributing/frontend-architecture/#binary-frames-and-documents)); the module's
`CollabProvider` is the Yjs side.

- **The provider** owns a `Y.Doc` and a y-protocols `Awareness`. After the subscription's `ok`,
  after every reconnect and after every `resync` or `doc_stale`, it sends `doc_sync` with its
  **contiguous watermark** (the newest row such that it has applied every row up to it; its own
  acknowledged rows count) and its epoch (0 for the first sync). It writes nothing until `synced`.
- **Updates** from `doc.on("update")` are collected for 100 ms, merged with `Y.mergeUpdates`
  (in groups of at most 256 KiB) and sent as one frame, and kept until their `ack`. A lost
  connection puts every unacknowledged update back to be sent after the next `synced`. `rate`,
  `log_full` and `unavailable` keep the update and retry with backoff and jitter (1 s doubling
  to 30 s); `log_full` also shows the *paused* message until an ack. `read_only` stops writing
  and `too_large` stops the live copy.
- **Seeding.** A writer that gets `synced` with `seq` 0 reads the article afresh, normalises its
  JSON through a throwaway editor (so every block has the id of the editor's `UniqueID`), builds
  the document with `prosemirrorJSONToYDoc` and sends one seed naming the version it read. On
  `seed_accepted` it applies the seed to its own document (a seed is not echoed to its sender).
  On `seed_rejected` it throws the seed away and syncs again; it never merges its own seed.
  A reader on an empty log shows the saved JSON and binds when the first update arrives.
- **Compaction.** On `compact_requested {upTo}` it sends the whole state with `upTo` set to its
  **watermark**, only when the watermark covers `upTo`, and never while one is in flight.
- **Awareness.** The local state (the person's name, a colour chosen from twelve by hashing the
  user id, the cursor) is sent whole, at most every 100 ms, by writers only; a removal is sent on
  leaving the page.
- **When it breaks.** An update that cannot be applied stops the provider (`doc_error` is sent) and
  the page shows the saved text, read-only, with **Rebuild from the last saved version** for
  writers, which calls `resetKnowledgeArticleLog`. `doc_reset` finishes the provider: the page
  discards its document, fetches the article again and starts a new provider; text that had not
  reached the log or the JSON is kept as the local draft.
- **The editor** is TipTap's `Collaboration` and `CollaborationCaret` over the provider's
  document, with StarterKit's undo history off (Yjs's undo is used: a person undoes only their
  own changes). The editor's schema is unchanged, so `schema.json` is too.
- **The page** is in collaborative mode once the provider is `ready` and the page has nothing
  unsaved; until then, and whenever the live channel is off, it is the single editor exactly as
  before. In collaborative mode the JSON is saved with `mode: "collab"` 5 s after the document
  last changed and at most every 30 s (each with 0 to 2 s of jitter), skipped when the saved
  JSON already equals the editor's; the conflict dialog, *… is editing* and the local draft are
  not used, and only the person's own typing (and undo) marks the page changed. A `409
  collab_inactive` makes the provider sync again. **A page that has a live log never takes the
  single-editor save path:** while the connection is down or the log is paused the save waits
  until the log's save is possible. A page that was typed in as a single editor when the log turns
  out to be live (`409 collab_active` from a single save or a restore, or the log binding while
  the page has unsaved text) keeps that text as the local draft, says so, and joins the log.
  **Restore** in collaborative mode replaces the editor's content in one transaction and saves it
  once the change is acknowledged, instead of calling the restore endpoint; in single mode a
  `collab_active` refusal is shown as *Others are editing this page right now*.
- **Safeguards.** A seed or compaction unanswered for 10 s is given up on and the provider syncs
  again. `read_only` from the server keeps the text and makes the editor read-only until access is
  read again or the connection returns. Leaving the page flushes what is waiting and removes the
  cursor; closing the tab with unacknowledged text asks first. A cursor's name is cut to 60
  characters and its colour is one of the twelve or derived from the user id. The awareness
  state is still whatever a writer's browser sends: the server does not stamp identities on it yet,
  so a writer could show another name. After a reset the rebuilt editor is read-only until the
  article has been fetched again.

## The retention worker

`knowledge.retention` runs **every 24 hours**. A cycle takes an advisory lease
(`pg_try_advisory_lock`) on a pooled connection of its own, so one replica runs it and it is
safe on a pool of one, then:

1. **purges the trash** — every trash group trashed more than 30 days ago, deleted for good with its versions, under the tree lock;
2. **prunes the versions** older than 365 days beyond each article's newest 20;
3. **deletes the unreferenced files** older than 7 days that neither an article's content nor any of its versions names, and **removes the objects** of every file row gone (up to 1000 a cycle; see [Files in articles](#files-in-articles));
4. **removes the favourites** of articles their person can no longer read;
5. and, after the connection is back in the pool and only on the replica that won the lease, runs the **erasure sweep**.

### Erasure

The sweep asks `contracts.UserDirectory` which of the users Knowledge holds anything of still
exist, 200 ids at a time. For each erased user, in one transaction under the tree lock, it deletes
the **private roots** (and their subtrees and versions) of which they were the **only writer**,
then their **member rows**, their **favourites** and their **mention notices**, and sets their **authorship** — and the uploader of their files — **to null** (shown as
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
| `PUT /articles/{id}/content` | The autosave, with `expectedVersion` (`mode: single`) or `mode: collab` without it. |
| `POST /articles/{id}/collab/reset` | Delete the article's live log (`resetKnowledgeArticleLog`). |
| `POST /articles/{id}/move` | Move. |
| `GET`/`PUT /articles/{id}/access` | Read and replace the access. |
| `GET /articles/{id}/text` | The plain text in pieces (also the Assistant's). |
| `POST /articles/{id}/trash`, `POST /articles/{id}/restore` | Trash and restore a group. |
| `GET /articles/{id}/versions`, `GET .../versions/{n}`, `POST .../versions/{n}/restore` | The history. |
| `GET /assignable-users?query=` | Active users (display names only, at most 20) for the access dialog's picker, from the user directory (an article's writer need not hold the identity permission that lists users). |
| `GET /favourites`, `PUT`/`DELETE /favourites/{id}`, `POST /favourites/{id}/move` | Favourites. |
| `GET /search` | Search (also the Assistant's). |
| `GET /trash`, `DELETE /trash` | The trash; *empty* needs `knowledge:manage`. |
| `POST /articles/{id}/files` | Upload a file into an article (`uploadKnowledgeFile`). |
| `GET /files/{fileId}` | Serve a file (`getKnowledgeFile`). |
| `GET /articles/{id}/backlinks` | The readable articles linking to it (`listKnowledgeBacklinks`). |
| `POST /links/resolve` | Titles, or `no_access` / `deleted`, of up to 200 article ids (`resolveKnowledgeLinks`). |
| `GET /articles/{id}/export?subtree=` | Markdown or a zip (`exportKnowledgeArticle`). |
| `POST /articles/{id}/files/copy` | Copy a file of another article into this one (`copyKnowledgeFile`). |
| `POST /import` | Import a `.md` or a `.zip` (`importKnowledgeMarkdown`). |

The seven operations above are all `x-vantigo-assistant: never`: the Assistant cannot use them. So is the reset of the live log.

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
| 409 | `version_conflict` | `expectedVersion` is not current (`mode: single`); carries `currentVersion`, `updatedAt`, `updatedBy`. |
| 409 | `article_locked` | A content save, title change or version restore on a locked article. |
| 409 | `collab_inactive` | A `mode: collab` save of an article whose live log is empty. |
| 409 | `collab_active` | A `mode: single` save or a version restore of an article whose live log is not empty and took a row within the last 2 minutes. |
| 409 | `visibility_change` | The change moves the article between private and the workspace and `confirmVisibility` was not given. |
| 409 | `not_trashed` | A restore of an article that is not in the trash. |
| 400 | `invalid_document` | Also: an image or attachment naming a file that is not the article's own. |
| 413 | `document_too_large` | The document is over 2 MiB. |
| 413 | `file_too_large` | An uploaded file over 25 MiB, or an import over 50 MiB. |
| 413 | `export_too_large` | An export of more than 2000 entries or an estimated 1 GiB. |
| 429 | `import_busy` | Two imports are already running in this process; `Retry-After: 10`. |
| 503 | `storage_unavailable` | The object store could not be reached (upload, serving a file, export, import). Nothing was changed. |

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
| Live log | an update 256 KiB; compaction demanded at 4 MiB or 2000 rows; refused at 8 MiB or 4000 rows; a seed or compacted row 2 MiB; awareness 4 KiB; 50 updates a second per connection, burst 200 |
| Trash | 30 days |
| Versions | 365 days, beyond the newest 20 |
| Uploaded file | 25 MiB; unreferenced files are deleted after 7 days |
| Link resolve | 200 ids |
| Mention notice | once an hour per person per article |
| Import | 50 MiB upload, 500 entries, 200 MiB uncompressed, 10 folders deep, 4 MiB per `.md`, 25 MiB per image |

## The editor and its licences

The editor in `apps/knowledge/frontend` is built on TipTap and ProseMirror. **Only permissively
licensed packages (MIT, BSD, Apache-2.0) are used, and never `@tiptap-pro`.** Pinned at 3.32.2
unless noted:

| Package | Licence |
| --- | --- |
| `@tiptap/core`, `@tiptap/react`, `@tiptap/pm`, `@tiptap/starter-kit`, `@tiptap/extensions`, `@tiptap/extension-link`, `@tiptap/extension-task-list`, `@tiptap/extension-task-item`, `@mantine/tiptap` | MIT |
| `@tiptap/extension-code-block-lowlight`, `-details`, `-drag-handle`, `-drag-handle-react`, `-mention`, `-node-range`, `-table`, `-unique-id`, `@tiptap/suggestion` | MIT |
| `lowlight` 3.3.0 | MIT |
| `highlight.js` 11.11.2 (through lowlight) | BSD-3-Clause |
| `yjs` 13.6.33, `y-protocols` 1.0.7, `lib0` 0.2.119 (through them), `@tiptap/extension-collaboration`, `@tiptap/extension-collaboration-caret`, `@tiptap/y-tiptap` 3.2.0 | MIT |

The server parses Markdown on import with [goldmark](https://github.com/yuin/goldmark)
(`github.com/yuin/goldmark`, **MIT**).
