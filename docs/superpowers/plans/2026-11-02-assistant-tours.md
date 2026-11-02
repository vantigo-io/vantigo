# The Assistant, delivery T — "Show me how" with guided tours: implementation plan

> **For agentic workers:** implement this plan task by task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** a person asks "how do I add a metering point?". The answer gives the steps and the
documentation link, as today, plus a **Show me** button. Clicking it starts a Mantine `Tour`:
it navigates to the right screen if it is not on show, points at *New metering point*, then at
the fields that matter, then at *Save*, with a short title and text in the person's language per
step. A step whose control is not on screen (hidden by permissions, or not there after 3 s)
shows centred, with its text. The model never writes a CSS selector: it chooses from a registry
of targets that each module frontend declares.

**Architecture:** each module frontend with targets keeps `src/assist-targets.json` (key, route,
label and description in en and nb) and exports it from its index as `assistTargets`; the control
carries `data-assist="<key>"`. `@vantigo/frontend-shell` owns the `AssistTarget` type and
`assistTargetProblems`, the pure check every package's test runs over its own source (a registered
key with no marker, a marker with no entry, a marker that is not a string literal). The host
collects every package's targets and hands them to `useAssistantShell`; no module imports another.
`tools/assist/gen-targets.ts` merges the packages' JSON files into
`apps/server/internal/assistant/assist-targets.json` (key, route, English description, sorted),
embedded in the module and wired into `go generate`; a Go test re-derives the merge from the
packages' files and fails when the embedded copy is stale. `show_tour(steps)` takes `{target,
title, body}` (at most 8), refuses a target outside the embedded registry, and becomes a `tour`
part; the target enum is in the tool's description. The panel draws a **Show me** button for a
`tour` part. The tour runner is a controlled `Tour` mounted once by the shell hook (so it works
from the panel and across routes): on a step it navigates through the host's `onNavigate` when the
step's route differs, waits up to 3 s for `[data-assist="key"]`, and shows the step centred if the
control never comes. The overlay lets clicks through the spotlight, so the person can open a dialog
the next step points into; the tour itself never clicks, fills in or submits anything.

**Spec:** [`docs/superpowers/specs/2026-11-01-assistant-design.md`](../specs/2026-11-01-assistant-design.md), S11 (and S5, S10).
**Brief:** [`…/2026-11-01-assistant-briefs/t-tours.md`](2026-11-01-assistant-briefs/t-tours.md).
**Predecessors:** [`2026-11-01-assistant-app.md`](2026-11-01-assistant-app.md) (A, #214), [`2026-11-02-assistant-writes.md`](2026-11-02-assistant-writes.md) (B, #216).

## Plan readings

1. **A step's route is not in the part.** The model gives `target`; the route comes from the
   registry, so the model cannot send the person somewhere the registry does not say. Routes with a
   parameter (`/customers/{customerId}`) cannot be navigated to: the runner shows such a step
   centred when the person is elsewhere, and the prompt says to start those tours only from the
   record.
2. **Targets inside dialogs** exist only once the dialog is open. The overlay passes clicks
   through the spotlight (`withOverlayInteraction`), the step text says "click it, then Next", and
   a dialog not opened within 3 s degrades to a centred step.
3. **`Tour` shows centred** when a step has no `target`; the runner passes the selector only for
   steps found, and keeps the tour hidden while it waits.
4. **Portal dropdowns:** no marked step points into an open `Select` or `Menu`, so no `zIndex`
   change is needed (documented in the contributing section).
5. **`@mantine/core` 9.7.4** `Tour`: controlled `active`/`step`/`onStepChange`, `labels`,
   `closeOnEscape`, `withKeyboardNavigation`, `zIndex` default 10000.
6. **The part is contract.** `AssistantPart` gains the `tour` type and `steps`; the Go server types
   and the TypeScript client are regenerated.
7. **Embedded copies** (docs, the registry) are refreshed with `go generate`; the docs change in
   this delivery needs it or the docs freshness test fails.

## Tasks

- [ ] **1. The plan.** This file, committed alone, with no `Docs-Impact` trailer.
- [ ] **2. The registry in the frontends.** `AssistTarget` and `assistTargetProblems` in
  frontend-shell; `assist-targets.json`, the export and the registry test in the six packages
  (customers, energy, invoices, time, expenses, projects); the `data-assist` markers.
- [ ] **3. The server learns the registry.** `tools/assist/gen-targets.ts`, the embedded JSON, the
  `go:generate` line, the freshness test; the `tour` part in `openapi/assistant.yaml`, codegen and
  the TypeScript client.
- [ ] **4. `show_tour`.** The tool, its validation, the part, the prompt rule, Go tests.
- [ ] **5. The runner and the button.** Tour runner, store, labels, Show me in the message list,
  the shell hook's `assistTargets` option, the host wiring, frontend tests and i18n parity.
- [ ] **6. Documentation (en and nb).** `user/assistant.md`, `reference/assistant.md`, the
  contributing section, `go generate` for the embedded docs.
- [ ] **7. The gates.** `server:check`, `server:test`, `frontend:check`, `docs:check`,
  `gen:client:test`, `gen:client` with no diff, the host build, `go test ./cmd/vantigo/`.
