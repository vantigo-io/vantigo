# Vantigo documentation

This directory is the documentation site, built with [Astro Starlight](https://starlight.astro.build/)
and published at <https://docs.vantigo.io>. The content lives in
[`src/content/docs/`](src/content/docs/), one tree per language:

```
src/content/docs/
├── en/                 English (the default; untranslated pages fall back to it)
│   ├── user/           The user guide: what someone using Vantigo can do, and how
│   ├── admin/          The administration guide: install, configure, run, upgrade
│   ├── reference/      Per-module domain model, rules, permissions and endpoints
│   └── contributing/   The rules a change is held to
└── nb/                 Norsk bokmål, mirroring en/ page for page
```

`superpowers/` beside this README holds the design specs, plans and research the
modules were built from. It is working material for contributors and agents, not part
of the site.

The site is built twice: once for docs.vantigo.io, and once, without the generated API
reference, into the server binary, which serves it under `/docs` so every installation
carries the documentation for the version it runs
(`scripts/spa-embed-overlay.sh`, `apps/server/internal/docs`).

## Running it

```bash
mise run docs:dev      # http://localhost:4321 with live reload
mise run docs:check    # the production build, broken links fail it, plus the coverage check
```

## The rule

A change that alters what Vantigo does — a behaviour, a setting, an endpoint, a
permission, a screen — lands in the same pull request as the change to the pages that
describe it, in **both** languages for the user and administration guides. Every page
declares the repository paths it documents in its `sources` frontmatter, and
`tools/docs/check-coverage.ts` fails CI when a path changes without its pages. The
details are in the [documentation guide](src/content/docs/en/contributing/documentation.md)
and in the repository's [`AGENTS.md`](../AGENTS.md).
