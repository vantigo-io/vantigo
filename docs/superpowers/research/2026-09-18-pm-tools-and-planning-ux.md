# Project-management tool patterns: tasks, milestones, planning, Gantt

Survey of official docs/feature pages for Jira, Asana, Monday.com, ClickUp, Linear, Basecamp, Notion,
Smartsheet, MS Project, Trello, and (briefly) Fieldwire/Procore, plus open-source React
Gantt/timeline components. Facts are attributed to vendor docs where fetched; items marked
**[uncertain]** come from secondary sources or weren't independently verified.

## 1. Task model

| Tool | Unit | Assignees | Statuses | Grouping | Subtasks | Extras |
|---|---|---|---|---|---|---|
| **Jira** | "Work item" (task/story/bug/epic) | Single assignee (no native multi-assignee) | Custom workflows per project/type, not fixed | Epics → stories/tasks → subtasks; Sprints; Releases/Versions | Subtasks are a distinct, shallow child type (one level) | Custom fields, priorities, labels, comments/attachments, watchers |
| **Asana** | Task | **Single assignee by design** (see §6); "collaborators" for others | Custom "sections" act as pseudo-status per project; a dedicated Status field/rule also possible | Sections (per-project columns/lists) | Subtasks, and sub-subtasks are possible but Asana recommends flattening; multi-homing (one task in several projects) | Custom fields, tags, priority (custom field), due dates, dependencies, checklists via subtasks, comments/attachments, Rules automation |
| **Monday.com** | "Item" on a board | People column can hold multiple people | Status column: fully custom labels, up to **40 labels per column**, custom colors | Boards → Groups (like sections); item can live on multiple boards via mirroring | Subitems (one level, are near-full items themselves) | Dozens of column types (numbers, text, date, dropdown, formula, mirror, timeline); automations; templates |
| **ClickUp** | Task, inside Space → Folder → List hierarchy | **Multiple Assignees is a native, toggleable "ClickApp"** (available on every plan); closing task by one assignee closes for all | Custom statuses per List/Space (ClickUp is known for deep status customization, e.g. "to do/in progress/review/done" variants) | Space/Folder/List hierarchy; also tags | **Unlimited nesting depth**, up to 1,000 subtasks per task; subtasks are full tasks (own assignees, dates, status, comments) | Checklists are separate/lighter than subtasks: up to 5 levels of indented sub-items, but not assignable, no own due dates, don't appear in reports |
| **Linear** | Issue | Single assignee (multi-assignee not documented/native) **[uncertain — not explicitly confirmed]** | Customizable ordered "workflow states" per team, not fixed; ships with a sensible default | Teams → Projects → (optional) Milestones; Cycles for sprints | Sub-issues supported; depth not documented in fetched pages | Labels, priorities, cycles, projects, comments, "Loops" (2026) for automation tied to project/target-date changes |
| **Basecamp** | To-do (inside a To-do list, inside a Project) | Assignable to one or more people **[uncertain — not verified in this pass]** | Binary done/not-done — no status workflow | Projects → To-do lists | No native subtasks; lists are the grouping unit | Comments, attachments; deliberately minimal — no custom fields, no dependencies, no time tracking on the base plan |
| **Notion** | Page/row in a database | People property, can be multi-select of people | Fully custom "Status"/Select property (no fixed set) | Any database property can group a view (status, assignee, etc.); sub-items via relations | Sub-items via self-relation, arbitrary depth (structurally a general database, not a purpose-built task engine) | Everything is a customizable property — priority, tags, checklists (to-do blocks), comments, all bespoke |
| **Trello** | Card | Multiple members per card | Lists = status/stage, entirely custom, no built-in workflow | Boards → Lists → Cards | Checklists inside a card (with % complete), not "subtasks"; no sub-cards natively | Labels, due dates, attachments, comments, Power-Ups add fields/automation |
| **Smartsheet / MS Project** | Row/Task in a sheet or project plan | Resource(s) assigned via Resource/Assignment fields, MS Project supports multiple resources with % allocation | Fixed lifecycle by % complete + custom status column optional | WBS (outline levels), summary tasks | Native outline hierarchy (parent/child rows), arbitrary depth | Baselines, critical/driving/summary paths (Smartsheet), full resource leveling (MS Project) |
| **Fieldwire / Procore** | Task/punch item tied to a location on a drawing | Assignable to a person/company/trade | Custom or fixed-lifecycle statuses (open/in review/closed for punch items) | Organized by Plan/sheet location, trade, or WBS (Procore) | Not a knowledge-work subtask model — tasks are location-pinned observations/punch items | Photos/markups on drawings, daily reports, RFIs — construction-specific, not comparable 1:1 to SaaS task trackers |

