---
title: Documentation
description: How this site is organised, how to run it, and the rule that keeps it true.
sidebar:
  order: 2
sources:
  - docs
  - tools/docs
  - apps/server/internal/docs
  - scripts/spa-embed-overlay.sh
  - scripts/restore-embed-overlay.sh
---

This site is the documentation for Vantigo, built with
[Astro Starlight](https://starlight.astro.build/) from the Markdown under
`docs/src/content/docs/` in the repository. It is published at
<https://docs.vantigo.io> on every push to `main`.

## Organisation

| Section | Audience | Languages |
| --- | --- | --- |
| `user/` | Someone using Vantigo: what they can do, and how. | English and Norwegian |
| `admin/` | Someone running Vantigo: install, configure, operate, upgrade. | English and Norwegian |
| `reference/` | Integrators and contributors: the domain model, rules, permissions and endpoints per module. | English |
| `reference/api/` | Generated from `openapi/*.yaml`; never edited by hand. | English |
| `contributing/` | Contributors: the rules a change is held to. | English |

English is the default language. `nb/` mirrors `en/` page for page with the same file
names, which is what lets Starlight pair translations and offer the language switch. A
page missing in `nb/` shows its English version with a notice, which is the intended
state for the reference and contributing sections.

## Running it

```bash
mise run docs:dev      # http://localhost:4321 with live reload
mise run docs:check    # the production build, which fails on a broken link, then the coverage check
```

## Two builds of the same site

The site is built twice from the same content:

- **The public site**, docs.vantigo.io, built by CI from `main` with the base path
  `/`, the generated API reference and the link validator. Its banner says
  `main@<commit>`.
- **The embedded copy** inside every Vantigo image, built by
  `scripts/spa-embed-overlay.sh` with `DOCS_BASE=/docs`, `DOCS_EMBED=1` (no API
  reference, which is 74 MB of generated pages) and `DOCS_VERSION=<release>`, and
  compiled into the binary from `apps/server/internal/docs/dist` the same way the
  SPA is. The server serves it at `/docs/` with the site's own content security
  policy and, under a base path, rewrites the baked `/docs/` URLs on the way out.

Links in pages are written as site paths without a base (`/en/user/`); a rehype plugin
in the Astro config prefixes them with the base at build time, so a page never needs
to know which build it is in.

## The rule: documentation changes with the code

A change that alters what Vantigo does lands in the same pull request as the change to
the pages that describe it. "What Vantigo does" means a behaviour, a rule, a setting,
an endpoint, a permission, a background worker, or a screen — anything a user, an
administrator or an integrator could notice.

Every page declares the repository paths it documents in its `sources` frontmatter:

```yaml
---
title: Invoices module
sources:
  - apps/server/internal/invoices
  - apps/invoices/frontend
  - openapi/invoices.yaml
---
```

`tools/docs/check-coverage.ts` runs in CI against the pull request's diff. For each
changed file it finds the pages whose `sources` cover it, and fails when none of them
changed too. A page under `user/` or `admin/` that changes in one language must change
in the other as well; the check fails on a one-sided edit.

When a change genuinely alters nothing documented — a refactor, a test, a dependency
bump — say so with a trailer in the commit message, and the check accepts it:

```
Docs-Impact: none — pure refactor, no behaviour change
```

The trailer must carry a reason. It is read by reviewers, so a reason that would not
survive a review is not one to write.

## Writing a page

- **User guide pages are tasks.** Start from what the person wants to do, tell them
  where to click and what happens, and state the permission the screen needs when it
  is not obvious. Name screens as the interface names them.
- **Administration pages are procedures.** Preconditions, the steps in order, how to
  verify the result, and what can go wrong.
- **Reference pages are the truth.** Written from the code, in the module's own terms,
  with the rule, the status code and the permission a request is checked against.
- **Prefer words to screenshots.** A screenshot is out of date the moment the screen
  changes; a sentence can be fixed in the same commit.
- **Link with site paths**, `/en/reference/invoices/#payments`, never with
  relative `.md` paths. Links to files in the repository use the GitHub URL.
- **Frontmatter**: `title` is required; `description` feeds search and previews;
  `sidebar.order` sorts a page within its section; `sources` is the coverage map.
