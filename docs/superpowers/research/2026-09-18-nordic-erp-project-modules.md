# Project/planning features in Norwegian & Nordic business software

Research for Vantigo's projects/time-tracking roadmap. Facts from official docs/help
centres/pricing pages (mostly Norwegian). Uncertain items marked **UNCERTAIN**.

## 1. Tripletex

1. **Entity**: Prosjektnavn, start/sluttdato, prosjektnummer (series), prosjektleder,
   avdeling, kunde, valuta. **Kontrakt** tab sets Fastpris vs Timepris (3 rate models) +
   mva-sats. Sub-structure: 2-level only, **hovedprosjekt → underprosjekt** (no phases/
   milestones — architects are told to model phases *as* sub-projects). Separate loosely
   coupled **Oppgaver** (tasks) module: status, priority, budget hours, notify-list, no
   dependencies/board. **Prosjektaktiviteter** are the real work-breakdown unit.
2. **Time**: logged as project **+ activity** (never project-only); activities must exist
   first. 3 rate models (per-employee, per-activity-per-employee "project-specific", flat).
   Approval optional (godkjenning av timer til fakturering, separat godkjenning/fakturering,
   "klar til fakturering" flag). Overtime is **manual**: log on an overtidslønnsart
   (2005/2006/2007/2008), and to *bill* it you must duplicate the activity at 150% rate —
   no automatic multiplier.
3. **Planning**: **No Gantt, no milestones, no dependencies, no kanban** (0 help-centre
   hits for "gantt"). Has: drag-and-drop **Ressursplanleggeren** (calendar, no external
   calendar sync), **Kapasitetsplan**, **Ressursplan** with Ansatt/Prosjekt modes and
   Prosjekt/Aktivitet/Ansatt levels.
4. **Money**: 4 budget layers (overall, per-activity, per-employee, P&L/resultat).
   Percent-complete billing option ("% Ferdig registrert" vs default "Fakturert honorar").
   **Prosjektprognose** (auto forecast = utført + gjenstående timer). Distinguishes
   real-time **Status** vs posted **Resultat**. **Fakturareserve** split into
   honorarreserve/a-kontoreserve/annen reserve. **A-konto fully modelled**: separate GL
   accounts (2900/3000), balance-sheet parked until final invoice, auto zero-invoice,
   must be zero before project close. **Innestående (retainage) is manual** (negative
   line on each a-konto invoice, reversed at final invoice) — no built-in feature.
   Viderefakturering (pass-through of supplier costs) with configurable markup.
5. **Docs**: per-project document archive. **Kontrollskjema** (control forms) —
   differentiated: ships NHO Elektro forms (samsvarserklæring, sluttkontroll, etc.),
   form builder, can gate invoicing/time entry on signature. **Boligmappa** integration
   (Elektro/VVS only): address/matrikkel lookup, push FDV docs. Mobile app can log
   hours/materials/control forms but **cannot invoice or log overtime**. No customer portal.
6. **Pricing**: Basis 199 / Smart 299 / Pro 479 / **Komplett 649** / **Elektro 699** /
   **VVS 699** kr/mnd. Full project module (accounting, invoicing, budgets, resource
   planner, doc archive) only in Komplett/Elektro/VVS. Pro only gets "project as free
   dimension" (postable, reportable, no invoicing/budgets/planner). Kontrollskjema +
   Boligmappa are Elektro/VVS-exclusive.

## 2. PowerOffice Go

1. **Entity**: Prosjektkode (auto or custom alphanumeric), navn, prosjektleder,
   **status** (Ikke startet/Pågår/På vent/Fullført/Arkivert), **Grad av ferdigstillelse**
   (%), kunde, avdeling, lokasjon, dates. Faktureringsmetode = Timer / Timer og
   kostnader / **Fastpris**. **Delprosjekt** (1-level subprojects, code `main.sub`),
   inherits then can override PM/customer/dept/dates. No phases/milestones/tasks.