**Cross-cutting facts:**
- **Fixed vs custom statuses**: Only Basecamp (binary done/not-done) and, to a lesser extent, Trello (lists are literally freeform, so "status" is just a list name) avoid a status concept entirely. Jira, Asana, Monday, ClickUp, Linear, Notion all use **customizable, per-team/per-project statuses**, not one universal fixed set — this is now the industry norm, not the exception.
- **Multiple assignees**: Genuinely native and promoted as a feature only in **ClickUp** (opt-in ClickApp) and loosely in Monday/Trello/Notion (a "people" field that happens to allow multiple values). **Asana explicitly rejects multi-assignee by design** (see §6). Jira and Linear are single-assignee with a separate "watchers"/followers concept for visibility.
- **Recurring tasks**: supported in Asana, ClickUp, Monday, Notion (via automation/template duplication); not a first-class Basecamp/Trello-core feature (Trello via Power-Up/Butler).
- **Checklists inside tasks vs. subtasks**: tools increasingly separate these two concepts — checklist = lightweight, non-assignable, no own dates, doesn't roll up to reporting; subtask = full first-class work item. ClickUp's docs make this distinction explicit.

## 2. Milestones

Two distinct models found:

**A. Milestone as a lightweight, dated marker (separate entity, thin schema)** — Linear, Asana, Monday(-ish).
- **Linear**: a milestone is its own object inside a Project: name/description + **optional target date only**; completion % is *derived* from the statuses of issues assigned to it (not entered manually). Issues link to a milestone via shortcut, command menu, drag-and-drop, or an auto-suggestion at issue-creation time. On the timeline/roadmap, milestones render as diamond markers with a completion-percentage indicator; the active one gets a distinct (yellow) icon. A milestone can later be "promoted" into a full project if it grows. (Source: linear.app/docs/project-milestones)
- **Asana**: a milestone is a special task type (a zero-duration marker) that appears on the Timeline as a diamond/marker with just a date — it is structurally a task, not a separate table, but is visually and semantically treated as a marker of a key date rather than an item of work.
- **Jira**: no native milestone entity at all — Releases/Versions and Sprint boundaries serve the same purpose (a due-dated container work items can be assigned to), and the roadmap shows them as bands rather than diamonds.

