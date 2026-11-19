# Dark theme — implementation plan

Date: 2026-11-18. Research: `docs/superpowers/research/2026-11-18-dark-theme-and-brands.md`
(§2, and the owner's decisions in §6).

Three deliveries:

- **D-A**, the foundation, in this repository.
- **D-L**, the landing site, in vantigo-landing. It runs in parallel with D-A.
- **D-B**, the sweep of every app, in this repository. It starts once D-A has merged.

The brands feature that follows builds on D-A's token layer. Do not add anything
brand-specific here.

## The shared palette

The app and the landing site use the same values, so the scenes on vantigo.io look like
the product.

**Light: unchanged.** Page `#f7f8fc`, surface `#ffffff`, text `#182033`, and the indigo
scale in `packages/frontend-shell/src/theme.ts`.

**Dark: a blue-tinted grey.** These values replace Mantine's neutral `dark` scale:

| `dark[i]` | 0 | 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 | 9 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| hex | `#dde1ef` | `#b4bad0` | `#8c93ad` | `#666d8a` | `#3d4361` | `#2f3450` | `#262a41` | `#1c1f32` | `#15182a` | `#0f1120` |

What each role uses:

| Role | Light | Dark |
| --- | --- | --- |
| Page background (`--vantigo-body-bg`) | `#f7f8fc` | `dark[8]` `#15182a` |
| Surface: cards and `--mantine-color-body` | `#ffffff` | `dark[7]` `#1c1f32` |
| Text (`--vantigo-text`, `--mantine-color-text`) | `#182033` | `dark[0]` `#dde1ef` |
| Dimmed text | Mantine default | `dark[2]` |
| Border | Mantine default | `dark[4]` |
| Link (`--vantigo-link`) | `vantigo-8` `#3730a3` | `vantigo-3` `#a5b4fc` |
| Primary filled (buttons) | `vantigo-6` | `vantigo-6`, so `primaryShade: { light: 6, dark: 6 }` |
| Subtle tint backgrounds (today `vantigo-0`, `gray-0`) | as today | `--mantine-color-vantigo-light` / `--mantine-color-default-hover` |

These are starting values. A browser pass may tune them, but change them in one place
only: the frontend-shell tokens. The landing site then copies the result.

## D-A: the foundation

Branch `feat/dark-theme-foundation`, opened against `main`. It includes the research
note and this plan.

1. **Tokens.**
   - In `packages/frontend-shell/src/theme.css`, define `--vantigo-body-bg`,
     `--vantigo-surface`, `--vantigo-text`, `--vantigo-text-dimmed`,
     `--vantigo-border`, `--vantigo-link` and `--vantigo-subtle-bg` for `:root` and
     `:root[data-mantine-color-scheme="dark"]`.
   - `body` uses them.
   - `theme.ts` adds the `dark` scale above, `primaryShade` and `colorSchemeManager`
     (a `localStorageColorSchemeManager` with key `vantigo-color-scheme`).
   - Export the token names from `frontend-shell`, so later code and the brands
     feature use them.
2. **Provider.** In `apps/host/frontend/src/main.tsx`, pass
   `defaultColorScheme="auto"` and the manager. The error boundary
   (`components/errors/app-error-boundary.tsx`) renders outside the provider, so its
   inline hex moves to classes that follow `prefers-color-scheme` and the attribute.
3. **No flash on load.**
   - Append a pre-paint snippet to `runtimeConfigScript()` in
     `apps/server/internal/web/index.go`. It reads `localStorage["vantigo-color-scheme"]`
     and falls back to `matchMedia("(prefers-color-scheme: dark)")`. It sets
     `data-mantine-color-scheme` on `<html>` and is wrapped in try/catch.
   - The existing test that pins the hash to exactly the injected script must keep
     passing. Add a test for the snippet.
   - Add `<meta name="color-scheme" content="light dark">` to
     `apps/host/frontend/index.html`.
4. **The personal preference.**
   - Migration: `identity.users.color_scheme text NULL CHECK (color_scheme IN ('light','dark'))`.
     NULL means automatic. Take the next free number after main's latest; check right
     before merging.
   - Plumb it like `preferred_language`: `queries/account.sql`, `account.go`
     (validate and normalise), `openapi/specs/identity.yaml` (`AccountProfileRequest`
     and `AccountResponse`), `go generate`, `apps/host/frontend/src/api/account.ts`
     (`"automatic" | "light" | "dark"`).
   - On the profile page (`routes/settings/profile.tsx`), an **Appearance** field next
     to **Preferred language**: Automatic, Light or Dark.
   - Add a sync component like `account-language-preference.tsx`. Signed in, it applies
     the server value through `useMantineColorScheme().setColorScheme`. It leaves
     public pages alone.
   - In the account menu, a quick switch (light/dark) that saves to the server and
     applies at once.
   - Strings in `en` and `nb`.
5. **Logo.**
   - Make a dark-background variant of `packages/frontend-shell/src/assets/logo.png`,
     with the navy stroke lightened to read on `dark[7]` and the mint kept. Generate it
     with a small committed script, so it can be regenerated.
   - The shell shows it in dark when no custom `logoUrl` is set.
   - Add a dark favicon through `<link rel="icon" media="(prefers-color-scheme: dark)">`.
   - Use the same file for the docs logo (`docs/astro.config.mjs` takes
     `logo: { light, dark }`).
6. **Tests.**
   - Unit tests for the manager and the sync component (override the `matchMedia`
     mock), the account API mapping, the profile field and the menu switch.
   - Go tests for the account field (validation, round trip) and the snippet.
   - Seed data: none needed. A preference is personal, so demo users stay automatic.
7. **Documentation.** This change alters what a user can notice.
   - `user/getting-started.md`, in `en` and `nb`, under "Edit your profile": the
     Appearance setting and the account-menu switch.
   - `contributing/frontend-architecture.md`: the token layer, and "never hard-code a
     colour; use the tokens or Mantine variables".
   - Then run `go generate ./...` in `apps/server`, because the assistant embeds the
     docs.
8. **Gates.** `mise run server:check`, `mise run server:test`, `mise run frontend:check`
   and `mise run docs:check`.

## D-L: the landing site (vantigo-landing)

Branch `feat/dark-theme` off the landing repository's latest `origin/main`.

1. Add a semantic token layer to `web/src/styles/tokens.css`, with light and dark
   values from the palette above. The selector is `:root[data-theme="dark"]`, plus
   `@media (prefers-color-scheme: dark) { :root:not([data-theme="light"]) … }`, so the
   site follows the OS with no script at all.
2. Move components and `scenes.css` from the raw scales to the semantic tokens where
   they are used as backgrounds, borders or text.
   - The scenes imitate the product, so their dark tones follow the app's dark palette.
   - The navy Security band and the AI feature card get a dark treatment that still
     stands out.
3. **Toggle** (owner decision: follow the computer first, plus a toggle).
   - A button in the header cycles automatic → light → dark. It is stored in
     `localStorage["vantigo-color-scheme"]`, the same key as the app.
   - A pre-paint inline script sets `data-theme`. It goes through the CSP hasher in
     `server/internal/web/csp.go`.
   - The toggle code lives in `web/src/scripts/site.ts`.
   - No `style=""` attributes (`check-budget.ts`).
4. Add `<meta name="color-scheme" content="light dark">`, and `theme-color` with
   `media` variants for both schemes.
5. Add the dark logo variant, matching the app's, in the header, footer, integrations
   hub and OneSystem. Add a dark favicon through `media`.
6. Checks:
   - the repository's gates (`web:check`, `server:test`, `server:check`, `smoke`);
   - the CSS budget;
   - a Lighthouse run emulating dark, so dark contrast is measured.

The landing site does not claim dark mode as a product feature until the user guide
documents it (D-A does).

## D-B: the sweep (after D-A merges)

- Replace the ~85 sites listed in the research note's §2.3 with tokens or Mantine
  variables. Concentrations: host public pages and error illustrations, the projects
  Gantt, communications, and shade-8/9 text.
- Replace the five copies of the drop zone with one shared component in
  `frontend-shell/ui`.
- Keep the e-mail HTML preview on white paper.
- Give Scalar (`apps/apiref`) `darkMode` from the same local key, and fix its two hex
  values.
- Add a colour lint to `frontend:check`: no `#hex`, `gray.0` or `bg="white"` in app
  `.tsx` outside allowlisted files.
- Do a browser walk of every app in both schemes.
- It can be split per group of apps across parallel agents, each in its own
  worktree.