2. **Time**: logged against **aktivitet** + optional prosjekt/delprosjekt + avdeling +
   timeart (linked to lønnsart for payroll). Billable flag per entry. **Rate hierarchy**
   (highest wins): Prismatrise > Delprosjekt > Prosjekt > Aktivitet > Kunde > Kundegruppe
   > Ansatt > Klient. Approval: **Delvis innsendt → Innsendt → Delvis godkjent →
   Godkjent**, partial-week submit, reopenable. Flexitime/avspasering tracked, but
   overtime not rule-automated.
3. **Planning**: **none** — no Gantt/resourcing/kanban/milestones/dependencies. PowerOffice
   explicitly outsources planning to partner **Moment** (master for customers/projects/
   numbering) and to ProResult/Entreprenør/SmartDok/Mobile Worker.
4. **Money**: 3 budget types (simple, hours×rate, per-GL-account). Prosjektrapport tracks
   **ufakturerte timer** (WIP proxy), bruttomargin/nettomargin, **fremdrift (%)**.
   Prosjektdashboard with hours/revenue/margin widgets. **A-konto explicitly NOT
   supported** ("PowerOffice Go har ingen egen funksjon for a-konto-fakturering") —
   documented workaround is a manual "A-konto" product line; partial invoicing done via
   separate sub-projects.
5. **Docs**: no dedicated project document archive/checklists/portal found; mobile app
   handles time + expense receipts only.
6. **Pricing**: Regnskap 425 kr/mnd; Timeregistrering 100 kr/mnd + 50 kr/bruker (gates
   Team/Aktiviteter/Påslag/per-employee budget tabs and billable time entry). No separate
   "Prosjekt" price — bundled with accounting as a dimension, richer with Timeregistrering.

## 3. Visma.net ERP (project accounting) / Visma.net "Prosjektstyring"

Two things share the label: native **ERP project accounting** (Acumatica-lineage), and
a marketed **"Prosjektstyring"** which vismasoftware.no states **is Severa** (§6) — treat
Gantt/resourcing claims on Visma.net's own pages as Severa's, not the ERP's.

1. **Entity**: Project ID, customer, template, **status** (In planning/Active/Completed/
   Suspended/Cancelled), Completed % (manual). Revenue budget level = Task / Task+item /
   Task+cost code. **Project Tasks** are the work-breakdown unit (smallest billable piece);
   invoicing rules assigned per task; nesting/phases **not documented (UNCERTAIN)**.
   **Change orders** with approval workflow — rare among peers.
2. **Time**: Time cards (weekly) or Time activities, booked to project+task. Rate tables +
   rate types + rate lookup rules (varies by employee/class/project/task/customer/item/date).
3. **Planning**: **none in the ERP module itself** — docs explicitly say resource
   allocation/employee assignment isn't detailed beyond an Employees list tab.
4. **Money**: budgets per task (cost + auto-generated revenue via allocation rules),
   original vs revised columns. **Percent-complete, 3 methods per task**: manual,
   budgeted-quantity ratio, budgeted-amount ratio (docs note whole-project % isn't
   directly trackable — workaround via a dummy task). Two invoicing models: **progress
   invoicing** (fixed-price, from revenue-budget "pending invoice amount" lines — the
   Visma a-konto analogue) and transaction-based T&M. Commitments (PO/SO) tracking.
5. **Docs**: activity-history log (tasks/events/emails/notes); no doc archive/checklist/
   portal found.
6. **Pricing**: not published (partner-sold).

## 4. Visma Business / Business NXT

**Effectively no project module.** Business NXT docs have 5 areas (General data,
Accounting, Logistics, Sysadmin, Connected services) — **no Project section anywhere**.
"Project" is a GL dimension (12 linkable cost carriers). Visma's own module list says
**"Project Management — available via Severa add-on"**, and Severa's integration docs
confirm: **Severa projects map to Business NXT's `OrgUnitX` dimension tables**; ERP
"treats projects as dimensional structures... rather than operating as a standalone
project management dimension." Pricing: Pro from 15,126 / Elite from 25,295 NOK/mnd —
**project management not included in any package**, licensed separately via Severa.

## 5. Visma eAccounting

