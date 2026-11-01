# Brief C — "Vantigo hosted mail" shown greyed in the channel form

You are implementing delivery **C** of the design
`docs/superpowers/specs/2026-10-31-integrations-and-configuration-tiers-design.md` as
it stands after the owner's answer 7 (2026-11-01): the hosted mail service is
**deferred**, and what ships now is only its greyed entry. Read the spec's T7 and the
*Deliveries* section first, then this brief. Delivery **B** must already be on `main`
(the channel form shows an SMTP channel without credentials as *Unavailable*; grep
`OutboundMail` in `apps/server/internal/contracts`). You work alone, from a cold start,
in this repository. Model: Claude Sonnet 5.5.

## The outcome

In Communications › Channels, the channel form's provider choice lists a third entry,
**Vantigo hosted mail** (nb **Vantigo-driftet e-post**), below *Microsoft 365* and
*SMTP*, disabled, with the hint **Not yet available** (nb **Ikke tilgjengelig ennå**).
It cannot be chosen, nothing about it reaches the API, no provider key exists on the
server, and `GET /channel-providers` is unchanged. The user guide names the entry and
says it is not yet offered.

## Read before you plan

- `AGENTS.md`, `docs/src/content/docs/en/contributing/frontend-architecture.md`.
- The spec's T7 table, the T8 banner, the *Deliveries* section (C and C′), the owner's
  answer 7.
- `apps/communications/frontend/src/components/ChannelForm.tsx`,
  `apps/communications/frontend/src/pages/admin.channels.tsx` (how providers from
  `GET /channel-providers` are rendered and how an unavailable Microsoft 365 is shown
  with its reason — reuse that disabled presentation), the module's i18n catalog, and
  the tests beside them.
- `docs/src/content/docs/en/user/communications.md` *Set up a channel* and its `nb/`
  twin; `en/reference/communications.md` *Providers* (read it; you should not need to
  change it, since the API does not change).

## Build

1. In the channel form, after the providers the API answers, a fixed entry
   `hosted` rendered with the same disabled styling the unavailable Microsoft 365
   provider uses, label and hint from the catalog in en and nb. It is never selectable,
   never submitted, and does not appear in the provider badge of existing channels
   (none can have it).
2. Tests: the entry renders, is disabled, and clicking it changes nothing; the
   existing provider tests stay green.
3. Docs, en and nb: `user/communications.md` *Set up a channel* gains one sentence —
   the form also lists *Vantigo hosted mail*, greyed, which is not yet offered.
4. Nothing else: no Go, no OpenAPI, no migration, no server-side provider key, no
   environment setting.

## How to work

- Worktree on `feat/hosted-mail-placeholder` cut from `main` after B landed;
  `bun install --frozen-lockfile` first.
- This delivery is small enough to plan in the commit message; no plan file is needed.
  Two commits at most: the frontend change with its tests, and the docs. Conventional
  Commits, SSH-signed through 1Password; if signing fails, stop and report — never
  `--no-gpg-sign` or `--no-verify`.
- Gates: `mise run frontend:check` and `mise run docs:check`. The Node 26 quirk and the
  midnight clock edge in `apps/customers` are known; run the Communications package
  directly if the loop stops before it, and say so.
- No push, no pull request.

## Report

Branch, `git log --oneline main..`, the gates' output verbatim, and the manual check:
open Communications › Channels › Add channel and see the greyed third entry with its
hint, in both languages.
