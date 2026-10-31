# Brief A — Integrations under Workspace admin, the Entra page in four tabs

You are implementing delivery **A** of the design
`docs/superpowers/specs/2026-10-31-integrations-and-configuration-tiers-design.md`
(decisions **T1, T2, T3, T15**). Read that file first, in full, then this brief. You
work alone, from a cold start, in this repository. Model: Claude Sonnet 5.5.

## The outcome

An Owner opens the avatar menu, chooses **Workspace admin**, and the sidebar has a
second section, **Integrations** (nb **Integrasjoner**), with one item, **Microsoft
Entra ID**, at `/workspace/entra`. That page is today's Entra page regrouped into four
tabs — **Status**, **Connection**, **Accounts**, **Outlook and mail** — each a route.
The Entra tile is gone from the app switcher; `/entra` redirects to the new page. A new
administration page, *Configuration*, explains the two configuration tiers. Nothing in
Go changes. Nothing the Entra module does changes.

## Read before you plan

- `AGENTS.md`, `CONTRIBUTING.md`, `docs/src/content/docs/en/contributing/module-boundaries.md`
  and `docs/src/content/docs/en/contributing/frontend-architecture.md`.
- The spec's *Readings* and T1–T3, T15; the Entra design D15 and the Outlook design
  M8 for how the page and the *Connections* page came to be
  (`docs/superpowers/specs/2026-10-19-entra-design.md`,
  `docs/superpowers/specs/2026-10-29-outlook-connection-and-mail-design.md`).
- `apps/host/frontend/src/apps.ts` (the `apps` list, `moduleApp("entra", …)`, the
  `areas` list with its `workspace` and `settings` entries, `AppKey`, `areaForKey`,
  `isAppEnabled`, `visibleNavSections`), `apps/host/frontend/src/navigation.ts`
  (`NavItem`, `NavSection`, `hasPermissions`), `apps/host/frontend/src/routes/workspace.tsx`
  (the area guard), `apps/host/frontend/src/routes/entra.tsx` and `routes/entra/index.tsx`,
  `apps/host/frontend/src/routes/settings/connections.tsx` (the pattern for a
  module-gated area page), `apps/host/frontend/src/routes/-app-layout.tsx`,
  `apps/host/frontend/src/catalogs/navigation.ts` (labels, en and nb), the tests
  `apps.test.ts`, `route-guards.test.ts`, `catalogs/parity.test.ts`.
- `apps/entra/frontend/src/pages/entra.tsx`, `lib/steps.ts`, `components/*`, `i18n.ts`,
  `index.ts`, and `pages/entra.test.tsx`.
- Where the old route is linked: grep `"/entra"` and `appUrl("/entra` across `apps/*/frontend`
  and `packages/`, and `/entra/` across `docs/src/content/docs`.
- `docs/src/content/docs/en/user/entra.md`, `en/admin/entra.md`, `en/reference/entra.md`,
  `en/user/workspace-administration.md`, `en/admin/index.md`, `en/admin/installation.md`
  (*Configuration*, *Email and observability*), and their `nb/` twins where they exist.
- `tools/docs/check-coverage.ts --list` for which pages claim which paths.

## Build

1. **The section and the item** (`apps.ts`): a `NavSection` labelled
   `navigation.integrations` after the workspace section, with
   `{ label: "navigation.entra", to: "/workspace/entra", icon: IconBrandWindows,
   module: "entra", requiredPermissions: ["entra:manage"] }`. Add the en and nb labels.
   Remove `moduleApp("entra", …)` from `apps`; make whatever derives from `AppKey`
   tolerate a module with no app (`appForKey`, the spotlight, dashboard tiles, tests).
2. **The area guard** (`routes/workspace.tsx`): admit, beside Owners and authorization
   managers, a session holding `entra:manage` while the Entra module is enabled. The
   child route `/workspace/entra` keeps its own gate: module enabled (else the
   module-disabled not-found, as `appLayoutOptions` does) and `entra:manage`.
3. **Four tab components** in `@vantigo/entra-ui`, exported from `index.ts`:
   `EntraStatusTab`, `EntraConnectionTab` (steps 1–3), `EntraAccountsTab` (steps 4–6
   and Groups; takes the same `signIn` and `provisioning` render props `EntraPage` takes
   today), `EntraOutlookTab` (Outlook connection, calendar sync, mail channels). Move
   the cards; do not rewrite them. The Status tab adds the "*n* of 6 steps done —
   continue in …" callout from `stepsDone` and one line per switch with a link to its
   tab. Keep `EntraPage` exported for one release as a thin composition of the four, so
   nothing else breaks; mark it deprecated in a comment.