**B. Milestone as a task/date flag, not a separate entity** — most kanban/board-first tools (Trello has no milestone concept at all; Monday.com's "Milestone" is just a column type/label combination on a normal item; Notion has no native milestone type — teams fake it with a Select property value or a template item).

Trade-offs:
- A **separate lightweight entity** (Linear's model) keeps milestones from cluttering the task list, gives a clean roadmap symbol, and derives progress automatically from linked issues — but requires new UI/data-model surface (a table, a relation, a percentage rollup).
- A **task-flag milestone** (Asana/Monday style) needs zero new schema (reuse the task table, add a boolean/duration-zero flag) and is much cheaper to ship, at the cost of milestones being "just tasks that happen to render as diamonds" and needing filtering logic to keep them out of normal work views.

## 3. Gantt / timeline

**Dependency types**: Finish-to-Start (FS) is the default and by far the most common; Start-to-Start (SS) and Finish-to-Finish (FF) are the next most supported; Start-to-Finish (SF) is rare and mostly an MS Project/Smartsheet-class feature. Notion's Gantt (via Timeline view + "Depends on" property) supports **only finish-to-start** dependencies and — notably — does **not** auto-shift dependent dates when a date changes, unlike every "real" Gantt tool (Jira roadmap does; monday.com, ClickUp, Smartsheet, MS Project all do).

**Lag/lead**: A signed offset applied to a dependency (MS Project convention: negative = lead/overlap, positive = lag/delay), edited by double-clicking the dependency line or editing a Predecessors field. Present in MS Project, Smartsheet, and PRO-tier DHTMLX/Bryntum-class components; not found as a documented feature in Jira, Asana, Trello, Notion.

**Auto-shift on change**: monday.com offers three explicit dependency modes: **Flexible** (auto-adjusts dependent dates, no overlap allowed), and a **"No action"** mode that just draws the connector without moving dates — i.e., auto-shift is a configurable, not universal, behavior even within one tool. Smartsheet and MS Project auto-shift by default. ClickUp has a "Reschedule dependencies" toggle in Gantt layout options — off by default, and when on, dragging one task cascades through the whole chain.

**Critical path**: A first-class, named feature in **Smartsheet** (auto-computed, tasks highlighted in red) and **MS Project** (classic CPM), and offered in commercial React components (SVAR, DHTMLX PRO, Bryntum, Syncfusion). **Not** offered in Jira, Asana, Trello, Notion, or in the free/community tiers of most Gantt UI libraries. DHTMLX explicitly gates critical-path calculation, resource leveling, baselines, and constraints behind its paid PRO edition — the free Community Edition (MIT) only does dependencies + manual scheduling + milestones.

**Baselines** (planned vs. actual comparison): native in Smartsheet (toggle in toolbar, requires Start/End Date columns) and MS Project; a common commercial add-on in JS Gantt libraries; not present in Jira/Asana/Trello/Notion out of the box.

**Drag to reschedule, zoom levels, grouping**: essentially universal across every timeline/Gantt implementation surveyed (Jira, Asana, monday.com, ClickUp, Smartsheet, MS Project, Notion, frappe-gantt, SVAR, DHTMLX). Typical zoom presets: Day / Week / Month / Year (frappe-gantt's four built-in view modes are a good minimal set). Grouping is normally by the tool's native hierarchy (epic/sprint in Jira, section in Asana, group in monday, folder/list in ClickUp).

**Tools that skip Gantt/full dependency graphs entirely:**
- **Basecamp** — no Gantt, no cross-task dependencies, and no time tracking on the base plan, by explicit design choice. Its stated rationale (Hill Charts page): task lists show *done/not-done* but not *what's unknown or where the team is stuck*; a Hill Chart instead asks each contributor to place a dot on a hill (uphill = "still figuring it out", downhill = "now executing") — a **human-generated**, subjective progress signal rather than a computed one, updated over time into a scrubbable history. Basecamp's broader design philosophy is captured in their own framing (paraphrased from their marketing/founders' writing): "features are deliberately limited... perfection is achieved not when there's nothing more to add, but when there's nothing left to take away." This is a deliberate rejection of Gantt-style timeline complexity for small/agency teams, not an oversight.
- **Trello** — kanban-only at the core; Gantt/timeline is only available via a Power-Up (3rd-party or Atlassian's own "Advanced Checklists"/Timeline power-ups), confirming Trello's core product never adopted native Gantt.

**"Timeline" (lightweight) vs "Gantt" (full)** — a real, named distinction in vendor language:
- Jira explicitly frames its **Timeline** (single-project, dates + basic dependencies) as the lightweight option and reserves **Plans** (formerly "Advanced Roadmaps") for the full multi-team, capacity-aware, scenario-comparison Gantt-class tool — gated to Premium.
- Asana's **Timeline** view is a simplified Gantt (drag, dependencies, milestones) but is explicitly described by third parties as lacking "advanced features that experienced project managers expect from true Gantt chart software" — hence the existence of the Instagantt add-on/integration Asana itself sells in its marketplace.
- Notion's **Timeline** database view is Gantt-*like* (drag, resize, single-type dependency) but is positioned by Notion as "more than a Gantt chart" / a flexible database view, not a PM-grade scheduling engine.
- monday.com and ClickUp use "Gantt" as the view name and ship closer to full functionality (multi-mode dependencies, critical path claims, baselines) than Jira/Asana/Notion's "timeline."
- Smartsheet and MS Project are the reference "full Gantt" tools: critical path, baselines, driving/summary paths, resource leveling.

## 4. Kanban/board, list/table, saved views, my-tasks, calendar, workload

- **Views available per tool** (board/kanban, list/table, calendar, timeline/Gantt, workload) are now a shared checklist most competitors converge on: Asana (List, Board, Timeline, Calendar, Workload via a paid tier), monday.com (Kanban, Gantt, Timeline, Workload, Calendar, Table — "views" is a monday.com core selling point), ClickUp (List, Board, Gantt, Calendar, and many more "ClickApp" views), Jira (Board, Backlog, Timeline, List/table-like "work item list"), Notion (Table, Board, Timeline, Calendar, List, Gallery — freely combinable on one database), Trello (Board is default; Calendar/Timeline are Power-Ups), Linear (List, Board per team/project; no native calendar/workload view found in docs).
- **Saved views/filters**: standard in Asana, ClickUp, Monday, Jira (JQL-based filters/boards), Linear (custom views); Trello's filter is lighter-weight and per-board rather than a saved cross-board "view" object.
- **"My tasks" across projects**: named, first-class features in Asana ("My Tasks") and ClickUp ("Home"/"My Tasks"); Jira has "Your work"/assigned-to-me JQL; Linear has "My Issues"; Trello has no cross-board personal task aggregation without Power-Ups.
- **Workload/capacity views**: Asana (Workload, gated to Business/paid tiers), monday.com (Workload view), ClickUp (Workload view + Box view); not found as native in Jira core (capacity planning lives in the paid "Plans"/Advanced Roadmaps), Linear, Trello, Basecamp, or Notion.

## 5. Templates, automation, notifications, mentions, activity log

- **Templates**: near-universal — Asana (task/project templates + a large public template gallery), monday.com (column templates + board templates), ClickUp (templates for tasks/lists/spaces), Notion (extensive first- and third-party template marketplace, e.g. its own "Projects (Tasks and Timeline)" template), Jira (project templates for Scrum/Kanban/etc.).
- **Automation/rules**: Asana "Rules" (trigger → action, e.g., move section → assign, add tag), monday.com "Automations" (similar trigger/action recipes, plus cross-board automations), ClickUp "Automations," Jira "Automation for Jira" (rule builder), Linear introduced **"Loops"** (Sept 2026 changelog) — automations tied to product-management events like a project's target date changing, which can auto-update a linked plan and post to Slack. Trello's automation is "Butler," and Basecamp intentionally ships none.
- **Notifications/digests, mentions, activity log**: standard @mention-in-comment support across Jira, Asana, monday.com, ClickUp, Linear, Notion, Trello, Basecamp; most offer a daily/weekly digest email and an in-app activity/notification feed; a per-item "activity log" (who changed what, when) is present in Jira (issue history), Asana (task activity feed), ClickUp, monday.com (log/activity), Linear, and Notion (page history, though Notion's page history is more document-versioning than task-audit-trail).

## 6. Shipping order — what comes first vs. later (evidence from changelogs/history)

- **Asana**: task lists + assignee/due-date model came first (core product since 2011-2012); **Timeline (Gantt-like) view was a major, technically hard, later addition** — Asana's own engineering blog post "The Timeline of a Feature Launch" (2019) describes a multi-year, phased rollout, and Asana still sells/promotes the third-party **Instagantt** integration for teams wanting "true" Gantt features Timeline doesn't cover — evidence that even Asana didn't consider its own native timeline a full Gantt replacement at launch.
- **Asana's one-assignee rule** is a stated, deliberate design decision (modeled explicitly on Apple's "Directly Responsible Individual" concept): one owner per task for unambiguous accountability; multi-person work is expected to be split into subtasks with individual owners, with "collaborators" (not co-assignees) used for visibility. This is presented by Asana as a philosophy, not a technical limitation.
- **Linear**: issues/projects/cycles shipped as the core, opinionated model from early on ("first-class, not duct-taped together" is Linear's own framing). **Milestones and the visual timeline/roadmap came later** and have continued to be actively reworked — e.g., the 2021 "Roadmap Timeline" preview, a 2023 "Project Views" changelog entry, and a 2024-02-29 changelog entitled "A new era for the timeline" that specifically added **milestones onto the timeline visualization** (implying milestones as data predated their timeline rendering). This is a clear "ship the entity, then ship the visualization" sequencing.
- **ClickUp**: positions itself as shipping breadth early and fast (multiple views, ClickApps) — its Gantt view, multiple-assignees ClickApp, and unlimited-nesting subtasks are all framed as differentiating, added-later power features layered onto the core Space/Folder/List/Task hierarchy.
- **monday.com**: built board/column primitives first (a generalized "Work OS" grid), then layered Gantt/Timeline/Workload as specific *view types* over the same item data — i.e., the underlying item/column model doesn't change per view, only the rendering does. This "one data model, many views" pattern recurs across Notion, ClickUp, and monday.com and looks like the dominant modern architecture.
- **What small teams actually use most [uncertain, secondary sources only]**: several vendor/blog sources converge on the claim that **kanban board + simple due dates is what most small teams actually use daily**, and that Gantt/timeline is reached for during planning/kickoff or by PMs specifically, not by day-to-day contributors — one source phrased it as "a simpler tool that people consistently use will outperform a feature-rich tool that becomes shelfware." Trello is repeatedly cited as the reference tool for "small teams, solo users, or anyone just getting started." I could not find a rigorous, methodologically transparent industry survey (e.g., a named market-research firm's data) confirming feature-usage percentages — treat this as directional vendor/blog consensus, not hard data.

## 7. Accessibility/complexity trade-offs and open-source React Gantt/timeline options

**Why Gantt UIs are heavy to build:**
- Rendering many overlapping horizontal bars with drag-resize, drag-move, and snap-to-grid interactions across a long horizontal timeline.
- Dependency-line rendering (SVG/canvas arrows that must re-route live as bars move) and cascade recalculation (auto-shifting every downstream dependent task, potentially across a whole project graph) on every drag.
- Critical-path computation is a real graph algorithm (longest path through a DAG honoring durations and calendars/holidays) — not just UI, real scheduling logic.
- Virtualization is required for both axes at scale: many rows (tasks) and a long horizontal date range — naive DOM rendering degrades fast; most serious libraries (SVAR, mantine-gantt, DHTMLX) explicitly advertise virtualized row rendering as a feature, implying it's non-trivial and a differentiator.
- Keyboard accessibility/ARIA for a drag-based, spatial UI is inherently harder than for a list/table — mantine-gantt calls out "full keyboard navigation and ARIA attributes" as a selling point, implying many competitors skip this.
- Zoom levels (day/week/month/year) each need different date-bucketing, label density, and bar-width math.

**Open-source / commercial React Gantt & timeline landscape:**

| Library | License | Maintenance | Notes |
|---|---|---|---|
| **frappe/gantt** | MIT | Active-ish (427+ commits, dozens of open issues/PRs; used by ERPNext) | Vanilla JS, not React-native; view modes Day/Week/Month/Year; drag-to-reschedule with optional dependency cascade (`move_dependencies`); no dependency-type variety (FS-style bars only) documented; several unofficial React wrappers exist (`react-frappe-gantt`, `frappe-gantt-react`) rather than an official one — adds an integration/maintenance risk layer. |
| **gantt-task-react** (MaTeMaTuK) | MIT | **Stale** — latest version (0.3.9) is ~4 years old, no recent commits/PR activity **[flag: likely unmaintained]** | TypeScript, React-native; a reasonable evaluation candidate architecturally but risky to adopt long-term without forking. |
| **SVAR React Gantt** (`svar.dev`) | MIT (per their own comparison) | Actively maintained per vendor's own blog claim **[vendor-sourced, not independently verified]** | Described as "pure React" (not a JS-core + wrapper); supports critical path, drag-and-drop, exports; positioned as the only genuinely open-source full-featured option in SVAR's own comparison table (naturally self-favorable — treat with some skepticism). |
| **DHTMLX Gantt** | **Dual-licensed**: Community Edition = MIT (dependencies, milestones, manual scheduling, plugins); **PRO Edition = commercial** (critical path, auto-scheduling, resource management, baselines, constraints, official React/Vue/Angular wrapper components) | Actively maintained, major v10.0 release referenced | The free tier is genuinely usable for a "timeline, not full Gantt" scope; critical path and baselines specifically require paying. |
| **Bryntum Gantt** | Commercial only (EUL for non-commercial/eval, OEM subscription for commercial/SaaS use, from ~$940/developer) | Actively maintained, enterprise-grade | Full-featured (critical path, resources, baselines) but not viable without a paid license for a commercial SaaS product. |
| **vis-timeline** (visjs) | Dual **Apache-2.0 / MIT** | Actively maintained (recent releases/PRs as of mid-2026); community-run, "call for maintainers" noted — bus-factor risk | General-purpose timeline (items/ranges/groups), not a dependency-aware Gantt — good fit for a lightweight "timeline of things with dates," not full project scheduling. |
| **react-big-calendar** | MIT (widely known; not independently refetched this pass) **[uncertain — from secondary source, not the repo itself]** | Long-running, moderately active community project | A calendar (day/week/month/agenda), not a Gantt — relevant only for the "calendar view" requirement, not for milestones/timeline planning. |
| **mantine-gantt** (WojakGra) | MIT | **Very early-stage / low-traction** (1 star, 1 fork, 0 issues, ~47 commits) — functionally promising but essentially a solo/early project, not proven in production | **Directly relevant**: built specifically for Mantine, deep integration (Styles API, `classNames`/`styles`, Mantine color tokens, `@mantine/core`/`@mantine/hooks` + `dayjs` peer deps). Features claimed: drag-to-reschedule, resize-to-change-duration, dependency arrows with interactive creation, **virtualized row rendering**, keyboard nav + ARIA. Given Vantigo likely uses Mantine, this is the most architecturally aligned candidate but carries real maintenance-risk given its size/traction — needs a trial/spike and a real look at the source before depending on it. A second, similarly-named `mantine-gantt-chart` (OctopBP) also exists — worth comparing both before choosing. |

## Synthesis

**(a) Minimal task model shared by essentially all tools**: an item with a **title**, **status** (increasingly a custom/per-team set, not fixed), an **optional single owner/assignee** (multi-assignee is the exception, not the rule — only ClickUp treats it as a first-class, promoted feature), a **due/start date pair**, a **parent grouping** (section/list/board/epic), free-text **comments**, and **attachments**. Everything else (custom fields, priorities, tags, checklists, dependencies) is an increasingly-common but still optional layer on top of that core five-field shape.

**(b) Two viable milestone models:**
1. **Task-flag milestone** (Asana/monday.com style): a milestone is just a task/item with a "this is a milestone" marker and (typically) zero duration. Cheapest to build — no new table, reuses existing task CRUD, filtering, and permissions; con: needs UI logic to keep milestones visually distinct and out of normal work-list views, and progress isn't naturally "rolled up" from anything (it's just done/not-done itself).
2. **Separate lightweight entity with derived progress** (Linear style): milestone is its own row with only a name + optional target date; tasks link to it via a foreign key; completion % is computed from linked tasks' statuses. Costs more up front (new table/relation, a rollup calculation) but yields a cleaner roadmap visualization and a genuinely meaningful "progress" number instead of a binary flag.
Given Vantigo already has a "projects" module with billing lines and roles, model 2 (separate milestone entity linked to tasks, closer to Linear's shape) is likely worth the modest extra schema cost, since project-level rollup reporting is probably already a goal for billing purposes.

**(c) Recommended progression for Vantigo (projects → time tracking → planning), based on the evidence above:**
1. **Time tracking first** (already planned) — orthogonal to planning features and needed regardless.
2. **Tasks with a single assignee, custom-but-simple statuses, due dates, and comments** — this is the shared minimal model in §(a); ship before anything visual. Keep assignee singular (Asana's accountability argument is well-documented and avoids a whole class of "who owns this" ambiguity and notification-fanout complexity) — add ClickUp-style multi-assignee only if a real customer need appears.
3. **A board/list view** (kanban + table) over that task model — this is what "small teams actually use most" per the (soft, vendor-sourced) evidence in §6, and is dramatically cheaper to build than any timeline/Gantt.
4. **Milestones as a separate lightweight entity** (name + optional target date + linked tasks, derived % complete) — cheap once tasks exist, and gives project-level rollup that's plausibly useful for billing/reporting given Vantigo's project-code (KVEM1000-style) structure.
5. **A lightweight "timeline" view** (Jira/Notion-style: drag bars, single FS dependency type, no auto-shift or with a simple opt-in auto-shift, day/week/month/year zoom) — only after tasks + milestones are solid and there's demonstrated demand. This is explicitly the tier below "real Gantt" that Jira, Asana, and Notion all ship and market as a distinct, lesser product from full scheduling tools.
6. **Never (or very late), unless a specific customer segment demands it**: full dependency-type variety (SS/FF/SF + lag), critical-path computation, baselines, and resource leveling — these are Smartsheet/MS-Project-class features, are expensive (real scheduling algorithms, not just UI), and are absent from Jira, Asana, monday's baseline offering, ClickUp's free tier, Notion, Trello, and Linear entirely. If ever needed, evaluate DHTMLX PRO or SVAR rather than building critical-path math in-house.
7. Consider a **Basecamp-style qualitative alternative to Gantt** (a single "how's this going" subjective marker per project/phase) as a much cheaper stand-in for stakeholders who just want "are we on track," before investing in a full timeline.

**(d) Pitfalls to avoid:**
- **Building a full Gantt too early.** Even category leaders (Asana, Jira, Notion) explicitly ship a lightweight "timeline" and treat full Gantt (critical path, baselines, resource leveling) as a separate, later, often paywalled tier — or skip it and sell into an integration (Asana + Instagantt) instead of building it themselves. This is strong evidence it's not worth doing early.
- **Custom statuses done badly**: nearly every modern tool supports custom per-project/per-team statuses, but this creates real complexity (reporting/rollups need a canonical "is this done" concept independent of the label). Plan a canonical status *category* (todo/in-progress/done) underneath any customizable label set from day one, rather than retrofitting it later.
- **Multi-assignee**: adds real complexity (who gets notified, who "owns" a comment/mention, how completion works when one of several assignees marks done — ClickUp's own docs note that when *one* assignee closes a task it closes for everyone, which surprises users). Asana's single-assignee-plus-subtasks pattern avoids this entirely; recommend following it unless a customer explicitly needs shared ownership.
- **Auto-shift/cascade dependency logic**: monday.com's need for three distinct modes (Flexible/rigid/no-action) and ClickUp's dedicated opt-in toggle both show that "when I move a task, what happens to its dependents" is genuinely contentious/non-obvious even for teams with a fully custom-status model — treat it as its own design decision, not a bolt-on detail, if/when dependencies are built.
- **Milestone scope creep**: Linear's own changelog history shows milestones as data (fields, linking) and milestones as a *timeline visualization* were shipped years apart — decoupling "can I mark a milestone" from "can I see it on a pretty timeline" is a legitimate, low-risk way to sequence the work.
- **Open-source Gantt component risk**: several MIT-licensed React options exist, but the closest fit for a Mantine-based app (`mantine-gantt`) is very early-stage (single-digit stars/forks) — treat any adoption as a spike/trial with a fallback plan (e.g., DHTMLX Community MIT tier, or building a minimal bespoke bar-chart view) rather than a committed dependency, and re-check maintenance status before relying on it in production.

## Sources

- https://support.atlassian.com/jira-software-cloud/docs/what-is-the-roadmap/
- https://www.atlassian.com/software/jira/guides/basic-roadmaps/overview
- https://www.atlassian.com/agile/tutorials/epics
- https://help.asana.com/s/article/timeline?language=en_US
- https://help.asana.com/s/article/all-asana-features?language=en_US
- https://asana.com/features/goals-reporting/portfolios
- https://asana.com/resources/why-one-assignee
- https://blog.asana.com/2019/05/timeline-feature-launch
- https://www.instagantt.com/product/asana-integration
- https://support.monday.com/hc/en-us/sections/24277117556882-Gantt-dependencies
- https://support.monday.com/hc/en-us/articles/360007402599-Dependencies-on-monday-com
- https://monday.com/features/gantt
- https://support.monday.com/hc/en-us/articles/360001269685-The-Status-Column
- https://help.clickup.com/hc/en-us/articles/20480724378135-Hierarchy-best-practices
- https://help.clickup.com/hc/en-us/articles/6309029762583-Multiple-Assignees
- https://help.clickup.com/hc/en-us/articles/6309825777943-Intro-to-subtasks
- https://help.clickup.com/hc/en-us/articles/6309942197783-Use-task-checklists
- https://clickup.com/learn/topic/task-management/concepts/subtasks/
- https://linear.app/docs/project-milestones
- https://linear.app/docs/conceptual-model
- https://linear.app/docs/projects
- https://linear.app/docs/use-cycles
- https://linear.app/changelog/2024-02-29-milestones-on-the-timeline
- https://linear.app/changelog/2021-05-27-linear-preview-roadmap-timeline
- https://linear.app/changelog/2023-05-25-project-views
- https://linear.app/changelog/2026-09-14-loops-for-product-management
- https://basecamp.com/hill-charts
- https://3.basecamp-help.com/article/412-hill-charts
- https://www.notion.com/help/timelines
- https://www.notion.com/help/guides/timeline-view-unlocks-high-output-planning-for-your-team
- https://www.notion.com/templates/projects-tasks-and-timeline
- https://help.smartsheet.com/learning-track/level-3-solutions/gantt-chart-dependencies
- https://help.smartsheet.com/learning-track/project-fundamentals-part-2-project-settings/baselines-and-critical-path
- https://www.smartsheet.com/content/gantt-chart-critical-path
- https://onplana.com/blog/dependency-types-deep-dive
- https://www.paymoapp.com/blog/lead-lag-and-constraints/
- https://trello.com/power-ups
- https://support.atlassian.com/trello/docs/power-ups-made-by-trello/
- https://www.fieldwire.com/blog/field-task-management/
- https://www.fieldwire.com/blog/construction-task-management-software-field/
- https://www.procore.com/library/gantt-charts
- https://support.procore.com/products/online/user-guide/project-level/schedule/tutorials/view-a-gantt-schedule
- https://github.com/frappe/gantt
- https://svar.dev/blog/top-react-gantt-charts/
- https://svar.dev/react/gantt/
- https://dhtmlx.com/docs/products/dhtmlxGantt/open-source/
- https://dhtmlx.com/blog/dhtmlx-gantt-licensing-options-explained-gpl-mit-community-pro-editions/
- https://bryntum.com/blog/react-fullcalendar-vs-big-calendar/
- https://github.com/visjs/vis-timeline
- https://github.com/visjs/vis-timeline/blob/master/LICENSE.md
- https://github.com/WojakGra/mantine-gantt
- https://github.com/MaTeMaTuK/gantt-task-react
- https://snyk.io/advisor/npm-package/@nkita/gantt-task-react (secondary, re: gantt-task-react maintenance status)
- https://monday.com/blog/rnd/gantt-vs-kanban/ (secondary, "small teams" usage framing)
