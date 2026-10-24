# Staff phase 2 — what competence means here, and the decisions (2026-10-24/25)

The Staff research of 2026-10-11 (`2026-10-11-staff-module.md`, "R") proposed a
competence model in its §6.5 and the owner decided (R §10.4) that *what competence
must mean for this company is discussed before the phase is specified*. This note is
that discussion, held 2026-10-24 and 2026-10-25, and the decisions it ended in. The
design spec `docs/superpowers/specs/2026-10-25-staff-competence-design.md` starts from
these and does not reopen them. Code references are against `main` at `65a369c6`.

## 1. Where the code stood

Phase 1 (A1, A2, B, C) is merged: the register, dated terms, documents, absence and the
account flag. What phase 2 hangs on already exists:

- the document archive has a seeded `certificate` category in retention class D, which
  can hold a competence's evidence (`mig/00043`, reference "Categories and classes");
- the employment terms carry the § 14-6 p `competence_development_text`;
- `staff.person_roles` already holds verneombud, AMU member and HMS responsible with
  elected dates (`mig/00042:174`), so the statutory role flags R §4.10 asked for need
  nothing more;
- the one manager rule (the terms' `manager_person_id`, else the department's) gives a
  team lead their reports' records with no permission, and `staff:manage` gives HR, or
  the CEO of a small firm, everyone's — the shape phase 2 reuses for every audience;
- the dashboard's attention items (`attention.go`), the daily workers
  (`staff-retention`, `staff-accounts`) and the absence mails (`notifications.go`) are
  the three patterns the expiry reminders follow;
- a module answering a read on a person's behalf applies its own visibility to the
  principal in the context and answers nothing, not an error, when the caller lacks it
  (module-boundaries rule 12) — the rule the new read contract is bound by.

What does **not** exist yet, and bounds what a requirement can target: Projects' roles
are permission sets (`manager`, `member`, `viewer`), not functional roles; Projects
phase 5's placeholders and Staffing's shift templates are unbuilt. Staff depends on
nobody but identity, and a preference naming a customer would be its first read of
another business module.

## 2. The two parties, and how each uses it

The owner's framing (2026-10-24): tenants come in many shapes — a manager works with
their reports' competence, and so does a CEO who is HR and everyone's manager — and
**Min side** (`/staff/me`) is where the signed-in person works with their own, while the
manager uses the same material in the regular one-to-one conversation.

**The person, on My page.** A Competence tab beside Absence, phone first, in two halves:
*what I can do* — competences, verified ones with their evidence and self-declared ones
with a *declared* badge, **Add competence** from the catalogue with a level and a
photographed certificate that lands in Documents under `certificate`, and **Renewed**
on an expiring certificate, which adds a new row and keeps the old as history — and
*what I want* — interests (a competence the person wants to use more, or to learn),
weighted preferences, and the availability declaration. Only the person writes the
second half; the manager reads it and never edits it, because it is the person's voice.

**The manager, in the one-to-one.** The report's person page renders the same tab with
the manager's capabilities, and the conversation has four moves: **confirm** (verify a
declared row, with the level agreed and a short note of what it rests on — evidence or
seen in the work), **look ahead** (certificates expiring, and the profile's gaps once
profiles exist), **hear the wishes** (interests, preferences and availability as
written, with a **Seen** stamp so the person knows they were read), and **agree**
(development goals — deferred, decision 13). A thin **review stamp** records that the
two went through the tab together on a date, drives "last reviewed" and an overdue
item under an interval setting, and is what a later medarbeidersamtale module grows
from.

Nothing in the tab takes a free-text reason: a reason for "no night shifts" drifts
into health within a sentence. A verification note is short and about evidence.

**The overview.** My team gains cards for declarations awaiting the manager,
certificates expiring among their reports and reviews overdue. A **Competence** area
answers the two business questions for `staff:view` and `staff:manage`: the matrix —
who holds what, at least which level, valid on a date — and who wants to learn what,
which is the gap-to-train decision.

## 3. Decisions taken by the owner