4. **Routes** in the host: `routes/workspace/entra.tsx` (layout: header, `Tabs` bound to
   the child route, `Outlet`), `routes/workspace/entra/index.tsx` (Status; redirects to
   `connection` while `stepsDone(connection).register` is false), `connection.tsx`,
   `accounts.tsx` (hands in identity's `SsoSettingsCard` as `routes/entra/index.tsx`
   does today), `outlook.tsx`. `routes/entra.tsx` and `routes/entra/index.tsx` become
   permanent redirects to `/workspace/entra` (keep the Outlook callback's `return_to`
   working if it ever named `/entra`: check `apps/entra/frontend/src/lib/return-to.ts`).
5. **Links** elsewhere that point at `/entra` (Staff settings' *Open Entra* text,
   Communications UI, the dashboard) now point at `/workspace/entra` or the tab they
   mean.
6. **Documentation**, in the same commit series, en and nb:
   - new `admin/configuration.md`: the two tiers (T1) in a table, the rule, the list of
     today's instance settings grouped (process, mail, storage, workers, shared services)
     with a one-line meaning each, and for every workspace setting the page that edits
     it. `sources` names `apps/server/internal/config`. Link it from `admin/index.md`.
   - `user/entra.md`: the page's new place, the four tabs, what each holds; every "the
     Entra page" becomes the tab it means.
   - `admin/entra.md`: the same rewording; the redirect.
   - `reference/entra.md`: the routes (`/workspace/entra/...`), the redirect, the exports.
   - `user/workspace-administration.md`: a new section *Integrations* — what it is, who
     sees it, that an Entra manager who is not an Owner sees only this section.
   - `contributing/module-boundaries.md` (en only): the host's *Integrations* section as
     a slot a module may take an item in, beside the *Connections* page slot.
   - `ROADMAP.md`: a short *Integrations* entry under *Platform* naming the spec and
     this delivery.
7. **Tests**: `apps.test.ts` and `route-guards.test.ts` gain the visibility matrix
   (Owner; Entra manager who is not Owner; authorization manager; Entra module off);
   the redirects; `catalogs/parity.test.ts` stays green (every en key has its nb).
   Split `pages/entra.test.tsx` by tab, each asserting the cards it took and the Status
   callout's text.

## Do not

Change any Go file, any OpenAPI contract, any migration, or the `entra:manage`
permission · move Settings › Connections · move Communications' Channels page · build
the AI page (delivery D) · touch the SSO card's placement (open question Q2) · rename
i18n keys that other packages import · add a dependency.

## How to work

- Create a worktree on a branch `feat/workspace-integrations` cut from `main`. Run
  `bun install --frozen-lockfile` before anything (a fresh worktree has no
  `node_modules`; without it the pre-commit hook runs the wrong Biome).
- **Plan first.** Write `docs/superpowers/plans/2026-10-31-workspace-integrations.md` in
  the house shape (see `docs/superpowers/plans/2026-10-29-entra-outlook-connection.md`:
  the `> For agentic workers` line, **Goal**, **Architecture**, **Spec**, a *Plan
  readings* section with what you verified in the code and the line numbers, then
  *Tasks* as checkboxes, each small enough to be one commit). Commit the plan alone,
  with no `Docs-Impact` trailer: plan files are under no page's `sources`, so the coverage check accepts the commit, and a `none` trailer anywhere in the range would waive the check for the whole pull request.
- Implement task by task, ticking the plan as you go. One commit per task,
  Conventional Commits as the history shows (`feat(host,entra-ui): …`,
  `docs(entra,admin): …`). Commits are SSH-signed through 1Password; if signing fails,
  stop and report — never pass `--no-gpg-sign` or `--no-verify`.
- Gates before you call it done: `mise run frontend:check`, `mise run docs:check`
  (and `mise run server:check` to prove you changed no Go). Frontend tests under
  Node 26 are known to fail only in `apps/projects/frontend`'s localStorage tests and
  to stop the loop early; run the host's and Entra's packages directly if that happens
  and say so.
- Do not push and do not open a pull request. Do not merge `main` into your branch.

## Report

When done, answer with: the branch and its commits (`git log --oneline main..`); the
gates' results verbatim; the plan's path; anything the design said that you could not
do as written and what you did instead; and the manual check an Owner should make in
a running stack (which menu, which page, what they should see).