**Lightweight dimension only**, no project management. Binary status (pågående or not).
Enabled on GL accounts 3000-8999. No dates/budget/PM/subprojects/tasks. **No time
tracking at all** (not on the features page). No planning. No budgets/WIP/invoicing
engine/a-konto — pure cost/revenue tagging. Severa's integration maps Severa projects
→ eAccounting cost units, confirming Visma pushes all PM work to Severa. Pricing:
"Prosjekt og avdeling" is **Smart-tier exclusive** (269-279 kr/mnd); no tier has time
tracking.

## 6. Visma Severa (PSA)

The only true PSA/consultancy tool in the Visma family; sold standalone and as the
"prosjektstyring" answer for both Visma.net and Business NXT.

1. **Entity**: customer/internal, business unit, cost center, deadline, project value
   (sales estimate). **Structure: Phases (+sub-phases) → project tasks.** Phases carry
   hour budgets, own members, own default work type, can be locked/marked complete, and
   **can be linked to each other (phase dependencies, Business tier)**. Project tasks are
   separate, kanban-managed, with types/statuses/deadlines(red if overdue)/recurrence.
   Fixed vs hourly expressed via pricing hierarchy (flat-rate phase → per-hour), not a flag.
2. **Time**: entry = customer+project+phase+work type. Work types flagged
   productive/non-productive but **billability overrides productivity**. Locking on
   phase/project. Approval: **Not reviewed → Approved**, dual approver roles (manager +
   project owner), strict (blocks invoicing) or advisory — **Business tier+**. Overtime
   is a manual flag, no NO-specific rules.
3. **Planning — strongest of all products surveyed**: **Gantt** (project plan + resource
   allocation drawn/edited on canvas), **Kanban** (project + task views), **phase
   dependencies**, resource allocation by hours or % capacity with overallocation warning,
   quick/auto allocation, availability filters, calendar+Google Drive sync (Business tier).
4. **Money**: percent-complete auto-calculated from actual/estimated hours (overridable).
   Fees (own work/products/subcontracting) + costs, each billable-now/later/invoiced/
   non-billable. **Milestone billing**: fees become billable on a scheduled date **or when
   linked phases complete**. **WIP is a headline KPI**: "value of hours minus invoiced
   amount", alongside Order book. Forecasting (revenue/billing/labour-expense/margin) from
   allocations; automatic revenue recognition at Platinum tier. No named a-konto — modelled
   as installments/scheduled invoicing instead (**UNCERTAIN** re: balance-sheet parking).
5. **Docs**: kanban collaboration tab, document storage (5GB→1TB by tier). Mobile app
   (all tiers) with receipt scanner (Business+). No customer portal found.
6. **Pricing** (€/user/mo): Basic 25 (PM, time, invoicing, mobile) → **Business 39**
   (approval, phase dependencies, resourcing/Gantt, task mgmt) → **Platinum 49** (revenue
   recognition, automated invoicing, advanced pricing) → Enterprise 69 (SSO, audit).
   **Approval, Gantt/resourcing and dependencies are NOT in the entry tier.**

## 7. 24SevenOffice / Finago (rebranded; time client = "Busy"/"Finago Busy")