Items 1–12 were put to the owner on 2026-10-24 with a recommendation each and accepted
as recommended ("I like this approach", with the additions in §2); items 13–16 were
answered on 2026-10-25.

1. **Two kinds in one flat catalogue.** A competence is a `skill` (a proficiency with a
   level, no expiry) or a `certificate` (issued by someone, with evidence, a validity
   and legal weight, asked only "valid on this date"). Categories are flat. The
   Norwegian certificates of R §4.11 are seeded and can be hidden, the way absence
   types are seeded and editable.
2. **One level scale**, 0–4 with the Norwegian labels of R §6.5 (kjenner til,
   grunnleggende, selvstendig, erfaren, ekspert), switched on per competence. No CEFR
   scale for languages in this phase.
3. **Declared versus verified.** The person declares from My page; the person's manager
   by the rule, `staff:approve` or `staff:manage` verifies, never oneself. A
   certificate counts only when verified; a skill counts as self-declared, with the
   badge visible wherever it is shown.
4. **Renewal keeps history.** A renewed certificate is a new row with its own evidence,
   superseding the old, never an edit of `valid_to`. The catalogue type carries a
   validity in months so the new row's `valid_to` is proposed from its issue date.
5. **Statutory role flags are out of this phase** — they exist already (§1).
6. **Colleagues see competences**: `staff:view` reads names, levels, validity and the
   verified flag. Evidence documents and verification notes are the person's, the
   manager's, `staff:manage`'s and `staff:personal-data`'s.
7. **Preferences are the person's and the manager's.** Written by the person alone on My
   page, read by the person's manager and `staff:manage`, never by colleagues; no
   free-text reason anywhere.
8. **Preference kinds in this phase** reference Staff's own entities or nothing:
   weekdays, time of day, location (Staff's locations), travel, remote, maximum
   consecutive days, maximum hours per week. Customer and project wishes and "do not
   schedule with" wait for Staffing, which reads both sides.
9. **Requirements live in Staff as competence profiles**: a named list of competence,
   minimum level and must-be-valid, assigned to people or defaulted by department,
   giving the gap list and the attention item without any other module. Projects and
   Staffing attach requirements on their own side through the read contract.
10. **Availability declarations ship minimal, as data only**, in the last delivery;
    hard versus soft is Staffing's spec's.
11. **Reminders**: an expiring certificate is a dashboard attention item and a mail to
    the person and their manager at the type's lead time (30 days for a yearly
    certificate, six months for a five-year one). Accepted as recommended, including
    that the person is mailed.
12. **No new permission.** `staff:manage` keeps the catalogue and the profiles,
    `staff:approve` gains verification, the self-service link covers My page.
13. **Development goals: later** — with the medarbeidersamtale module. The review
    stamp ships without goals.
14. **The review stamp: yes**, with the interval setting and the overdue item.
15. **Interests and preferences are read by the manager and `staff:manage`, written by
    the person alone** — confirmed.
16. **The matrix shows self-declared rows too**, with the declared badge; a
    verified-only filter is offered.

## 4. Consequences the spec takes from these

- **The person uploads evidence** — new for Staff, where only `staff:manage` and
  `staff:personal-data` file documents today (`documents.go:24`). The upload is allowed
  through the competence endpoint alone, files under `certificate`, and the person
  cannot delete it; a renewal supersedes it.
- **The read contract answers facts about competence under the Colleague rule and a
  fit, never the wishes, about preferences** (rule 12): a planner in Staffing with
  `staff:view` learns who holds what; what a person wants is scored by Staff into a
  number for a proposed booking, so the matcher weighs wishes nobody but the manager
  reads. The scoring is specified with Staffing; this phase defines the shape.
- **Three deliveries, three pull requests, one migration each**, opened against `main`
  one at a time: A the catalogue, person competences, evidence, verification, the
  expiry worker and the contract's competence reads; B the one-to-one — interests,
  preferences, availability, the Seen stamps, the review stamp, the My team cards; C
  profiles, gaps, the matrix, the interests overview and the matching contract written
  into the reference.
