# Working in the Vantigo repository

Instructions for anyone — a person or an AI agent — changing this repository. They
complement [CONTRIBUTING.md](CONTRIBUTING.md), which explains how to get the stack
running, and the conventions in
[module boundaries](docs/src/content/docs/en/contributing/module-boundaries.md).

## The documentation changes with the code

Vantigo's documentation lives in this repository, under
[`docs/`](docs/README.md), and is published at <https://docs.vantigo.io>. It is
kept true by one rule:

**A change that alters what Vantigo does lands in the same commit series as the
change to the pages that describe it.** "What Vantigo does" means a behaviour, a rule,
a setting, an endpoint, a permission, a background worker or a screen — anything a
user, an administrator or an integrator could notice. A change that is finished in the
code but not in the documentation is not finished.

How it is enforced:

- Every page declares the repository paths it documents in its `sources` frontmatter.
  `bun run docs:check` (and CI, on every pull request) fails when a file under one of
  those paths changes and none of its pages do. `bun run tools/docs/check-coverage.ts --list`
  prints the map.
- The user guide (`user/`) and the administration guide (`admin/`) exist in **English
  and Norwegian bokmål**, page for page, with the same file names under `en/` and
  `nb/`. Editing one language without the other fails the check. Write the English page
  first and translate it; do not leave a stub.
- The reference (`reference/`) and contributing (`contributing/`) sections are English
  only and fall back for Norwegian readers.
- The API reference under `reference/api/` is generated from `openapi/*.yaml` at
  build time. Never write it by hand; change the contract.
- When a change truly alters nothing documented — a refactor, a test, a dependency
  bump — say so in the commit message, with a reason, and the check accepts it:

  ```
  Docs-Impact: none — pure refactor, no behaviour change
  ```

### Which page to update

| You changed | Update |
| --- | --- |
| A screen, a flow, a label a user sees | `user/<module>.md` in `en/` and `nb/` |
| A setting, a deployment concern, an operator procedure | the page under `admin/` in `en/` and `nb/` |
| A module's rule, model, permission, worker or endpoint | `reference/<module>.md`, and the user guide if the user can notice it |
| An OpenAPI contract | nothing by hand: the API reference regenerates |
| The identity module | `admin/authentication.md` or `admin/sso-scim.md` |
| A new module | a new page in every section it touches, listed in that section's overview |
| The documentation site itself, its build or the server's `/docs` handler | `contributing/documentation.md`, and `admin/installation.md` if what an operator sees changes |

### Writing

- User guide pages are tasks: what the person wants to do, where to click, what
  happens, and the permission the screen needs when it is not obvious. Name screens
  as the interface names them, in that language.
- Administration pages are procedures: preconditions, the steps in order, how to
  verify, what can go wrong.
- Reference pages are the truth, written from the code in the module's own terms,
  with the rule, the status code and the permission a request is checked against.
- Prefer words to screenshots. Link with site paths (`/en/reference/invoices/#payments`),
  never relative `.md` paths; link to repository files with their GitHub URL.
- `mise run docs:dev` serves the site locally; `mise run docs:check` runs what CI runs.

## Specs, plans and research

Design specs, implementation plans and research live under `docs/superpowers/`
(`specs/`, `plans/`, `research/`), dated, and are not part of the published site. A
plan for a feature lists, as its own step, the documentation pages it will change, in
both languages where the section is bilingual.

## Adding a module

Follow the checklist
[Adding a module](docs/src/content/docs/en/contributing/module-boundaries.md#adding-a-module)
step by step. A new module has to be named in many registries, and these are the ones
that have been missed:

- the integration test's `everyModule()` in
  `apps/server/internal/integration/assistant_test.go`;
- the Assistant's link prefixes: `APP_ROUTE_PREFIXES` in
  `apps/assistant/frontend/src/lib/links.ts` and `appRoutes` in
  `apps/server/internal/assistant/prompt.go`, bumping `promptVersion` there;
- every depguard set in `apps/server/.golangci.yml`, the hyphenated ones too;
- `modulesEnv` in `apps/server/internal/modtest/modtest.go`, for a module that
  `internal/config` makes require another;
- the host's per-module registrations: `moduleKeys`, `apps.ts`, the layout route,
  `i18n.ts`, the admin permission labels, the dashboard and the spotlight, and the
  API client in `sessionClients` (`apps/host/frontend/src/api/session-handlers.ts`).
- **the public site**: the module's page on <https://vantigo.io>, which lives in another
  repository — see [The public site](#the-public-site) below.

A pull request's CI runs against the base it branched from. When another pull request
has touched a module registry since, rebase onto the latest `main` and let CI run again
right before merging — otherwise the new module and the new registry each pass alone
and break `main` together.

## The public site

Vantigo's marketing site is its own repository,
[vantigo-io/vantigo-landing](https://github.com/vantigo-io/vantigo-landing) (private;
usually checked out beside this one, as `../vantigo-landing`). It has one page per
app — `vantigo.io/apps/customers` — that says what the app is about, what it solves and
how, with animated product scenes, and it lists every app on its front page. Nothing in
this repository can check that site, so it is kept true by this rule:

**A new app is not finished until it has its page on the public site, and a change a
customer could be sold on — a new capability, a renamed or removed one, an app that
goes from coming to live — reaches the app's page there.** The site may only say what
the user guide here documents, so write the guide pages first, in both languages, and
the landing page from them.

- For a new app, follow *Adding an app* in that repository's `AGENTS.md`: name it in the
  two catalogs, write `web/src/apps/<key>.ts` in English and Norwegian from
  `docs/src/content/docs/{en,nb}/user/<module>.md`, register it, and run its checks.
  Open that pull request alongside this repository's, and link the two.
- `mise run apps:check` in the landing repository compares the apps it shows with the
  user guide pages here and names what is missing.
- A plan for a new app or a customer-visible feature lists the landing page as its own
  step, next to the documentation pages.
- If the landing repository is not available to you, say so when you hand the work
  over, and name the app and what its page must say — do not let it pass silently.

## Finishing a change

Before calling a change done: the Go gate (`mise run server:check`, `mise run server:test`),
the frontend gate (`mise run frontend:check`) and the documentation gate
(`mise run docs:check`) pass, and the commit message follows Conventional Commits as
the history shows. A new app, or a change a customer could be sold on, also has its
pull request in the landing repository ([The public site](#the-public-site)).