1. **Entity**: number (optional series), name, dates, customer, work types, participants,
   rights-managed vs open. Status set post-creation (active/inactive gates
   time-entry/invoicing); sub-statuses for reporting only. **Sub-structure: tasks →
   subtasks (multi-level) → milestones** (milestones can't have subtasks). Projects can be
   flagged "task-driven" (oppgavestyrt) forcing a task on every time entry.
2. **Time**: customer+project+(work type OR task)+billable flag, mandatory on
   task-driven projects. Approval per-project toggle, dedicated Godkjenning tab. **Rate =
   work type × participant × project** (richest per-project rate card found). Time-bank
   auto-computes flex/overtime/absence against configured capacity. Mobile app.
3. **Planning**: **Gantt: yes** (Planlegging & Gantt tab, auto-populated from tasks).
   **Resource/capacity planning: yes** — Planleggeren with colour-coded availability,
   drag-and-drop, day/week views, calendar integration. Ressurser tab shows staff
   availability. **No kanban found** (table/list UI). Dependencies **not found**.
4. **Money**: budget at **GL-account level** (not just hours/amount). Central mechanism =
   **Fakturaplan** (dated invoice plans; multiple per project = de facto **a-konto/
   delfakturering** without using the term), mixing hours + costs + fixed-price product
   lines. **Viderefakturering** with configurable markup (% or fixed). Profitability
   report: effektiv timepris, faktureringsgrad, lønnsomhet. No named percent-complete/WIP.
5. **Docs**: Dokumenter tab (role-gated), E-post tab links sent/received mail. Partial
   external "customer portal": external contacts need a Community account + rights-managed
   project. 3 project roles (Prosjektleder/Deltaker/Observatør).
6. **Pricing**: project management **included in all Finago Office packages** (71-659+
   kr/mnd). **Finago Busy Pro 169 kr/user/mo** (time, flex, project planning, orders);
   **Busy Max 239 kr/user/mo** adds **budgeting + profitability reporting**.

## 8. Xledger

1. **Entity**: positions on hierarchies/multi-entity projects; "up to 12 levels /
   unlimited subprojects" claim is **UNCERTAIN** (unverified on primary pages). Confirmed:
   coding at **project, task and activity** levels; project coding is **mandatory** on
   transactions. Budgets settable per project, per phase, or per customer.
2. **Time**: logged against projects/activities/assignments/time codes, classed
   regular/overtime/billable. Mobile, calendar, browser, import; day/week/month entry.
   Approval by **project manager and line manager**, configurable workflow, mobile-capable.
   Rate resolution hierarchy **not publicly documented (UNCERTAIN)**.
3. **Planning**: **capacity/resource planning is a headline feature** — allocation,
   planned vs actual, utilisation vs plan, budget generated from the resource plan.
   **No Gantt found. No kanban. No dependencies/milestones found** — the mirror image of
   24SevenOffice.
4. **Money**: budgets per project/phase/customer, **original vs revised**, deviations
   surfaced live. **WIP explicitly named** — "project valuations (WIP)", postable at any
   time, auto-updated as invoices approve (only vendor here with WIP as a named feature).
   Invoicing: hours/travel/expenses/fixed price; **invoice at subproject, project, or
   customer-collection level**; EHF/eFaktura/email/post delivery. Revenue can be reported
   as actual-booked **or** imputed-earnings (accrual view). Percent-complete/a-konto
   mechanics **not publicly documented (UNCERTAIN)**.
5. **Docs**: mobile app for time/expense/approvals. No documents/checklists/portal found.
6. **Pricing**: no public list; charged by companies/entities + users + transactions; not
   discoverable at what tier projects unlock.

## 9. Uni Micro — Uni Economy (cloud) vs Uni Økonomi V3/Contracting (legacy, construction)

**Two very different stories** — do not conflate.

**Uni Economy (cloud)**: Project is one of 4 **dimension types** (Prosjekt/Avdeling/
Ansvar/Område), not a PM entity. Fields: number, name, description, customer, PM.
Statuses: Registrert/Tilbud/Pågående/Avsluttet/Deaktivert. **No sub-structure at all**
(no phases/tasks/milestones). Time: **timeart** (type: ordinær/betalt flex/fri med
lønn/fri/flex/overtid — no overtime *billing* rule) can link to a **product**, which
drives invoice price. Approval: team-based, auto-approved if no team or team-leader.
**No Gantt/resourcing/kanban/tasks/dependencies/milestones at all** — weakest planning
of all products surveyed; the "project planning tool" is really an overview screen
(revenue/cost/result widgets + order/quote/hours tabs). Invoicing: "Fakturering av
timer" wizard pulls hours booked to customer/order/**project**; **if hours are booked
to both an order and a project, only the order counts**; a project must have a customer
before its hours can be invoiced. No percent-complete. Pricing indicative/**UNCERTAIN**:
project tool ~59 kr/mnd, time ~326 kr/mnd + ~12 kr/employee.

**Uni Økonomi V3 / Contracting (legacy, construction-oriented)**: Project is an umbrella
over **prosjektordrer** (orders) — the real sub-structure. **Richest rate model found**:
hourly rate resolved **customer → project → order**, override chain, default rate
fallback, and fixed-price expressed as a **zero hourly rate**. Employee cost on project
explicitly includes **arbeidsgiveravgift + feriepenger** uplift (only vendor doing this).
Teaches the classic NS-style chain: **delfakturering, a-konto-fakturering,
sluttfakturering**; **innestående (retention)** tracked via a "Prosjektkontroll" widget
with contract value, unbilled revenue, **DB/DG%**, and user-set expected DG% for
variance — the only vendor with retention as a named, trackable field (not just a manual
invoice-line workaround).

## 10. Trade/installer & energy-sector tools

| Product | Vendor | Structure | Planning | Money/Norwegian specifics |
|---|---|---|---|---|
| **Ordrestyring** | Aceve | "ordrefil" (order-centric); sub-structure UNCERTAIN | Drag-and-drop calendar (no Gantt/kanban found); assigns by availability/competence | Budgets per project (arbeid/materialer/UE) vs actual with warnings; delfakturering+sluttfakturering; **Boligmappa integration** (FDV push); OS Pay for instant payment |
| **SpeedyCraft** (Devinco) | Devinco | arbeidsordre/serviceordre/serviceobjekter — order-centric, not WBS; help centre blocked fetch (403), most detail **UNCERTAIN** | Ressursplanlegger exists, form unknown; no Gantt/kanban documented | No invoicing (feeds ERP); materials/purchase-order/lagerstyring; **100% offline mobile** is headline differentiator |
| **Cordel** (SmartCraft) | SmartCraft | serviceordre (all tiers) vs **prosjektordre (PROFF/TOTAL only)**; **underjobber** + first-class **endringsmeldinger** (change orders) | Bemanningsplan (staffing) only; no Gantt/kanban/dependencies found | **Strongest Norwegian a-konto/NS support**: forskuddsvis/a konto/full grunnlag, invoicing on produserte mengder, **explicit "a-konto beholdning etter NS 8415/8417"**; live prosjektregnskap with forecast; EHF + eFaktura Privat; formal prosjektregnskap flagged for projects >300k kr |
| **Handyman** (GSGroup) | GSGroup | serviceordrer + oppgaver; installed-equipment hierarchy is on equipment, not project | **Calendar AND Gantt** in Ressursplanlegging; route optimisation w/ live GPS; no kanban/dependencies found | Per-line approval granularity (approve task or individual time/material lines); financial depth (budgets/WIP/a-konto) lives in connected ERP, not Handyman itself; 40+ ERP integrations |
| **Moment** (Milient Software) | Milient | prosjektfaser + aktiviteter; milestones, start/end dates, **dependencies** listed | **Gantt + timelines + dependencies**; resource/role-capacity planning (top tier only) | **Client rate vs internal cost rate** distinction; consumption-threshold notifications (30/60/90% of estimate); budgets = mid-tier feature, resource planning = top-tier feature; lists fixed/hourly/**a-konto-like retainer** invoicing (moderately confident) |

Other Norwegian tools found (shallower detail): **Svenn** (håndverker, ressursplanlegger,
HMS/KS, Sentral godkjenning help), **Steddy** (håndverker, endringsgodkjenning focus),
**SmartDok** (not a PM tool per se but the reference for **HMS-kort/mannskapsliste/RUH/
SJA** — geofenced e-mannskapsliste mirrored to HMSREG, satisfies byggherreforskriften),
**EG JobOffice** (ex-Holte, rør/VVS), **Drifti** (per-company pricing by headcount),
**Skyworker**, **MobileWorker** (Devinco), **Weld IT**. On the energy/utility side:
**Powel/Volue Infrastructure** — UtilityFlow (Dynamics 365-based, arbeidsordre from
field), Netbas Nettutvikling (project → planlegging → prosjektering →
materialbestilling → gjennomføring → sluttdokumentasjon, **lets external contractors
work directly in the tool**), and an **Entreprenørportal** (rare confirmed customer/
contractor portal in this whole survey). "Norda", "Ferro", "Elrond", "Contractor",
Mestergruppen-branded tools: **not found** as distinct Norwegian project products.

---

## Synthesis

**(a) Table stakes across nearly every Norwegian ERP project module**: project as a
named/coded entity linked to a customer; time logged against project (+ some
sub-dimension — activity/work type/task); billable vs non-billable; approval-before-
invoicing workflow of some kind; invoice generation from logged hours (T&M) and from
fixed price; basic hours/amount budget with actual-vs-budget reporting; supplier-cost
pass-through with markup; EHF invoice delivery. Mobile time entry is near-universal.

**(b) Only the specialised/PSA tools have**: Gantt charts (24SevenOffice, Severa,
Handyman, Moment — absent from Tripletex, PowerOffice, Xledger, Uni, Visma ERP);
resource/capacity planning as a dedicated visual tool (Severa, Xledger, 24SevenOffice,
Moment, Handyman — absent from Tripletex beyond a simple calendar, absent entirely from
PowerOffice/Uni/Visma Business/eAccounting); task **dependencies** (only clearly
confirmed in Severa's linked phases and claimed for Moment); **WIP as a named,
system-tracked figure** (Xledger, Severa — everyone else either has no concept or a
rough proxy like "ufakturerte timer"); percent-complete **billing automation** (Visma.net
ERP's 3 methods, Tripletex's optional mode, Severa's auto-calc — most others are
manual-only or absent); kanban boards (Severa only, confirmed); true customer/contractor
portals (essentially absent — only Powel's Entreprenørportal and 24SevenOffice's
limited "invite external participant" flow); compliance checklists/control forms tied
to invoicing gates (Tripletex's Kontrollskjema; HMS/KS is a whole separate tool category
in the trade segment — SmartDok, Svenn, Steddy, Devinco HMS/KS).

**(c) Dominant hierarchy model**: there is no single standard, but a clear axis from
"project = accounting dimension" to "project = full WBS":
- **Dimension only** (no sub-structure): Visma eAccounting, Uni Economy.
- **Project + one flat work-breakdown layer**: Tripletex (activities), PowerOffice
  (activities + 1-level subprojects), Xledger (activity, alongside task), Ordrestyring/
  SpeedyCraft/Handyman (order + tasks, order-centric).
- **Project + phases/tasks with milestones**: 24SevenOffice (tasks→subtasks→milestones),
  Visma.net ERP (tasks, invoicing rule per task), Uni Contracting (orders as sub-jobs,
  NS-style billing), Cordel (underjobber + change orders).
- **Full PM hierarchy with dependencies**: Severa (phases with sub-phases → tasks,
  linked/dependent phases) and, per marketing claims, Moment (phases → activities,
  milestones, dependencies).
Invoicing ties to this hierarchy in two recognisable ways: (1) **invoicing rule/rate
attached to the task/activity/phase** (Visma.net ERP per-task invoicing rules, Severa's
phase-level flat-rate pricing and "billable when phase completes", Tripletex's
per-activity rates), or (2) **dated invoice-plan/schedule detached from the WBS**
(Tripletex's Fakturaplan, 24SevenOffice's Fakturaplan, Cordel's fakturaplaner) that
sweeps up whatever hours/costs/fixed-price lines exist as of a date. The latter is more
common in the Norwegian SMB tier; the former is more common in PSA/ERP-project-ledger
tools (Severa, Visma.net ERP).

**(d) Norwegian-specific patterns**:
- **A-konto**: a real differentiator, not universal. Fully modelled with GL parking and
  auto zero-invoice only in **Tripletex**; explicitly **unsupported** in PowerOffice Go
  (manual product-line workaround) and absent in Visma eAccounting/Business; Uni
  Contracting and **Cordel** are the only ones that explicitly cite the **NS 8415/8417**
  (sub-contract) billing chain by name; Visma.net ERP's "progress invoicing" is the
  closest ERP-ledger analogue without using the term.
- **Innestående (retention/holdback)**: only Uni Contracting tracks it as a system field
  (DB/DG%); Tripletex documents it only as a fully manual invoice-line workaround; no
  other product mentions it.
- **NS 8405/8406/8407** (byggekontrakt milestones) by name: **not found on any general
  ERP vendor's pages**; Cordel cites the underentreprise siblings NS 8415/8417 instead.
  This whole area (sluttoppgjør deadlines, all-claims-or-lost rules, formal
  avdragsoversikt) appears to be an unserved gap in SMB-tier software — likely still
  handled in spreadsheets even at firms using Tripletex/Cordel.
- **Overtidsregler**: nobody automates Norwegian overtime multipliers end-to-end.
  Tripletex requires a manual duplicate-activity-at-150% to bill overtime; Uni Economy
  encodes flex/overtime as timeart *types* but not billing rules; PowerOffice tracks
  fleksitid/avspasering but not billing; Severa/Xledger just flag overtime hours.
- **Boligmappa**: confirmed only for **Tripletex** (Elektro/VVS tier) and **Ordrestyring**
  (FDV push on completion); not found elsewhere (absence of evidence, not proof of absence).
- **EHF**: treated as table stakes/assumed universal; explicitly confirmed for Xledger,
  Cordel (+ eFaktura Privat for consumers), Tripletex (with an auto-link-incoming-invoice-
  to-project feature), Severa (Basic tier), eAccounting (billed per unit).
- **HMS-kort/mannskapsliste/byggherreforskriften/RUH/SJA**: owned by a distinct tool
  category (SmartDok, Devinco SiteMonitor) rather than by any general ERP or PM product —
  worth treating as a separate compliance layer, not a "projects module" feature, if
  Vantigo ever targets byggebransjen directly.

## Sources

Domains/help-centre sections actually read across three research passes (article-level
URLs omitted here for length; ~150 pages fetched in total):

- **Tripletex**: hjelp.tripletex.no (~40 articles: project setup, budget, activities,
  time entry/approval, rate models, resource/capacity planning, a-konto, invoicing,
  forecast, control forms, Boligmappa, overtime, pass-through, retainage);
  tripletex.no/{priser, prosjekt-og-timeforing, funksjoner/prosjektstyring}.
- **PowerOffice Go**: hjelpesenter.poweroffice.no (~13 articles: project/subproject
  setup, budget, reports, dashboard, pass-through, time entry/approval/rate hierarchy,
  unsupported invoice types); poweroffice.no/{priser, utvidelser/moment}.
- **Visma.net/Business NXT/eAccounting/Severa**: docs.vismasoftware.no/visma-net-erp
  (project accounting docs); doc.visma.net/userdoc/businessnxt; vismasoftware.no/erp/
  {visma-net,business-nxt}; eaccounting.no/{funksjoner,priser}; eaccountingapi.
  vismaonline.com (401 probe); support.severa.com (~15 solution articles); severa.com/
  {features,pricing}. Unreachable: help.visma.net, help.visma.com, severa.visma.com,
  community.visma.com, hjelp.eaccounting.no.
- **24SevenOffice/Finago**: support.24sevenoffice.com/no-no (~12 articles); finago.no/
  {priser, produkter/timeregistrering}.
- **Xledger**: xledger.com/no/{erp-system/prosjekt, erp-system/prosjektregnskap,
  bransjer/prosjekt, pris}; xledger.com/solutions/{project-accounting,timesheets,
  time-and-expense-tracking}; viewgroup.no/xledger-moduler-og-funksjoner.
- **Uni Micro**: hjelp.unimicro.no (project/time/dimension articles);
  support.unimicro.no/kundestotte/contracting (setup, reports, course material).
- **Ordrestyring/Aceve**: aceve.com/no/{produkter/ordrestyring, funksjoner/*}; drifti.no
  comparison articles.
- **SpeedyCraft/Devinco**: devinco.com (help centre 403'd — marketing pages only).
- **Cordel/SmartCraft**: cordel.no/{funksjoner/*, faq, produkter/cordel-*};
  smartcraft.com/solutions/hvac-plumbing/cordel.
- **Handyman/GSGroup**: handyman.gsgroup.no (product, office, mobile, el-installasjon).
- **Moment/Milient**: poweroffice.no/utvidelser/moment; milientsoftware.com/{product,
  pricing}/project-management and resource-management; mynewsdesk.com press release.
- **Other trade tools**: svenn.com/prosjektstyring; steddy.no/s/prosjektstyringsverktoy;
  smartdok.no.
- **Energy/utility**: volueinfrastructure.com (Netbas, saksbehandling, ressursoptimalisering,
  entreprenørportal); mynewsdesk.com Powel press releases.
- **NS-standard background**: ns8407.com, codex.no, innbyggerkontakt.no.
