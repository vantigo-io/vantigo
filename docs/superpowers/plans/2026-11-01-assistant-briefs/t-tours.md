# Brief T: "Show me how" with Mantine Tour

You are implementing delivery **T** of the design
`docs/superpowers/specs/2026-11-01-assistant-design.md`. It carries decision **S11**.

Read the design in full first, then this brief. Delivery **A** must be on `main`. Model:
Claude Sonnet 5.5.

## The outcome

A person asks "how do I add a metering point?". The answer gives the steps and the
documentation link, as today, plus a **Show me** button. Clicking it starts a Mantine
`Tour`:

- the tour navigates to the right screen, if it isn't the one on show;
- it points at *New metering point*, then at the fields that matter, then at *Save*;
- each step has a short title and text in the person's language;
- Next, Back and Close work by keyboard as Mantine provides;
- a step whose control isn't on screen (hidden by permissions, or still loading after
  3 s) shows centred, with its text.

**The model never writes a CSS selector.** It chooses from a registry of targets that
each module frontend declares.

## Read before you plan

- `AGENTS.md`, `CONTRIBUTING.md`, the module-boundaries frontend rules (the host
  composes; no module package imports another), and S10 and S11 in the spec.
- Mantine's Tour documentation: <https://mantine.dev/core/tour/>. `@mantine/core` is
  9.7.4 in the lockfile. Note:
  - selector targets that mount after the step activates;
  - the controlled `active`, `step` and `onStepChange` props;
  - `labels` for i18n;
  - the portal-dropdown `zIndex` caveat.
- **Delivery A's assistant code:**
  - `apps/server/internal/assistant/tools.go` (how a tool is declared and its result
    becomes a message part), `prompt.go` (the route table, the rules);
  - `apps/assistant/frontend/src/components/message-list.tsx` (how parts render,
    including `link`), `assistant-panel.tsx`, `hooks/use-assistant-shell.tsx`;
  - how the host mounts the panel (`apps/host/frontend/src/routes/-app-layout.tsx`)
    and how slots pass module code into the host.
- **The screens you mark.** Pick the eight most-used create and edit flows from the
  user guide (`docs/src/content/docs/en/user/*.md`). For example:
  - creating a customer;
  - adding a contact;
  - creating a metering point and its supply period;
  - creating an invoice;
  - registering time;
  - submitting an expense claim;
  - creating a project.

## Build

1. **The target registry:**
   - **Each module frontend that has targets** exports `assistTargets: AssistTarget[]`
     from its package index. `AssistTarget` is `{key, route, label: {en, nb},
     description: {en, nb}}`.
     - `key` is `<module>.<screen>.<control>`, for example
       `energy.metering-points.new`.
     - `route` is the route pattern the control lives on, for example
       `/energy/metering-points`.
   - **The control carries `data-assist="<key>"`.**
   - **The host collects every package's targets** and passes them to the assistant
     package through a slot. No module package imports another.
   - **A test per package** fails when a registered key has no `data-assist="<key>"`
     in that package's source, or when a `data-assist` key in the source is not
     registered.
2. **The server learns the registry:**
   - **Generate a JSON file of the targets** from the frontend packages at build time,
     embedded in the assistant module the way A embeds the docs. The model sees it in
     `show_tour`'s description, as the tool's enum.
   - **A test checks the embedded copy is fresh** (the docs copy's freshness check is
     the model).
   - **Keep it small:** key, route and the English description.
3. **The tool:** `show_tour(steps: [{target, title, body}])`.
   - At most 8 steps.
   - `target` must be a registered key. An unknown key refuses the call, so the model
     can try again.
   - `title` and `body` are in the person's language. The prompt says so.
   - It becomes a `tour` message part. The panel renders it as a **Show me** button
     under the answer. It never starts on its own.
   - Add a prompt rule: the text steps come first, and the tour only when the screen
     has targets.
4. **The tour runner,** in the assistant frontend package, mounted once in the panel:
   - a controlled `Tour` with `active`, `step` and `onStepChange`;
   - **steps on another route:** before the step activates, call the host's
     `navigate` (A's slot), then wait up to 3 s for
     `[data-assist="<key>"]`. Without a target, the step shows centred;
   - **en and nb labels** through Mantine's `labels`;
   - focus returns to the panel on close;
   - Escape closes;
   - **portal dropdowns:** a step inside an open `Select` or `Menu` raises that
     dropdown's `zIndex` as Mantine documents. If none of the marked steps needs this,
     say so.
   - The panel stays open while the tour runs. It does not cover the target: the
     panel pushes the page, so the target is in view. Verify this on the marked
     screens.
5. **Markers:** add the `data-assist` markers and registry entries for the screens you
   picked: about 25–40 targets in total, the create buttons, key fields and save
   buttons.
6. **Documentation** (en and nb):
   - **`user/assistant.md`:** *Show me*, what a tour does and does not do, and Escape.
   - **`reference/assistant.md`:** `show_tour`, the registry, the freshness check.
   - **`contributing/`:** a short section on adding a target to a screen. Put it in
     the existing frontend architecture page if it fits, else in
     `module-boundaries.md`'s frontend part. Pick one and say which.
7. **Tests:**
   - **Go:** an unknown target is refused; the step cap; the part shape; embedded
     freshness.
   - **Frontend:**
     - the runner navigates, waits, and falls back to a centred step;
     - the labels in nb;
     - Show me never auto-starts;
     - each package's registry test;
     - i18n parity.

## Do not

- Let the model produce selectors.
- Start a tour without a click.
- Fill in or submit anything during a tour.
- Import one module frontend package from another.
- Mark every screen. Mark the ones listed, and document how to add more.

## How to work

- **Branch:** `feat/assistant-tours`, cut from `origin/main` as it is when you start,
  in the worktree the session that started you names.
- **Setup:** `bun install --frozen-lockfile` first.
- **Plan first:** `docs/superpowers/plans/2026-11-02-assistant-tours.md`, in the house
  shape. Commit it alone.
- **Commits:** one per task, Conventional Commits, each ending with
  `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`.
  - Commits are signed; never pass `--no-gpg-sign`.
  - **Never put a `Docs-Impact` trailer on any commit.**
- **The gates, all green before you push:**
  - `mise run server:check`, `server:test`, `frontend:check`, `docs:check`;
  - plus `bun run gen:client:test`, and `bun run gen:client` with no diff;
  - plus the host build: `cd apps/host/frontend && bun run build`.

  Read the logs.
- **The pull request:** `git push -u origin feat/assistant-tours`, then
  `gh pr create --base main`:
  - title `feat: the Assistant shows the way with guided tours`;
  - the body ending with `🤖 Generated with [Claude Code](https://claude.com/claude-code)`.

  Do not poll CI.

## Report

- The PR URL and the commits.
- The gates.
- The screens marked and the target count.
- Where the contributing section went.
- Departures from the spec.
