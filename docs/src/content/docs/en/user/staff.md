---
title: Staff
description: The people register — People, a person's page with the person's photo, the at-a-glance cards, its documents, their competences, what their profile asks and their account, the legal hold, an erased record and its rehire, New person, My page, the Absence calendar and approvals, the Competence page with its matrix and the interests, absence and the holiday account, and the Groups and their place in the directory, and the Staff settings with the company's structure, the document categories, the competence catalogue and its profiles, the register's import and export, the payroll export, retention, the thresholds and the accounts.
sidebar:
  order: 45
sources:
  - apps/staff/frontend
  - apps/host/frontend/src/routes/staff
  - apps/host/frontend/src/catalogs/dashboard.ts
  - apps/host/frontend/src/lib/dashboard-helpers.ts
---

The Staff app is where HR keeps the company's people: who they are, how to reach them,
their employments with the terms the contract states, and the company's structure the
employments sit in. The rules behind every screen — who reads which field, what a
refusal means — are in the [Staff module reference](/en/reference/staff/); this page is
about what you do on each screen.

## Who sees what

The Staff tile is shown to everyone with `staff:access`; the app opens on People for
those who see it and on My page for everyone else. The sidebar shows the areas your
permissions give you:

| Area in the sidebar | Who sees it |
| --- | --- |
| **My page** | everyone with `staff:access` |
| **Staff directory** (the People screen) | `staff:view`, or `staff:manage` — an HR role without `staff:view` still lists people and reads the structure |
| **Absence** (the calendar and the approvals) | `staff:view`, `staff:approve`, `staff:manage` or `staff:personal-data`; a manager without those opens it from the dashboard's link, and decides on [My team](#my-team) |
| **Competence** (the matrix and, for HR, the interests) | `staff:view` or `staff:manage` |
| **Groups** | `staff:manage` |
| **Staff settings** | `staff:manage` |

What a screen offers follows what the server says you may do: the **New person**
button, the **Status** filter and the status column are there for `staff:manage` only.
The full table is under [Permissions](/en/reference/staff/#permissions).

## Find someone in People

**People** (sidebar: **Staff directory**) lists the company's people by name, with their employee number, title, department,
manager and work email. The title and the department are the ones in the terms in force
today, and the manager is the one who manages the person today: the one the terms name,
or, when they name none or the one named has left, the department's manager or the next
one up who is still employed; a dash means there is none.

- **Search** matches every word you type against the names, the employee number and
  the work email. The list follows a moment after you stop typing.
- **Business unit**, **Department** and **Location** narrow the list to the people
  whose employment today — or, for someone starting later, their first terms — sits
  there. Archived rows are offered too, marked "(archived)", so you can still find who
  was in them.
- Every filter is in the page's address, so a refreshed page or a pasted link shows
  the same list.

A colleague holding `staff:view` sees the people **active today** only: someone who
has not started yet, and someone who has left, are not in their list.

**On leave today** beside a name means an approved or registered absence covers today
— ferie, a leave, a sick day alike. Everyone who sees the list sees the same badge, and
it never says which kind of absence it is; a request still waiting, and a doctor's
visit of a few hours, do not count.

### Status, for HR

With `staff:manage` you also see the **Status** control and a status column:

- **Current** (the default) lists those active today, those starting later and those
  with **No employment** yet — the person saved in the first step of
  [New person](#add-a-new-person) whose employment is still to come.
- **Ended** lists those whose every employment ended before today.
- **All** lists everyone.

The column says **Active**, **Starts on** with the first day, **Ended on** with the
last day worked, or **No employment**. A former employee whose record the retention run
has erased is listed under **Ended** and **All** with **Erased** beside the status — see
[An erased record](#an-erased-record). "Today" is the business time zone's day
(see [Change the settings](#change-the-settings)), so the list turns over at midnight
there, not in your browser.

Press a name to open [the person's page](#a-persons-page).

## A person's page

A person's page has seven tabs — **Profile**, **Employments**, **Absence**, **Competence**, **History**,
**Documents** and **Account** — and the tab you are on is in the page's address (`?tab=account` opens the
Account tab, which is where the dashboard's account items lead). It shows what you may see of the person and
nothing else: a group of fields that is not yours to read is not on the page at all,
not even as an empty heading. The person themselves, a manager of theirs, a colleague
holding `staff:view` and HR each see a different page; the full table is under
[field groups and audiences](/en/reference/staff/#field-groups-and-audiences). A person
you may not see answers exactly as one who does not exist.

At the top, beside the name, is the person's **photo** — or their initials when there is
none — with their title and department under the name. Everyone who may open the page
sees the photo, colleagues included. The photo HR sets here becomes the person's picture
everywhere: in Vantigo — where the person can no longer pick their own while it stands —
and, when they have a Microsoft Entra ID account, in Entra ID too. When HR has set none,
the page shows the person's own Vantigo picture, the one they chose themselves. HR
(`staff:manage`) and `staff:personal-data` can change it:

1. Press the photo (the button **Photo of** and the person's name).
2. Choose **Change photo** and pick a JPEG or PNG — on a phone, take one with the camera
   or choose one from the library. The new photo shows at once.
3. Or choose **Remove photo** and confirm: the person's own picture comes back, or
   their initials when they have none. **Remove photo** is not offered while the
   picture shown is the person's own: that one is theirs to change.

A photo is at most 5 MiB and 4096 pixels on each side; any other file is refused with
the reason. Where a phone photo was taken, and with which camera, is removed
before it is stored. The person, a manager, `staff:approve` and a colleague see the
photo and have no menu. The photo reaches Entra ID at the account's next run, within
the hour by default. The person's
history says that a photo was set or removed, never the picture.

### Profile

The tab opens on [At a glance](#at-a-glance), then the person's groups:

- **Work** — the photo, the names, the employee number, the work email and phone, the title, the
  department, the business unit, the location and the manager in force today, the
  language and the work country. Everyone who may open the page sees it.
- **Private** — the birth date, gender, mobile and private phone, private email and home
  address. The person and HR.
- **HR's notes** — HR alone (`staff:manage`, `staff:personal-data`).
- **Identifiers** — the national identity number as **On file** or **Not on file**, with
  its kind, and the bank account as **Ending in** and its last four digits. The page
  never shows more than that, except through **Reveal** below.
- **Next of kin** and **Roles** (Safety representative, AMU member, HSE responsible,
  each with the days they hold it).
- **Self-service** — the user the person signs in as, if HR has linked one, or — when
  Staff's accounts manage the person's user — *… — managed by Staff; see the Account
  tab*, with nothing to link or unlink. The person's account itself is on the
  [Account](#account) tab.
- **Children and care days** — **Children under 12**, **Sole carer** and **Children
  with extra care days**, what the person's care days are counted from. The person, HR
  and `staff:personal-data`, and only when one of them is recorded.
- **Organisation** — **Managers** up the chain from the person's manager today, and
  the **Direct reports**. Press a name to open their page.

A manager sees the work group, the roles and the organisation of the people they
manage; a colleague the same of anyone active today. A manager who has left manages
nobody: their people go to the department's manager, or the next one up.

#### At a glance

At the top of the Profile tab is a row of small cards — what you would otherwise look
for across the tabs before talking to someone. Each is a figure with a line under it,
and a card's label opens the tab it sums up (Absence, Employments or Documents). You
see a card only when you may read what it shows: a card that is not yours is not there
at all, never a zero.

- **Sick days** — the sick days this year, and how many of them were egenmelding and
  how many sykmelding; a part-time sykmelding counts by its degree. The person, their
  manager, `staff:approve` and HR.
- **Egenmelding** — how much of the egenmelding policy the person has used in the last
  12 months: *2 of 4 times*, or *6 of 24 days*. The same readers.
- **Holiday** — the holiday days left this year, with what was taken so far and what is
  planned ahead, requests still waiting included; in red, as the days overdrawn, when
  the account is overdrawn. Approved holiday ahead is already gone from what is left,
  as on the Absence tab, so the three do not add up to the year's days. The same
  readers.
- **Sick child** — the days at home with a sick child this year. You see the count as
  the person's manager or `staff:approve`; the person and HR also see it against their
  care days (*of 10 this year*).
- **Position** — the FTE, the title, since when and for how long the person has been
  employed, and, while it lasts, when the probation ends. Everyone who may open the page
  sees the first three; the probation the person, their manager, `staff:approve` and HR.
- **Absence** — what the person is away on today (*Away*), the next absence ahead
  (*Next*), and a sick-leave case open since when, with its day; *Nothing now or
  planned* when there is none. A colleague sees *Away* and *Next* as an absence, never
  which kind, and only once it is approved or registered; a manager sees the type, but
  an HR-only type reads as leave. The open case is for the person, their manager,
  `staff:approve` and HR.
- **Contract** — *Signed*, or *Not signed* in red, for the employment in force today;
  *No employment* when none is. It opens the Documents tab. HR (`staff:manage`) only.
- **Hours this week** — the hours logged in the Time app this week, every project
  together, against the hours the contract expects (*31.5 of 37.5 h*), with how many are
  approved. The expected week is the full-time hours times the FTE, added up over every
  employment the person is in today, not reduced for absence or public holidays — the Absence card says why a week is short. *Time
  tracking is off* when the Time app is not in use, and *No user linked* when the person
  has no user to log hours with. The person, their manager, `staff:manage` and
  `staff:personal-data` — not `staff:approve`, and not a colleague. You need no
  permission in the Time app to see it.

#### Change the details (HR)

1. Press **Edit details** in the Work group. The form holds the work group, the private
   group and HR's notes. The employee number is shown but never changes.
2. Change what you need and press **Save**. A refusal lands on its field — a work email
   another person has, for one.

The form also holds the three **care-day fields** under **Children and care days**:
**Children under 12** (without NAV's extra-days decision), **Sole carer** and
**Children with extra care days** (with it) — what the care days for a sick child are
counted from ([the care-day fields](/en/admin/staff/#the-care-day-fields)). A field left
empty is not recorded; one you do not touch is saved as it was. The person, HR and
`staff:personal-data` read them; a manager and a colleague never do, and the person's
own **Edit my details** never changes them.

If somebody else saved the person since you opened the form, the form closes, the page
reloads and says *Somebody else saved this; it has been reloaded*. Make your change
again on what is there now.

#### Next of kin and roles (HR)

- **Edit next of kin** opens the list: up to three, each with a **Name** and a
  **Relationship** (both required), a **Phone** and a **Note**. **Add next of kin**
  adds one, the bin removes one, and **Save** replaces the whole list.
- **Edit roles** works the same way: **Add role**, choose the **Role**, and give
  **From** and **To** if you know them (the end not before the start).

#### Reveal or set the national identity number and the bank account

With `staff:personal-data`, the Identifiers group offers **Reveal** and **Set**. Held
alone, `staff:personal-data` shows every group of the person but changes nothing else:
the edit buttons are `staff:manage`'s.

- **Reveal** asks first, then shows the number for 30 seconds with a countdown
  (*Hidden in … s*); **Hide** hides it sooner. Each reveal is recorded in the person's
  history with your name. The number is not kept by the page or the browser.
- **Set** asks for the **Kind** — fødselsnummer, d-nummer or a foreign identity number
  with its **Country** — and the **Number**, or for the bank account a Norwegian
  account number or an IBAN. A national identity number that encodes another birth date
  than the person's is refused on the number: one of the two is wrong, so correct
  whichever it is. Setting a bank account mails the person, naming its last four
  digits.

`staff:manage` alone sees **On file** and the last four digits and nothing more. See
[the sealed identifiers](/en/reference/staff/#the-sealed-identifiers).

#### Link the person to their user (HR)

The **Self-service** group links the person to the user they sign in as, so they see
their own record on [My page](#my-page). Type a name in **User**, choose the user and
press **Link**. The page then says *… — linked, not managed by Staff*: the link gives
the user My page and nothing more, grants no permission, and Staff never changes the
user's account. A disabled user, or one already linked to another person, is refused
on the field. **Unlink** asks first, then removes the link; the user's account stays as
it is.

A user Staff's accounts manage is never linked or unlinked here: the page says *This
user's account is managed by Staff's accounts, so it is not linked or unlinked by hand.
Use the Account tab.* When Staff cannot ask identity whether a user is managed, the
page says *Identity could not be reached, or no module provides accounts* and nothing
is changed; try again.

### Employments

Each employment is a card with its start, its end and the reason, the business unit,
the kind of employment relationship, the day the contract was signed, and the days the
percentage (**Percentage in force since**) and the pay (**Pay in force since**) took
effect, with a status badge. Under it, the **Terms** as a timeline, newest first, the
row in force today marked **In force**. Each row shows its fields as you may read them:
the person and HR see every field and the **Pay components**; a manager every field
but the pay; a colleague only the days, the employment type and the percentage.

With `staff:manage` the tab and the cards offer:

- **New employment** (above the cards) — an employment with its first terms, in the
  form of [New person's second step](#step-2-the-first-employment): a second position,
  a leaver hired again, or the first employment you left for later. Its number is the
  employee number and the next free `-n` unless you assign your own. On an erased
  record the button is **Rehire** — see [Rehire](#rehire).
- **New terms** — a new row from the day it **Takes effect**, prefilled from the row in
  force today. Change what changes and press **Save terms**; a field you empty is
  cleared from that day. **More terms** holds the rest of § 14-6 and the pay: **Add pay
  component** (a name, an amount, a currency and a period, at most twenty), the
  **Seniority date**, the **Pay grade**, and — once a probation is set — whether an
  extension of it is agreed in writing, to which day and why.
- **Correct** on any row — rewrites the row as it was really agreed; the history
  records what changed. The first row takes effect on the start: to move it, change the
  start under **Edit employment**.
- **Delete** on the latest row only, never the first, after asking. The row before it
  is in force again.
- **Edit employment** — the **Start date** (the first terms move with it), the
  **Business unit**, the kind of relationship, **Contract signed** and the **Holiday
  scheme** ([holiday schemes](/en/admin/staff/#holiday-schemes)). The employment number
  never changes.
- **End employment** — the **Last day worked**, today in the business time zone unless
  you change it and allowed in the future, and the **End reason**, one of the
  a-melding's six; the form asks for the reason before it sends anything. **Reopen**
  takes an end recorded in error away again.

A refusal lands on the field it is about: a day another row already has on **Takes
effect**, a manager who would end up managing themselves on **Manager**, a probation
that is too long on **Probation ends**, hours that do not fit the scheme on the hours,
and terms dated after the end on **Last day worked**.

#### The contract check

The **Contract check** lists the seventeen items aml. § 14-6 asks a written contract to
hold, a) to q), each **Present**, **Missing** or **Not applicable** — judged on the
terms in force today — and says whether a signed contract is recorded. It informs; it
never stops a save. The person, a manager of theirs and HR see it; a manager, who does
not see the pay, sees it without item i), the pay. See
[the § 14-6 check](/en/reference/staff/#the--14-6-check) for where each item is read from.

### Absence

**Absence** is the person's absence as you may see it: the person, a manager of
theirs, whoever decides everyone's absence (`staff:approve`) and HR. Anyone else does not
see the tab. From top to bottom:

- **The holiday account** for a year — entitlement, carried in, taken, requested and
  remaining — with **Previous year** and **Next year** to page through the years.
- **Holiday adjustments** (HR): the carry-overs and corrections, newest year first.
  **Add adjustment** records one for the year shown — a **Correction** (with a **Note**
  saying why), or an **Agreed** or **Forced carry-over** from an earlier year — and the
  bin deletes one after asking. **Carry over from** *year* (HR) carries the year's
  untaken holiday into the next: choose the **Kind** — *Agreed*, within the holiday
  scheme's maximum, or *Forced*, beyond it with a note — leave **Days** empty for what
  remains, and press **Carry over**. A second carry-over from the same year is refused:
  *A carry-over from that year is already recorded*.
- **Egenmelding** — the person's standing under the company's policy. HR has **Withdraw
  egenmelding**: choose **Until** — a day after today, at most a year ahead — give the
  **Reason** and press **Withdraw**. **Lift the withdrawal** ends it early. A withdrawal
  is shown only to the person, HR and `staff:personal-data`.
- **Care days** for this year (the year buttons do not change it), to the person, HR and `staff:personal-data`.
- **Sick-leave case from** *date* — the open case: its last day so far, the 52-week end,
  the milestones and the accommodation note. A manager, an Approver and HR **Mark done**
  and **Waive** a milestone; the manager and HR **Change the accommodation note** (an
  Approver is not shown it). HR also has **Change the estimate** — or **Reset to first
  day + 364** — and **Close the case**. A milestone's note, and why one was waived, are
  shown only to the person and `staff:personal-data`, who has **Change the note** on each milestone.
- **Absences** — the person's rows that touch the year shown, as you may see them, a
  page at a time when there are many. A type that reveals health or
  family matters shows a manager and HR without `staff:personal-data` as an approved
  *Leave*, with nothing to change. HR has **Register absence** — a sykmelding the
  person brings, say — and on a row:
  - **Change** on a request or a registration (a sykmelding that ended early),
  - **Correct** on an approved absence whose days or type were wrong — a type that
    reveals health or family matters is never corrected into one that does not, or
    the other way round: cancel the absence and enter the other,
  - **Cancel absence** on an approved absence (also for the one who decides it) or a
    request, after asking,
  - **Postpone holiday** on a registered 100 % sykmelding: the employee's demand to postpone
    approved ferie under ferieloven § 9. Set **Demanded on** — never after today, never
    before the sykmelding began — and press **Record the demand**; the ferie it lies over
    is split and its days return to the account. A demand made after the last working
    day before the holiday, for sickness that began before it, is refused in words.

A write sends what the row carries to guard it: if somebody changed the row meanwhile,
the form closes, the tab reloads and says so — make your change again on what is there
now. A row that has gone meanwhile says *This is no longer there.*

### Competence

**Competence** is what the person can do: skills with a level, from *aware* to
*expert*, and certificates with a validity. Everyone who sees the person sees the tab,
grouped by category, but not the same tab:

- **A colleague** with `staff:view` sees each competence's name, its **Level**,
  whether it is **Declared** or **Verified**, and how long it is valid — *Valid to* a
  date with the days left, or *Valid without end* — with **Expiring** or **Expired**
  when it is. Nothing more, and no buttons.
- **The person's manager, whoever holds `staff:approve`, and HR** also see each
  row's detail: **Issued**, **Issuer**, **Reference**, **Entered by** (*The person*,
  *A manager* or *HR*), *Verified by* a name on a date and what the verification rests
  on, and the **Evidence** — the photo or scan of the certificate — to download.

A competence the person added themselves carries the **Declared** badge until
somebody verifies it, and the dashboard lists it for the person's manager under
*… declared a competence awaiting verification*.

**What their profile asks.** At the top of the tab, **What their profile asks** lists
each competence profile that applies to the person — what their position needs, as HR
describes it under [Competence profiles](#competence-profiles) — with *Assigned* under
its name when HR assigned it to them, and *Default for* a department when it is the
default of the department their terms place them in today; a profile can be both. Each
profile carries *2 not met*, or *All met*, and a row for each competence it asks: what
it **Asks** — *At least* a level, *Valid* for a certificate, or *Held* when holding it
is enough — its **Status**, and what the person **Holds**: their level and the day it
is valid to. The statuses, and what you do about each:

- **Met** — the person holds it as the profile asks. With the **Declared** badge
  beside it, they declared it themselves and nobody has verified it yet: it counts, and
  you **Verify** it as any declared competence.
- **Expires soon** — met, but the certificate is within its reminder of its end. It
  still counts; press **Renewed** when the new certificate is there.
- **Declared, not verified** — a certificate the person declared themselves. It does
  not count until somebody verifies it: look at the evidence and **Verify** it.
- **Below the level** — the person holds the competence below the level the profile
  asks. Agree on how they get there; when they have, **Edit** or **Verify** it at the
  new level.
- **Expired** — the certificate's last valid day has passed. Renew it and press
  **Renewed**.
- **Missing** — the person has no current row of it, or the certificate is not valid
  yet. Add it with **Add competence** if you have seen it in the work, or plan how the
  person gets it.

Every status but **Met** and **Expires soon** is not met. A competence archived since
the profile was written stays on it, marked *(archived)*, and still counts; a profile HR
has archived asks nothing and is not shown. When no profile applies, the tab says *No
profile asks anything of* the person. The items not met also reach the dashboard of the
person's manager and HR — *… does not meet their competence profile* — and the
manager's My team, under **Profiles not met**.

HR has **Assign profiles** above the list: choose the profiles that apply to the person,
at most 20, and press **Save profiles** — the profiles chosen replace those assigned
before. It offers the profiles that are not archived, and keeps one archived since
until you take it off. A department's default is not assigned here: it applies by
itself, and is set on the department under [The structure](#the-structure). The
person, their manager, whoever holds `staff:approve`, `staff:personal-data` and HR see
**What their profile asks**; colleagues do not. The rules are under
[profiles and gaps](/en/reference/staff/#profiles-and-gaps).

**In the one-to-one.** Open your report's page on **Competence** and go through it
together:

- **Verify** confirms a declared competence. Choose the **Level** the two of you agree
  on — it may be lower or higher than the one the person chose — and what it is
  **Verified on the basis of**: *The evidence* (you saw the certificate) or *Seen in
  the work*. A short **Note** about what it rests on is optional; keep it to the
  evidence. The row then reads *Verified by* you. Nobody verifies their own
  competences — not with `staff:approve`, not as HR: someone else does it. If you do
  not agree with a declaration, **Remove** it and say why in the conversation; there
  is no "rejected" state.
- **Add competence** enters one you have seen in the work. It is verified at once, in
  your name.
- **Edit** corrects a row's level, dates, issuer or reference. If you change a verified
  row's level, the row reads *Verified by* you from then on.
- **Renewed** records a renewed certificate: the new **Issued** date, the **Valid to**
  date — proposed from the issue date when the certificate has a fixed validity — the
  **Issuer** and the **Reference**; the person or HR adds the new certificate's evidence.
  The old row is kept as history
  and the new one is current; renewed by you, it is verified at once. A skill has no
  validity, and is not renewed: edit its level instead.
- **Remove** takes a competence off the list, after asking. On a renewed certificate
  it takes the renewal back: the earlier row is current again. HR may tick **Remove
  the whole history** to remove every row of that competence at once.
- **Add evidence** files a photo or a scan on a row that has none; it is the person's
  and HR's to do.
- **Show history** lists the renewed rows too, marked *Renewed since*.

A certificate within its reminder of its end shows **Expiring** with the days left,
and **Expired** once the day has passed; both are on your dashboard, and you and the
person get an email. When the new certificate is there, press **Renewed** on the row.

**What they want.** Under the competences, **What they want** shows what the person
wrote for the one-to-one, as they wrote it: their **Interests** — each competence
marked *Wants to learn*, or *Wants to use more* when they already hold it — their
**Preferences** — how they would rather work, each with how much it matters: *Nice to
have*, *Matters* or *Important* — and their **Availability**, the days and hours they
can, cannot or would rather work. It is the person's voice: there is nothing for you to
edit, and nothing in it asks for a reason — if a preference needs one, it is said in the
conversation. A row the person added or changed since it was last seen carries **New**.
When you have read it, press **Mark as seen**: every such row, and every competence on
the tab not yet seen, then reads *Seen* with the day — for the person too, so they know
it was read — and a competence row's detail shows the day as **Seen in the one-to-one**.
If the person then changes a declared competence or adds its evidence, that row is new
again. The button is greyed out while there is nothing new. The person's manager and HR read
**What they want** and mark it as seen; `staff:personal-data` reads it and marks
nothing; whoever holds `staff:approve` without being the manager does not see it, and
neither do colleagues. The rules are under
[what the person wants](/en/reference/staff/#what-the-person-wants).

**Reviewed together.** Last on the tab, **Reviewed together** shows **Last reviewed** —
the day and who recorded it, or *Never* — and, behind **Show earlier reviews**, the
reviews before it. When the two of you have gone through the tab, press **Reviewed
today** and confirm: it records that you went through the competences together that
day, and nothing more — no notes, no goals. The manager, whoever holds `staff:approve`
and HR may press it, never on their own record; the person sees the line but does not
record a review of themselves. When no review is recorded within the interval HR sets
under [Competence reviews](#competence-reviews) — 12 months by default, counted from
the last review or from the start of the current employment, whichever is later — the
dashboard tells the manager and HR *The competence review of … is overdue*, and My team
lists the person under **Reviews overdue**. See
[the review](/en/reference/staff/#the-review).

### History

**History** lists every change to the person, newest first and paged: what changed,
who changed it and when. A field shows its value before and after (*Work email:
old → new*); a field of a group you may not read only that it changed (*Birth date
changed*); a change of the pay is left out for a manager, so a pay correction shows as
the kind of change alone, marked *(details not visible to you)*. The person, a manager of theirs and HR see the History tab;
a colleague does not.

The account's changes are there too, for the person, HR and `staff:personal-data`
alone: *Account flag changed* with the fields before and after (*Staff provides an
account: No → Yes*, **Account from**, **Address**), and the accounts worker's steps —
*Account prepared*, *Account enabled*, *Account disabled*, *Account brought in line*,
*Account held up*, *Account step failed* and *Account belongs to another employee
number* — each with the **Step** it took, the **State** it left the account in, why it
was held up or failed, the **Invitation**, and whether the account flag was turned off
meanwhile, all in words.

When the person joins or leaves a [group](#groups), the history says *Joined a group*
or *Left a group*, by HR — or by the accounts worker for a derived group — with the
group's name and how they came in, *Listed* or *The rule* (*Sales (The rule)*). The
group's name and how are shown to the person, HR and `staff:personal-data`; a manager
sees only that the person joined or left a group.

### Documents

**Documents** is the person's archive: the contract, attests, certificates, letters and
the like, each a PDF, JPEG, PNG or HEIC file. The person themselves, HR
(`staff:manage`) and `staff:personal-data` see the tab; a manager and a colleague do
not.

The documents are listed by category, each category with its **class**, which decides
when its files are erased once the person has left — hold the pointer over the class
for the rule:

- **A · Pay and contract** — five years after the year the employment the document is
  filed against ended.
- **D · Personnel** — with the rest of the personnel record, a grace period after the
  last employment ended.
- **E · Health** — a set time after the last employment ended.

The rules in full are under [Retention](/en/reference/staff/#retention). Each row shows
the file, its size, **Valid to** — marked **Expired** once that day has passed —
**Signed on** and when it was **Added**.

**Health documents** — those in a category of class E, such as **Health** — are seen
only by the person themselves and by `staff:personal-data`. HR without
`staff:personal-data` does not see them in the list and is not offered a health
category.

#### File a document (HR)

With `staff:manage` or `staff:personal-data`:

1. Press **Add document**.
2. Drop a file on the box, or press **Choose a file**. On a phone, **Take a photo** opens
   the camera.
3. Choose the **Category**, and fill in **Valid to** — a certificate's last valid day —
   and **Signed on** if they apply.
4. Press **Upload**. The document is listed under its category.

One file at a time: a PDF, JPEG, PNG or HEIC file of at most 10 MiB, and at most 200
documents per person. A file that is larger or of another type is refused before it is
sent. Staff also reads the file's own contents, and a file that is not what its name
says is refused on the file. The 200 include health documents you may not see, so HR
can be told a person has 200 while seeing fewer.

A **contract** asks for **Signed on** first, and says what it does: when exactly one
employment covers that day and has no signing date yet, the employment gets this one,
and [the contract check](#the-contract-check) counts the contract as signed. When two
employments cover the day, nothing is set — type the date on the employment instead.

#### Download, change or remove a document

- **Download** (the arrow) fetches the file and saves it under its name. Everyone who
  sees a document can download it.
- **Change** (the pencil) changes the **Category**, **Valid to** and **Signed on**; the
  file never changes. A date you empty is cleared. If somebody else changed the
  document meanwhile, the form closes on what is there now and says so.
- **Remove** (the bin) asks first. The file is removed for good and cannot be
  restored; the History keeps a line saying it was removed.

Changing or removing a health document, and moving a document into or out of a health
category, is `staff:personal-data`'s: HR without it is not offered **Change** and
**Remove** on a health document — not even on their own. Changing a health document,
`staff:personal-data` also chooses its **Sick-leave case** — one of the person's cases,
open or closed — so it is erased with the case's health data; empty the field to take it
off the case. A document in any other category belongs to no case.

### Account

The **Account** tab is HR's (`staff:manage`). The person themselves and a holder of
`staff:personal-data` read it and change nothing. A manager, `staff:approve` and a
colleague do not have it. It has two
groups — three for a person of the Microsoft Entra ID kind, whose **Directory profile**
follows.

**Account flag** says whether Staff gives the person a user to sign in with, from which
day, at which address and who holds the account. HR changes it:

1. Turn on **Staff provides an account**. Staff then creates the person's user,
   disabled, the number of days before its day that **Prepare the account (days
   before)** in [Accounts](#accounts) says, enables it on that day and disables it the
   day after the last employment ends. It is never deleted. Turning the flag off stops
   Staff creating or enabling the account, but a person who leaves still loses it: the
   day after their last employment ends, Staff disables an account it made whatever the
   flag says (an account HR linked by hand is left as it is).
2. **Account from** is the day the account is enabled. Left empty, it is the first day
   of the person's earliest employment that has not ended, and the field says which
   day that is — or that there is none yet, so there is no day to enable the account
   on. A day you type wins over the employments; clear it to follow them again.
3. Choose the **Address**: **Work email** or **Private email**. When the person does
   not have the one chosen, the tab warns *… has no work email on file: the account
   waits until one is added*.
4. Choose the **Account kind** — who holds the person's account:
   - **Local** — Vantigo holds the password: Staff creates the user and invites the
     person to choose one.
   - **External identity provider** — the company's identity provider owns the account:
     Staff finds the user it created once an Owner has adopted it, enables what Staff
     disabled and disables, but never creates or invites.
   - **Microsoft Entra ID** — offered only while an enabled module offers it: Staff has
     Entra create the person's account, enables and disables it there, and tells
     Vantigo to expect it, so the user it brings is the person's with no Owner in
     between.

   A new person gets the **Default account kind** from [Accounts](#accounts). Changing a
   person away from Microsoft Entra ID while Entra holds an account for them — enabled,
   disabled or held up, until Entra has deleted it or reported it gone — is refused —
   *The account kind cannot leave Microsoft Entra ID while Microsoft Entra ID
   holds an account for the person. The person leaves the directory by Disable now or
   by the end of their employment.* A person whose
   Vantigo user still waits for its invitation can be moved from Local to Microsoft
   Entra ID: the account that arrives from Entra takes over that user. Until Staff has
   set everyone's kind on the upgrade that brought the kinds, a change of kind is not
   saved: *Could not save* — *Identity could not be reached, or no module provides
   accounts. Nothing was changed; …* — try again later.
5. Press **Save the account flag**. The fields are saved together. If somebody
   else changed the flag since you opened the tab, the tab reloads and says
   *Somebody else saved this; it has been reloaded*.

**User account** first says the person's account kind in a sentence — *Local account —
Staff creates the user and invites the person to choose a password.*, *External
identity provider — the provider owns the account: …* or *Microsoft Entra ID — Staff
creates the person's account in the directory, …* — or *Accounts are unavailable — …*
when no module provides them. For a person of the Microsoft Entra ID kind it then shows
the person's accounts as cards side by side: **Vantigo**, the user the person signs in
to Vantigo with, and a card titled with the provider's name, **Microsoft Entra ID**, for
their account in the company's directory. For anyone else it shows the Vantigo user
alone.

Until Staff has created or found a user, the Vantigo part says *No user yet: Staff has
not created or found one for this person.* Then:

- **State** — *Being prepared — Staff is creating or finding the user*, *Pending —
  waiting for the person to choose a password*, *Active*, *Disabled by Staff* or
  *Disabled by an Owner*, *Failed*, *Blocked*, or *Linked, not managed by Staff* for a
  user HR linked by hand.
- **User**, and **Managed by Staff** — yes or no.
- Why the account is held up, in a sentence, when it is: no employment to give an
  account from, the address the flag names missing, an Owner disabled the user (or the
  pending user, whom Staff then never enables), the user gone, the account belonging to
  another employee number (someone else's, left alone), a pending user made for a local
  account while the person's kind no longer invites, the address in identity differing from the person's
  after their invitation was sent (an Owner changes it in identity), an invitation revoked
  by an Owner or three left unanswered, the user no longer managed by Staff, an Owner or
  a system administrator, no user with the employee number yet in the identity
  provider, the address or the employee number held by a user Staff does not manage,
  no roles named for Staff — or one that grants administration — an erased person whose
  user in identity has not yet been erased (the retention worker tries again), and a
  worker whose mode differs from the installation's. *Failed attempts in a row: 3* says
  identity could not be reached; the worker tries again.
- **Invitation** — whether one is open and can be accepted, the latest lapsed
  unanswered, an Owner revoked it, or three went unanswered, with how many were sent.
- **Next attempt** and **Last brought in line**.

For a user Staff does not manage — one HR linked by hand, once the flag is on, or one
an Owner stopped managing — the tab tells HR that an Owner adopts the user for Staff on identity's
**Users** page (**Adopt for Staff**), with the person's employee number, and **Open
Users** goes there. An erased record shows no such step.

The **Microsoft Entra ID** card shows the person's account in the directory — or *No
account in the directory yet: Staff has not created or found one for this person.*:

- **State** — *Being prepared — Staff asked the directory to create or find the
  account*, *Enabled*, *Disabled*, *Deleted — restorable in the directory for thirty
  days*, *Failed* or *Blocked*.
- **User principal name** — the name the person signs in to Microsoft with, once Entra
  has made the account — and **Enabled since** while it is enabled, or **Last brought in
  line** while it is not.
- **Pass issued** — when Entra issued a Temporary Access Pass nobody has shown yet —
  **Announced** — when Staff told Vantigo to expect the account — and **Next attempt**.
- Why the account is held up, in a sentence, when it is: Entra not connected or failing,
  two accounts in Entra with the person's employee number, the work email already
  another account's there, an account synchronised from an on-premises directory, an
  account with an administrative role in Entra, an account Vantigo did not create
  whose adoption nobody has confirmed on the Microsoft Entra ID page (Staff writes
  nothing to it until someone does), an account not deleted because it is
  still enabled there, the account gone from Entra (Staff never makes a second); and,
  one sentence each, what Entra reported and could not do: the hire date not written,
  no licence left (the licence named in brackets), a pass not allowed or refused, a
  leaver step not finished (the step named — Staff tries it again the next day), the manager's
  account not found, the person's manager not in Entra, the photo not written (*The
  directory refused the photo.*). *Failed attempts in a row: 3.
  The directory could not be reached; the worker tries again.* says Entra could not be
  reached.

Below it, **Directory profile** is a read-only table, **Field**, **Written** and
**Value today**: every field Staff writes on the account and the value it carries from
the register today. The names, the employee number, the user principal name and the
usage location are always written and shown greyed, **Written** *Always*; the others
follow the installation's [Directory profile](#directory-profile), **Written** *Yes*, or
*Off — not sent* for a field switched off there, which Staff no longer writes and the
directory keeps as it is. The person
sees this table on their own tab too: it is their data as the directory sees it.

**Show the pass** (HR) is offered while Entra holds a pass nobody has shown. It shows
the pass Entra issued for the person's first sign-in in a window, **Temporary Access
Pass**, with *… types this pass at their first sign-in.*, *Works until …*, **Copy** and
**Close**: *It will not be shown again.* It is shown **once**: after you close the window
nobody can see it again, and the button is gone. How long a pass works, and whether it
works for one sign-in only, is the Microsoft Entra ID connection's setting — eight hours
unless changed — under its [**Set the account rules** step](/en/user/entra/#4-set-the-account-rules). Give it to the person yourself; it is
never mailed. A pass that has run out, or one already shown, is gone — the tab says
*The pass was shown already, has expired, or none was issued. Run now (new pass) issues
a new one.* — so use **Run now (new pass)**.

**Run now** (HR, while the flag is on) does the account's next step at once, with a
fresh invitation for a pending user whose last one cannot be accepted. The tab then
says *Done: a new invitation was sent*, or *Done: no invitation was sent* — none was
due, one can still be accepted, or the last went less than ten minutes ago; the run is
done all the same. For a person of the Microsoft Entra ID kind it is **Run now (new
pass)**: the step runs in Entra first, then in Vantigo, and a new pass is issued only
when the run enables the account — on the person's first day, or when they are hired
again; an account already enabled gets no new pass, only its details brought in line.
The tab then says *Done: when the account is enabled, the directory issued a new
pass — Show the pass shows it once.* When Entra refuses the step, the tab says *The
identity provider refused the step; nothing more was done there.* and why, in a
sentence.

**Disable now** (HR) asks first — *This also turns the account flag off, so the account
stays disabled until HR turns the flag on again* — then disables the user — for a
person of the Microsoft Entra ID kind the Entra account first, then the Vantigo user —
and says *The account is disabled and the account flag is off.*, *The account and the
directory account are disabled and the account flag is off.* when both were, *The
account flag is off and the directory account is disabled.* when there was no Vantigo
user yet, or *The account flag is off; there was no account to disable.* It is not offered
for a user HR linked by hand under Self-service, which is not Staff's to disable: an
Owner disables that account in identity. When the user cannot be disabled
from Staff, the tab says why, the flag already off: Staff does not manage the user, the
user is an Owner or a system administrator (an Owner disables them on the Users page),
or the account carries another employee number — or, for the Entra account, Entra
refused. When identity or Entra cannot be reached, the flag is off and the accounts
worker disables the account at its next run, as soon as it can. For a person of the Microsoft Entra ID
kind, Disable now is offered too while their Entra account is enabled, even with the
flag already off. While the
worker is acting on the account, Run now and Disable now say so and change nothing:
try again in a few minutes. After any refusal the tab reads the account again.

On an [erased record](#an-erased-record) the tab is read-only but for **Disable now**:
the person's access must end too.

A **Groups** card lists the [groups](#groups) the person is in — *the groups the
person is in, and so the groups and licences a directory gives them*, archived groups
aside — by **Name**, **Came in by** (*Listed* by HR, or *The rule*) and **Since**, or
says *Not in any group.* It is read-only here: with `staff:manage` a group's name opens
its page, where HR changes its members.

### A legal hold

With `staff:personal-data`, a person's page has **Put under legal hold** at the top. A
legal hold keeps the record whole — no retention erase touches it, and the person
cannot be deleted — for as long as a dispute, a claim or an investigation needs it.

1. Press **Put under legal hold**.
2. Write the **Reason** — required, up to 500 characters: what the hold is for and, if
   you can, until when.
3. Press **Set the legal hold**. The page shows **Under legal hold** with the day it
   was set and the reason, and the History has **Legal hold set**.

To lift it, press **Lift the legal hold** and confirm. The next retention run may then
erase what has fallen due meanwhile; the History keeps the hold and its reason.

HR with `staff:manage` alone sees **Under legal hold** and the reason but cannot set or
lift it. A manager and a colleague are not shown the hold.

### An erased record

Once a former employee's last employment has ended and the grace period has passed,
the [retention run](#retention) erases their personnel administration, and also erases
the disabled user Staff managed for them in identity (leaving a user HR linked by hand
under Self-service untouched, and still deleting a Microsoft Entra ID account once
**Delete an external account (days after the end)** has passed). HR
(`staff:manage`, `staff:personal-data`) then sees **Erased** beside the person's status
and a notice, **This record was erased on** and the day: the contact details, the
address, the next of kin, the national identity number, the bank account, HR's notes
and the personnel documents are gone; the employments, the history and the pay and
contract documents remain.

The record is read-only, and the page offers none of its changes — **Edit details**,
the next of kin and the roles, setting the national identity number or the bank
account, linking a user, the employments' and the terms' actions, and filing, changing
or removing a document are not there, because Staff would refuse them. What may still
be done:

- the [legal hold](#a-legal-hold) and [the reassessment](#the-reassessment);
- **Unlink** under Self-service, so the user can be linked to someone else;
- **Disable now** on the [Account](#account) tab;
- reading: the History, the Documents and their downloads, and
  [the personal-data file](#download-a-persons-data);
- [Rehire](#rehire).

#### Rehire

When an erased former employee comes back, rehire them on their own record rather than
adding a new person, so their history stays in one place. With `staff:manage`:

1. Open the person, go to **Employments** and press **Rehire**.
2. Fill in the employment as in [New person's second step](#step-2-the-first-employment)
   and press **Save employment**.

The record opens again under the same employee number: the notice goes, and the History
has **Rehired** before the new employment. What the erase removed stays empty — type
the contact details, the address and the next of kin again, and, with
`staff:personal-data`, the national identity number and the bank account.

#### The reassessment

Twelve months after an erased person's last employment ended, the
[dashboard](#the-dashboard) shows **Reassessment due** to `staff:personal-data`, and
the item opens the person's page. The notice there says whether the reassessment is
recorded.

1. Review what is left of the record.
2. Press **Mark as reassessed** in the notice and confirm.

The notice then says **Reassessed on** and the day, and the dashboard's item goes away.
Before it is due, or once it is recorded, Staff refuses it, and the page says that the
reassessment is recorded once, from twelve months after the last employment ended. A
rehire clears the reassessment with the erasure. `staff:manage` alone sees whether it
is recorded but cannot record it.

### Download a person's data

With `staff:personal-data`, **Download personal data** at the top of a person's page
saves everything Staff has about them in one file,
`staff-<employee number>-personal-data.json`: the record with the national identity
number and the bank account in full, HR's notes, the next of kin, the roles, every
employment with its terms, the user link, the documents' list and the whole history.
It is the answer to an innsyn request — see
[Answer an innsyn request](/en/admin/staff/#answer-an-innsyn-request).

The page asks first, because the file opens the sealed values: the download is recorded
in the person's history as **Personal data exported**, with your name.

## Add a new person

With `staff:manage`, press **New person** in People, or choose **New person** in the
search's quick actions, which opens People with the form already open. The form has two steps, as a hire
does: the person, then their first employment. The person is saved by the first step,
so you can stop after it and add the employment later — they stay in People under
**Current** with the status **No employment**.

### Step 1: the person

1. Fill in **Given name** and **Family name**. They are required (up to 100
   characters each). **Display name** is optional; left empty, it is the two names.
2. The employee number is **generated** by default: the form says which number comes
   next — the settings' prefix and next number — unless somebody already has it, in
   which case the next free one is used. To give your own, turn on **Assign my own
   employee number** and type it: up to 16 letters, digits or hyphens, unique however
   it is cased. The number never changes afterwards.
3. Add **Work email**, **Work phone**, **Language** and **Work country** as you have
   them. A work email another person has is refused on the field.
4. **Private details (optional)** opens the birth date, gender, mobile and private
   phone, private email and home address. None of them is required to create the
   person.
5. Press **Save and continue**. A refusal lands on the field it is about.

When the given name, the family name and the birth date are all filled in, **Save and
continue** first asks whether a former employee with the same names and birth date has
been [erased](#an-erased-record) — Staff cannot recognise them by the national identity
number, which the erase removed. If one has, the form says **Someone with these names
and birth date was erased**, lists them with their employee number and the day they
were erased, and waits:

- **Open** with the employee number opens the erased record; if it is the same person,
  [rehire](#rehire) them there.
- **Create anyway** adds the new person under a new employee number.

Without a birth date nothing is asked. If the question cannot be answered, the person
is created without it.

### Step 2: the first employment

The step opens with a reminder, and it is the reason two fields are marked required:
under aml. § 14-6 the contract states whether the employment is permanent or temporary
and its **percentage of a full position** (stillingsprosent). When either is missing,
the law presumes what the employee claims — so Staff never fills them in for you, and
refuses the terms without them.

1. **Business unit** is preset to the default unit; choose another if the employment
   is reported elsewhere. **Start date** is preset to today in the business time zone.
2. The employment number — the a-melding's arbeidsforholdID — is generated from the
   employee number (`E-8-1` for employee `E-8`). Turn on **Assign my own employment
   number** to type one instead (up to 40 letters, digits or hyphens).
3. Choose the **Employment type**: **Permanent (fast)** or **Temporary
   (midlertidig)**. A temporary employment asks for its **Temporary ground** (aml.
   § 14-9 (2) a–e, or a tariff agreement's) and, if you know it, the **Expected end**.
4. **Appointment form** follows the type and offers only the forms that go with it;
   a form that hires the person out asks who they are **Hired out to**.
5. Type the **Percentage of a full position** — more than 0, at most 100.
6. **Working-hours scheme** starts at **Not shift work**, and **Hours of a full
   position per week** follows the scheme (37.5; 36.5 for shift work; 33.6 offshore
   and continuous shift; 35.5 round-the-clock) until you change the hours yourself.
   Hours that do not fit the scheme are flagged at once — exactly 36.5 or 35.5 for
   those two schemes, 28 to 33.6 for the two 33.6 schemes — and nothing is sent until
   they do. See [the hours rule](/en/reference/staff/#the-hours-rule).
7. Add the **Department**, **Location**, **Manager**, **Title** and **Occupation
   code** (the STYRK-08 yrkeskode, seven digits) as you have them. Left empty, the
   manager is the department's.
8. **More terms** opens the rest of what § 14-6 asks a contract to hold — the worker
   type, the probation, the notice periods, the working time and breaks, any special
   arrangement, overtime, the tariff agreement, holiday, competence development,
   social security and pension, the cost centre and division, the remuneration, pay
   method and payday, and HR's note — plus the kind of employment relationship and the
   day the contract was signed.
9. Press **Save employment**. The person's page opens. If the server refuses a field
   — a percentage out of range, an archived department — the refusal lands on that
   field; one about the whole employment is shown as a message.

**Add it later** closes the form with the person saved and opens their page, where
**New employment** on the Employments tab adds it.

## Groups

**Groups** (`staff:manage`) is where HR keeps the company's groups: sets of people, kept
in the company's directory — Microsoft Entra ID — when you turn it on for a group, where
they become a security group or a Microsoft 365 group (a team too, if you ask). A group's
licences are how its members get theirs in Microsoft 365. There are two kinds:

- **Listed** — *You name the members.*
- **Derived** — *A rule names the members, brought up to date every business day*: who
  joins and who leaves is written to each person's history.

### The list

**Groups** lists the groups by **Name**, with **Key**, **Kind**, **Members** (how many),
**Providers** — a badge for each directory HR turned on for the group, such as
*Microsoft Entra ID: In sync*, *Microsoft Entra ID: Failed* or *Microsoft Entra ID: Off*,
or *—* when there is none — and **Status**, *Archived* for an archived group. Archived
groups are hidden, and greyed out, until you turn on **Show archived**. Press a group's
name to open it. With no group yet, the page says *No groups yet* — *Add a group with
New group.*

### Add a group

1. Press **New group**.
2. Fill in the **Key** — lower-case letters, digits and hyphens, at most 60, starting
   with a letter or a digit, unique. It is how the directory knows the group (its mail
   nickname, unless you set another) and **cannot be changed later**. Then the **Name**
   and, if you like, a **Description**.
3. Choose the **Kind**, **Listed** or **Derived**. For **Derived**, choose the **Rule**:
   - **Everyone employed** — *Everyone with an employment in force today.*;
   - **Department** — the people whose terms place them in the department you pick;
     turn on **Include departments beneath** to take in *the people in every department
     under this one too*;
   - **Business unit** — the people employed in it;
   - **Location** — the people whose terms place them there;
   - **Employee type** — the people of the type you pick;
   - **Reports to** — pick the **Manager**: *the manager's direct reports today, one
     level down*.

   A person with several employments is judged on the one that started first.
4. Press **Save**. A key already taken, or a rule without what it needs, is marked on its
   field. The page says *… is saved* and opens the new group. A derived group fills at
   the accounts worker's next run, within the hour; a listed one starts empty.

### A group's page

The page's header shows the group's name, its kind, its key and its rule in words —
*the department Sales and those beneath it*, *reports to Kari Nordmann* — and its
description. **Edit** opens the form, which shows *Key: … — it never changes.* in place
of the key; the kind can change there too. If someone else saved the group since you
opened it, the page says so and reads it again. **Archive** asks first. **Delete** is
there only while no directory has the group or has ever synced it and none is turned on
for it, archived or not; otherwise archive it — or, when no directory has made it yet,
turn its directories off first. A directory that is sending the group at that moment
still refuses the delete. It asks *Delete …?* — *The group, its members, owners and settings are
deleted. No directory has it yet.* — and each member is recorded on their history as
having left the group.

- **Members** — for a listed group, *The people you name, from the day you save them*:
  pick them under **The members** and press **Save the members**; each who joins or
  leaves is written to their history. For a derived group the list is read-only,
  *Materialised daily by the rule*. Each member is listed with their **Employee
  number**, **Came in by** — *Listed* or *The rule* — and **Since**; an empty group says
  *Nobody is in the group.*
- **Owners** — *The people who own the group in each directory, beside Staff itself*:
  pick them under **The owners** and press **Save the owners**, for a listed or a derived
  group. An owner needs an enabled account in the directory to be one there; the
  change reaches it at the accounts worker's next run.

Below them is a card for each directory Staff can keep groups in, titled with its name —
**Microsoft Entra ID**. Until the directory is connected, the card says *Microsoft
Entra ID is not connected yet; the group is made there once it is.* Turn on **In
Microsoft Entra ID** and the card shows the group's settings there, which the directory
itself describes: each with its label in your language, *About this setting* linking to
Microsoft's page, the required ones marked and the defaults filled in. For Microsoft
Entra ID they are the **Group type** — *Security* or *Microsoft 365*, which cannot change
once the group is made — the **Mail nickname**, which cannot change either, the
**Visibility** of a Microsoft 365 group, the **Description in the tenant**, **Team in
Microsoft Teams** for a Microsoft 365 group, and the **Licences** its members get, each
with the seats left. Press
**Save**: a value that does not fit is marked beside its field, and otherwise the page
says *Saved for Microsoft Entra ID; the group follows at the accounts worker's next run*.

The card's badge then says where the group stands: *Waiting* until it is sent, *Found
in Microsoft Entra ID and awaiting confirmation there*, *In sync since …* with *Staff's
in Microsoft Entra ID too*, *Failed*, *Kept, renamed* or *Let go*. When the directory
refused a setting — a type or a mail nickname changed after the group was made, a team
with no person among the owners — the card says *Failed:* and why beside that setting;
a refusal of the group itself — *the directory holds a group by this key that Staff may
not write* — is said at the top of the card. Members the directory could not change are
said there too, and tried again at the next run. Change the value and save again — or
change the group's name, description or owners — to have it tried again. Meanwhile a
group the directory already holds still has its members kept in step: a person who
leaves the group in Staff leaves it there too. Once a day Staff sets the members in the
directory to the group's again, so a member someone added there by hand is removed.

**A group found in the directory.** When Microsoft Entra ID already held a group with the
mail nickname that Vantigo did not make, Vantigo links it but writes nothing to it until
someone who manages Microsoft Entra ID confirms it on the Microsoft Entra ID page
([Confirm a group Vantigo did not create](/en/user/entra/#confirm-a-group-vantigo-did-not-create)).
Until then the badge says *Found in Microsoft Entra ID and awaiting confirmation there*
and the card *the directory held a group by this key that Staff did not make; it is
written to only once someone confirms it there*. It is not a failure: the worker asks
again at each run, and the group follows at the run after the confirmation. A team is made once the group is fifteen minutes old,
so it appears a run after the group. When the directory could not say what a group can
carry, the card says *Microsoft Entra ID could not say what a group can carry there.
Try again later.*

**Turning the switch off** asks *Turn off Microsoft Entra ID for …?* and says what
happens, by **Delete an external account (days after the end)** under
[Accounts](#accounts): *The group will be let go in Microsoft Entra ID after N days —
deleted, or emptied and kept, as the directory's rules say.*,
or, when that is empty, *The group will be kept in Microsoft Entra ID, renamed “…
(archived)”.* **Turn off** confirms, and the page says *Microsoft Entra ID is off for
the group*. The settings are kept. A directory you turned on but never saved for is
just turned off again, without asking.

**Let go in Microsoft Entra ID** means deleted for a Microsoft 365 group Vantigo made,
which the tenant can restore for thirty days. A security group Vantigo made is never deleted there,
since Microsoft cannot restore one: when the days have passed, its people are removed
from it, it is renamed “… (archived)” and kept. A group Vantigo found rather than made
is left in Microsoft Entra ID as it is. The card says *Let go* in each case; the
[administration guide](/en/admin/entra/#staffs-groups-in-the-tenant) has the
details.

**Archive** asks *Archive …?* — *The group keeps its members and becomes read-only. In
each directory it is let go after the days Staff's settings say — deleted, or emptied
and kept, as the directory's rules say — or renamed “… (archived)” and kept when they
say never.* — and does that for every directory. The
page then says *The group is archived: it keeps its members as they stood and can no
longer be changed.*, shows the owners as a list, and offers no **Edit**, **Archive**,
**Save** or switch. Nothing in Staff is deleted.

## My page

**My page** (`staff:access`) is your own record in Staff, found through the user HR
linked it to, and shows what you see of yourself — whatever else you may hold. It has
a person's four tabs, then **Absence**, **Competence**, **Account** and, when you manage
anyone, **My team**:

- **Profile** — [At a glance](#at-a-glance) first, your own cards (all but the
  contract), then your work details, your private details, your national identity number
  as **On file** and your bank account's last four digits, your next of kin, your roles
  and where you sit in the organisation, and under them **Your groups**: by name, the
  groups you are in, as HR keeps them (*You are not in any group.* when there are none).
  Press **Edit my details** to change your
  mobile and private phone, your private email, your home address and your next of
  kin, then **Save**. Everything else — your name, your work details, your birth date
  and gender, your national identity number and your bank account — is HR's to change:
  ask HR. If HR saved your record while you were editing, the form closes on what is
  there now and says so.
- **Employments** — your employments and their terms, the pay included: it is your
  contract.
- **History** — the changes to your record, without HR's notes.
- **Documents** — your documents, health documents included, to download; HR files and
  changes them. Under **My data**, **Download my data** saves a file (JSON) with your
  record, your next of kin, roles, employments and terms, your documents' details, your
  competences, what you want — your interests, preferences and availability — your
  competence reviews, your photo's type and when it was set, and your history — without the values HR keeps sealed (your national identity number and
  bank account in full) and without HR's notes. The file lists your documents without
  their contents: download each from the list. Downloading your data records nothing.
  When Staff gave you your account, it disables it the day after your last
  employment, so My page and **Download my data** are gone with it: ask HR for your data, and they
  download it for you with the personal-data export.
- **Absence** — your holiday account, your egenmelding standing, your care days and
  your absences, with **Request absence** (see
  [Request absence](#request-absence-from-your-phone) below).
- **Competence** — what you can do, with **Add competence**, and what you want — your
  interests, preferences and availability (see [Your competence](#your-competence) and
  [What I want](#what-i-want) below).
- **Account** — whether Staff gives you a user to sign in with, from which day and at
  which address, and how your account stands: pending your password, active,
  disabled, with your invitation, and the **Groups** card: the groups you are in, how
  you came in and since when. It is read-only: ask HR to change it.
- **My team** — only when you manage someone today: the requests you decide, your
  reports' absence this month, their open sick-leave cases and what waits on their
  competences (see [My team](#my-team) below).

If HR has not linked your user to a record yet, My page says *You have no record in
Staff yet* and explains how it comes to be; ask HR if you think yours is missing.

My page is made for a phone first: on a narrow screen everything is one column, the
form takes the whole screen and the request sheet opens from the bottom.

### Your photo

Your picture, or your initials when there is none, is beside the page's title. It is
your Vantigo picture — the same one as under **Settings › Profile** and in the avatar
menu — so changing it here changes it there too.

1. Press it (the button **Photo of** and your name).
2. Choose **Change photo** and pick a JPEG or PNG: on a phone, take one with the camera
   or choose one from your library.
3. To take it away, choose **Remove photo** and confirm; your initials are shown in its
   place.

When HR has set a photo of you, that photo is your picture everywhere in Vantigo, and
in Microsoft Entra ID when you have an account there. The menu then shows **Set by HR**,
greyed out, with *Ask HR to change it*: you cannot change or remove it yourself. Your own
picture is kept aside, and comes back when HR removes their photo.

A photo is at most 5 MiB and 4096 pixels on each side; any other file is refused with
the reason. Everyone who can open your page in Staff sees your picture, colleagues
included. Your picture reaches Entra ID within the hour. Your history
says when HR set or removed a photo, never the picture.

### Your absence

The **Absence** tab of My page shows, as Staff counts them:

- **Holiday account** for this year, in working days: **Entitlement**, **Carried in**
  (and **Carried in automatically**, when the company carries untaken days over by
  itself), **Taken**, **Requested, waiting** and **Remaining**. When what you have asked
  for would take more than remains, the card says so — the requests are still sent.
- **Egenmelding** — the company's policy (*The statutory minimum*, *The extended
  policy* or the company's own limits), how many calendar days you may self-certify at
  a time, what you have used in the last 12 months against the policy's limit, and from
  when you may. If HR has withdrawn your right to egenmelding, it says until when and
  why.
- **Care days** for this year — **Entitlement**, **Used** and **Remaining** — from the
  details HR recorded about your children; if they are not recorded yet, it says so.
- **My absences**, newest first, each with its type, days, status (*Requested*,
  *Approved*, *Rejected*, *Cancelled* or *Registered*), working days or hours, your
  note and the decider's note. A request still waiting has **Change** and **Cancel
  request**; a cancelled request stays in the list.

### Your competence

The **Competence** tab of My page is what your position asks of you, what you can do —
skills with a level and certificates with a validity — as you and your manager keep it
together, and, under it, what you want. It is made for your phone.

**What your profile asks.** At the top of the tab, **What your profile asks** shows each
competence profile that applies to you — what your position needs, assigned to you by
HR or your department's default — and, for each competence it asks, whether you meet
it. **Met** means you do; with the **Declared** badge it counts too, and your manager
verifies it when you next meet. **Expires soon** still counts: renew the certificate
before its last day. **Declared, not verified** is a certificate you added yourself: it
counts once your manager has verified it, so add the evidence. **Below the level** asks
for more than the level you hold: talk to your manager about how to get there, and say
under **What I want** that you want to learn it. **Expired** is a certificate past its
last day: renew it and press **Renewed**. **Missing** is a competence you have not
added, or a certificate not valid yet: if you hold it, add it with its evidence. Each
profile says how many items are not met, or *All met*. When no profile applies to you,
the tab says *No profile asks anything of you*.

To add a competence:

1. On My page, open **Competence** and press **Add competence**. On a phone the form
   fills the screen.
2. Under **Competence**, search the catalogue and choose one. If it has a level,
   choose the **Level**, from *aware* to *expert*.
3. For a certificate, set **Issued**. When the certificate has a fixed validity,
   **Valid to** is proposed from it — change it if your certificate says otherwise.
   Add the **Issuer** and the **Reference** (the certificate's number) if you like.
4. Under **Evidence**, press **Take a photo** to photograph the certificate with the
   phone's camera, or **Choose a file** (a PDF, JPEG, PNG or HEIC of at most 10 MiB).
   The evidence is optional, and is filed among your documents under *Certificate*.
5. Press **Save**.

You can also start from anywhere: search for **Add competence** (Ctrl+K or ⌘K) and the
form opens on My page's **Competence** tab.

The competence is listed with the **Declared** badge. Your manager sees it on their
dashboard and verifies it — usually in your next one-to-one — at the level the two of
you agree on; it then reads *Verified by* them. Until then **Edit** and **Remove** are
yours; once it is verified it is no longer yours to change or remove: renew it, or ask
your manager. If the evidence could not be filed, the page says so — the competence is
saved: press **Add evidence** on it to try again. A competence you already have is
not added twice: change or renew the one you have.

**When a certificate expires.** Within its reminder of the end — 30 days ahead for a
yearly certificate, six months for a five-year one — it shows **Expiring** with the
days left, and you and your manager get an email; once the day has passed it shows
**Expired**, and you get one more. When you have renewed it, press **Renewed** on it,
set the new dates and add the new certificate: the old one stays as history, and the
new one waits for your manager's verification, as a new one does.

**Who sees it.** Colleagues who hold `staff:view` see what you can do: your
competences, their levels, whether they are verified and how long they are valid. The
detail — the evidence, the issuer, the reference and the verification — is seen by
you, your manager, whoever holds `staff:approve` and HR.

#### What I want

Under your competences, **What I want** is yours to write for the one-to-one: what you
would like to learn or do more of, how you would rather work, and when you can work.
Only you write it; your manager reads it, before or in the conversation. Each part is
saved on its own:

- **Interests** — under **Your interests**, search the catalogue and pick the
  competences you are interested in, at most 20, then press **Save interests**. One you
  already have reads *Wants to use more*; one you do not, *Wants to learn*. Take one off
  with its cross and save again.
- **Preferences** — one card for each kind: **Weekdays** (the days you would rather
  work), **Time of day** (*Morning*, *Day*, *Evening* or *Night*), **Location** (up to
  ten of the company's locations), **Travel** (*No travel*, *Within the region*,
  *Within the country* or *Abroad too*), **Remote work** (*Never*, *Some days*,
  *Mostly* or *Only remote*), **Most days in a row** (1 to 14) and **Most hours a week**
  (1 to 60). Turn on **I have a preference** on a card, fill it in and choose **How much
  it matters**: *Nice to have*, *Matters* or *Important*. Turn the switch off to take a
  preference away. Press **Save preferences**.
- **Availability** — when you can, cannot or would rather work. Press **Add
  declaration** and choose the **Kind** — *Available*, *Unavailable* or *Preferred* —
  and how it **Repeats**: **Every week**, on a **Weekday**, with a **First day** and a
  **Last day** if it begins or ends; or **On dates**, from a **First day** to a **Last
  day** (left empty, the one day). Turn on **Part of the day** to say when it **Starts**
  and **Ends**; otherwise it is the whole day. Press **Add**. Add as many as you need,
  at most 50, take one away with its button in the table, and press **Save
  availability**. It is data only: nobody plans from it yet.

There is no field for a reason anywhere, on purpose. A reason for "no night shifts"
easily becomes a fact about your health, and that does not belong in a form: if a
preference needs explaining, say it in the conversation.

When your manager or HR has read what you wrote, each row reads *Seen* with the day; a
row you add or change after that has no mark until they have read it again, and so has
a competence you add. Below, **Reviewed together** shows **Last reviewed**: the last
time you and your manager went through the tab together, and who recorded it. Your
manager and HR read **What I want**; colleagues do not, and neither does someone who
holds `staff:approve` without being your manager.

### Request absence from your phone

1. On My page, open **Absence** and press **Request absence**. On a phone — a screen
   narrower than 768 pixels — the sheet opens from the bottom; wider, it is a dialog.
2. Choose the **Type**. The sheet asks for what that type takes:
   - a day type: **First day** and, for more than one day, **Last day**;
   - an hour type, such as a doctor's or dentist's visit: the **Day** and the **Hours**;
   - a type that allows it: the **Percentage of each day** (left empty, the whole day);
   - a sykmelding: its **Degree** (left empty, 100 %);
   - a **Note**, except on a sick or child-sick type, which carries none.
3. For a day type the sheet estimates the working days — Monday to Friday, without
   the public holidays — and, for holiday, about how many days you have left after it.
   For an egenmelding it shows what the policy allows you today before you send it.
4. Press **Send request** for a type that needs a decision, or **Register** for one
   that is a notification. The confirmation gives the working days and your holiday
   account as Staff counted them.

You can also start from anywhere: search for **Request absence** (Ctrl+K or ⌘K) and
the sheet opens on My page's **Absence** tab.

If Staff refuses, the sheet says why on the field it is about — another absence on one
of the days on **First day**; an egenmelding beyond what the policy allows, before you
have been employed long enough, or while your right is withdrawn on **Type**, telling
you to register a sykmelding instead. To change a waiting request, press **Change**,
adjust it and **Save**; if somebody changed it meanwhile, it is reloaded and the page
says so. A change keeps the kind of absence: a request stays a request, and a type that
reveals health or family matters is not swapped for one that does not, or the other way
round — those are decided by different people. Press **Cancel request** and request the
other instead.

### My team

**My team** is a tab of My page for anyone who manages someone today by the terms — a
team lead needs no other permission for their own reports. It shows:

- **Approvals** — the requests you decide, oldest first, each with the person, the
  type, the days, the working days and the person's note. Press **Approve**, or **Reject**, write the
  **Reason** and press **Reject request**: a rejection always says why. If somebody else
  decided the request first, or the person withdrew it, the page says so and reads the
  inbox again.
- **Absence in** the current month — each report's absences as you may see them.
- **Open sick-leave cases** of your reports — the follow-up milestones with their dates:
  **Mark done**, or **Waive** with why it is not needed (never a diagnosis); the end of
  the employer's period follows from the absences and is neither. **Change the
  accommodation note** writes the notes about tilrettelegging that the person reads too.
- **Awaiting your verification** — the competences your reports declared themselves,
  each with the day it was declared.
- **Certificates expiring** — your reports' certificates that expire soon or have
  expired, the expired first and marked **Expired**, each with its day.
- **Reviews overdue** — your reports whose competence review is overdue, each with the
  day it fell due.
- **Profiles not met** — your reports who do not hold what their competence profiles
  ask, each with how many items are not met (*2 not met*).

In the four competence cards, a name opens the person's **Competence** tab, where you
verify, renew, press **Reviewed today** or go through **What their profile asks**.

## The Absence page

**Absence** in the sidebar has two tabs. It opens on **Approvals** when you decide
requests, and on **Calendar** otherwise; the dashboard's request items open it too — a
manager without a permission beyond `staff:access` lands there on **Approvals**, with
the same requests as on [My team](#my-team).

### Calendar

**Calendar** shows a month: a row per person and their absences as bars across the
days, weekends and public holidays shaded. Move with **Previous month** and **Next
month**; with the directory or HR's permission, narrow it with **Department** (or
**All departments**). A dashed bar is a request still waiting. Who appears follows your
permissions: with HR's, the Approver's or the personal-data permission, everyone
employed in the month; with the directory alone, those of them active today; a manager
without those, their reports and themselves.

Each bar shows only what you may see of it: the type and its status, a percentage of
the day or the hours for HR and the person's manager; *Leave* for a type that reveals
health or family matters, once it is approved; and *Absent* — the days alone, without
a type or a status, a person's days run together into one bar and no visit of a few
hours shown — to a colleague who sees the directory. Point at a bar, or read it
with a screen reader, for its days and details. On a phone the month scrolls sideways
inside its frame.

### Approvals

**Approvals** lists the requests you decide, oldest first — the same inbox as on
[My team](#my-team), for whoever decides by right: the Approver (`staff:approve`) and HR
(`staff:manage`) decide everyone's requests of an ordinary type, and a holder of
`staff:personal-data` the requests of a type that reveals health or family matters,
which reach nobody else's inbox. Each shows the person, the type, the days, the working
days and the person's note. Press **Approve**, or **Reject**, write the **Reason** and
press **Reject request**. If somebody else decided it first or the person withdrew it,
the page says so and reads the inbox again; if the person already has an absence on one
of the days, the request cannot be approved as it stands. If you are the only person
holding `staff:personal-data`, your own request of such a type is in your inbox too and
you approve it yourself; everyone who holds that permission is then shown it on the
dashboard until the absence ends.

#### The mails a decider receives

- **A manager** is mailed at their work email (else their private one) when someone they
  decide for asks for absence — the person, the type (*an absence* for sickness) and the
  days — or registers it — the person, *an absence* and the days. A manager is never
  mailed about a type that reveals health or family matters.
- **The Approver and HR** are mailed only about a person with no manager who can
  decide — none in their terms or department, or none linked to an active user — and
  only about requests, at the address their user has; so is an **Owner**. The mail
  names the person, the type and the days and links to **Absence**.
- **A holder of `staff:personal-data`** is mailed about every request of a type that
  reveals health or family matters, by someone else: "An absence request awaits your
  decision", with no name, type or days — open **Approvals** to see it.

## The Competence page

**Competence** in the sidebar, for `staff:view` and `staff:manage`, answers who in the
company holds what. It has two tabs: **Matrix**, and, for HR, **Interests**. The tab and
every filter are kept in the page's address, so a link carries them.

### Matrix

**Matrix** lists every person active today, by name, with the competences they hold
that are valid on a day, a column for each competence. Narrow it with the filters:

- **Category** — the competences of one category.
- **Competences** — one or more competences, searched by name and grouped by category.
  Only the people who hold every one chosen are listed, with those as the columns.
- **Minimum level** — leaves out a level below it; a competence without a level, such
  as most certificates, is not touched.
- **Valid on** — the day the competences must be valid, today unless you choose
  another; clear it to go back to today. A certificate expired on that day, or not yet
  valid, is not shown: the person's tab says when it ran out.
- **Verified only** — leaves out what a person declared and nobody has verified.

With no competence chosen, everyone active is listed and the columns are the
competences the people shown hold; someone who holds none of them has an empty row.
Each cell shows the level, or a check for a competence without one, the **Declared**
badge when nobody has verified it — a declared certificate is shown too, unless you
turn on **Verified only** — and *Valid to* a date when it ends. A name opens the
person's **Competence** tab. On a phone each person is a card listing what they hold.
When nobody is left, the page says *Nobody meets the filter*: widen it.

The matrix shows what a colleague sees on the tab — the names, the levels, whether
verified and how long valid — and nothing of the detail. See
[the matrix](/en/reference/staff/#the-matrix).

### Interests

**Interests** is HR's (`staff:manage`) alone: who wants to learn what. A card for each
competence somebody has picked among their interests on My page lists those who **Wants
to learn** it — they do not hold it — and those who **Wants to use more** of it,
holding it already, each name opening the person's **Competence** tab. Read beside the
profiles, it says whom to train: someone whose profile shows **Below the level** or
**Missing** on a competence they want to learn is the first to ask. Only people active
today are listed, and whoever holds `staff:approve` without `staff:manage` does not see
the tab. With nothing written yet, it says *Nobody has written an interest yet*. See
[the interests overview](/en/reference/staff/#the-interests-overview).

## Absence and your holiday

The rules behind the screens above:

- **Asking for time off.** A type that needs a decision — ferie, a leave, time off in
  lieu — is *requested*; a type that is a notification — egenmelding, sykmelding, a sick
  child, a doctor's or dentist's visit — is *registered* as soon as it is entered. You
  enter your own; HR enters one on your behalf. Your manager does not enter one for you.
  A visit to the doctor is hours of one day; everything else is whole days, some types
  as a percentage of each day. A sick absence carries no note.
- **Two absences on one day** stand together only when both types allow a percentage
  and the percentages add up to at most 100 % — but a sykmelding of any degree stands
  beside a leave that leaves part of your day, as the doctor grades it against the
  work that is left. A request you have made counts as if it were
  approved, so you cannot ask twice for the same day. A visit of a few hours, such as
  a doctor's, stands beside any day absence; visits on one day add up to at most 24
  hours.
- **Sick during your holiday.** A sykmelding does not change your approved ferie by
  itself. If you are wholly unable to work (a 100 % sykmelding), you may demand that the
  holiday be postponed — with a legeerklæring, and by your last working day before the
  holiday when you fall ill before it, and without undue delay once back at work when
  you fall ill during it; tell HR, who records it. The ferie is then split
  around the sykmelding and the days return to your account. A sykmelding over an
  egenmelding replaces the days it covers.
- **Egenmelding** follows the company's policy: under the statutory minimum, three
  calendar days at a time and four times in twelve months. Days in a row are one
  egenmelding, and a weekend between two counts with it — egenmelding on Friday and
  again on Monday is four days; one illness around a sykmelding is one egenmelding. It is refused, with the reason, when you have used what
  the policy allows, before you have been employed the months it asks, past the
  sixteenth day of a sickness, or while HR has withdrawn your right to it; then you
  need a sykmelding. Your egenmelding standing shows what you have used in the last
  twelve months.
- **Your sick-leave case.** Your sick absences are gathered into a case — a new one
  when more than sixteen days have passed since the last. Its follow-up — the end of
  the employer's sixteen days (counted from your first working day off sick), the follow-up plan at four weeks, the dialogue meetings
  at seven and twenty-six, NAV's activity requirement at eight — is your manager's and
  HR's to mark. A case never holds a diagnosis: there is nowhere to write one.
- **Care days** for a sick child count against your days for the year — 10 for one or
  two children, 15 for three or more, more as a sole carer or with a chronically ill
  child — from the details HR has recorded. Going over is shown, never refused.
- **Changing your mind.** While a request waits, you change it or cancel it (a
  cancelled request stays in your history); to turn it into a registration — or the
  other way round — cancel it and enter the other. A registered absence — a sick one
  included — is HR's to change or remove: if your sykmelding ends early, tell HR.
- **Who decides.** Your manager, the Approver or HR approves or rejects your request; a
  rejection says why. Nobody approves their own: a manager's request goes to their own
  manager. Once approved, whoever decides your absence may cancel it until it begins,
  and after that only HR; HR corrects a decided or
  registered absence when the days were wrong. You are mailed when your request is
  decided, cancelled or corrected, and your manager when you ask for or register an
  absence — the mail never names a sickness, an HR-only type, a percentage or a note,
  and a registration reaches your manager only as "an absence".
- **Leave that reveals health or family matters** (an HR-only type, such as pregnancy
  leave) is decided by whoever holds the personal-data permission, not by your manager,
  and your manager is not mailed about it; they see it only once approved, as *leave*.
- **Deciding, for managers.** The requests you decide wait in your approvals inbox,
  oldest first — on [My team](#my-team), and on the [Absence page](#the-absence-page)
  for those who decide by right; the absence calendar shows a month of your team — or,
  with the directory, of everyone, where colleagues see only the days someone is away.
- **Your holiday account** counts in working days, Monday to Friday without public
  holidays: your entitlement under the company's holiday scheme (25 virkedager are
  21 working days; more from the year you turn 60; fewer when you start after
  30 September), what HR carried in from last year, what you have taken, what you have
  asked for and what remains. A request that would take you past what remains is still
  sent — the account shows it as overdrawn.

Who sees your absence: you, your manager and HR see the dates, the status, the type and
the details; colleagues with access to the directory see only the days you are away —
never whether it was a request or a registration, the type, a percentage or a note,
and your days run together into one span, so a sick day looks like a day off and a
sykmelding over a holiday is never seen. A visit of a few hours is not shown to
colleagues at all. A type that reveals health or family matters (such as pregnancy
leave or a religious holiday) shows your manager only *leave*, like any other leave —
an antenatal check-up, which is hours, is not shown to them at all — and its history is
not shown to them, so nothing in how it was entered gives it away.

## The dashboard

**Home**, the dashboard, has a **Staff** card for those with `staff:view` or
`staff:manage`: the number of people employed today, and below it the full-time
equivalents in force today, how many start and how many end within 30 days, how many
people are away today — on any approved or registered absence of whole or part days, whatever its type (a
visit of a few hours does not count) —
and, when there are any, the records with no employment at all. Press the card to open
Staff. With `staff:access` alone you have the Staff app under **Modules** and no card.
The open sick-leave cases and the sick-leave rate over the last twelve months are
read through the API by `staff:manage`, `staff:approve` and `staff:personal-data`
alone, never by `staff:view`.

Staff's items join the other apps' in **Needs attention**, each worded in your
language. Most are HR's (`staff:manage`); a manager sees the probation, the requests,
the sick-leave follow-up and the competence items for the people they manage,
`staff:approve` the requests, the follow-ups and the competences awaiting verification
of everyone, and the reassessment and the confidential
requests are `staff:personal-data`'s. A request of a confidential (HR-only) absence
type is counted only for `staff:personal-data`:

| Item | It means | Opens |
| --- | --- | --- |
| No signed contract | An employment started seven days ago — or today, for a hired-out person or a temporary employment agreed at the start to last under a month — with no signing date and no signed contract document filed for it. File the signed contract on the person's page, or type the date it was signed. | The person |
| Probation ending | The probation, or its extension, ends within 30 days. A manager sees this for the employments they manage. | The person |
| Temporary for a year | A temporary employment has lasted a year: the person is owed a month's written notice of when it ends. | The person |
| Temporary for three years | A run of temporary employments on ground a or b has lasted three years, and the person may have a right to a permanent position. | The person |
| No manager | Someone employed today has no manager in employment: none named, or the one named has left, and nobody in employment manages their department or one above it. A manager who has left is skipped: their people go to the next manager up, who also sees their probation items. | The person |
| Reassessment due | An erased person's last employment ended a year ago: record the reassessment. | The person |
| User in identity not erased | An erased person's user in identity could not yet be erased — still enabled, an Owner or a system administrator, no longer managed by Staff, or identity could not be reached. | The person |
| Documents not deleted | The document store has refused to delete documents for more than a day. | Staff settings |
| OTP threshold | By the percentages employed today, the company needs an occupational pension scheme — unless the only such person is the owner. | Staff settings |
| 5, 30 or more than 50 employees | The headcount has crossed a threshold that brings a duty: a whistleblowing routine and a safety representative at 5, a working environment committee at 30, the duty to report on equality above 50. | Staff settings |
| Absence requests await your decision | Requests you decide are waiting, counted. | Absence |
| Confidential requests waiting over a week | Requests of a confidential absence type have waited more than seven days — your own included, so the only holder of `staff:personal-data` sees their own request wait for someone who may decide it. | Absence |
| Sick-leave follow-up due | A follow-up of an open sick-leave case — the plan, a dialogue meeting, NAV's activity requirement — falls due within a week or has passed, and is neither done nor waived. A short case raises none. | The person |
| Holiday left from last year | In January: someone has holiday left from last year and no carry-over is recorded. Record the carry-over, or settle it. | The person |
| Too little holiday in the main period | From April to September: someone has fewer approved holiday days between 1 June and 30 September than their scheme's main period (three weeks under the Holiday Act). | The person |
| Approved their own absence | Someone approved their own absence, as the only decider there was. | The person |
| Sick during approved holiday | A 100 % sykmelding lies over approved ferie: the employee may demand to postpone it. | The person |
| … declared a competence awaiting verification | Someone added a competence — or renewed a certificate — themselves, and nobody has verified it yet. A manager sees their own people's; `staff:approve` and HR everyone's. Verify it, or remove it. | The person's Competence tab |
| A certificate of … expires soon | A certificate is within its reminder of its last valid day. A manager sees their own people's; HR everyone's. Press **Renewed** on it when the new one is there. | The person's Competence tab |
| A certificate of … has expired | A certificate's last valid day has passed. Renew it, or remove it if the person no longer needs it. | The person's Competence tab |
| The competence review of … is overdue | Someone in employment has had no competence review recorded within the interval set under [Competence reviews](#competence-reviews) — counted from their last review, or from the start of their current employment when that is later. A manager sees their own people's; HR everyone's. Go through the tab together and press **Reviewed today**. | The person's Competence tab |
| … does not meet their competence profile (2 items) | A competence profile that applies to someone active today asks for competences they do not meet — missing, below the level, a certificate declared but not verified, or expired — counted. One per person, for their manager and HR, never for the person themselves or for `staff:approve` alone. It goes away when every item is met or expires soon. | The person's Competence tab |
| The group … is not in sync with its directory | A group could not be kept in the directory — a setting the directory refused, a group there holding its key that Vantigo may not change, or a directory that could not be reached three times in a row; the group's page says why. One per group and directory, for HR. | The group |
| Microsoft Entra ID refused the photo of … | Entra ID would not take the person's picture — HR's photo, or the person's own when HR has set none. Nothing is needed for sign-in; set another photo to try again. For HR. | The person's Account tab |

An item goes away when what it asks for is done. A threshold's item stays until HR
acknowledges the threshold — once, for good — under [Thresholds](#thresholds) in the
Staff settings.

## Change the settings

**Staff settings** (`staff:manage`) has the general settings first, then the company's
structure, the document categories, four cards for competence — **Competence
categories**, **Competences**, **Competence profiles** and **Competence reviews** — three cards for absence — **Absence types**,
**Holiday schemes** and **Egenmelding and carry-over** — four cards for the data —
**Import and export people**, **Payroll export**, **Retention** and **Thresholds** — and
**Accounts** and **Directory profile**.
Someone without `staff:manage` who opens the page by its address sees none of them.

### General

- **Business time zone** — every date in Staff, "today" included, is a calendar day in
  this zone. It is shown, not changed, here: it is the workspace's, and an Owner
  changes it under [Workspace administration](/en/user/workspace-administration/#workspace-settings).
- **Employee number prefix** and **Next employee number** — a generated number is the
  prefix and the next number written together without padding: `E-` and 8 make `E-8`.
  The line below the fields shows the next number and its length. An employee number
  holds at most 16 characters, so a prefix and a number that together would make a
  longer one are refused on both fields before anything is saved. A company that wants
  `EMP007` sets the prefix to `EMP00` while that lasts.
- **Pension provider** — the occupational pension provider the contract check reads
  when a terms row names no social-security text.
- **Occupational injury insurer**.

Press **Save settings**. If somebody else saved the settings since you opened the page,
they are reloaded and the page says so; make your change again. The same goes for a
row of the structure: its form closes on the refreshed table.

### The structure

Four tables, each with an **Add** button, a pencil to change a row, and **Archive**:

- **Legal entities** — the juridisk enhet, with its organisation number (nine digits;
  spaces are ignored).
- **Business units** — the virksomhet the a-melding reports per, each listed with the
  legal entity it sits under.
- **Departments** — a code, a name, a **Manager** (a person in Staff: type a name or an
  employee number to find them), a **Parent department**, a **Default competence
  profile**, a cost centre and a division. A department's manager manages the people
  whose terms name the department and no manager of their own. Its default competence
  profile applies to everyone whose terms place them in the department today, beside
  the profiles assigned to them; the table shows it as **Default profile**. Clear it to
  leave the department without one.
- **Locations** — the workplace with its address; the country is Norway when left
  empty.

Exactly one legal entity and one business unit carry the **Default** marker; a new
employment goes in the default business unit when it names none. To move the default,
press **Make default** on another row — the marker leaves the old one in the same
step. The default itself has no switch to turn it off.

**Archive** asks first, then takes the row out of the pickers while it stays on
everything that already names it, marked **Archived**, with no actions left. Nothing is
deleted. Some archives are refused, and the message says why in the server's own
words — the default is never archived, for one: make another row the default first.
The rules are under [the structure](/en/reference/staff/#the-structure).

### Document categories

**Document categories** lists what a document can be filed under: the **Key**, the
**English name** and the **Norwegian name**, the **Class**, whether it is
**Restricted** — class E: its documents are health documents — and its status. The six
**Seeded** categories (contract, attest, certificate, correspondence, health and other)
never change and have no actions.

To add one of your own:

1. Press **Add category**.
2. Type a **Key** — 1 to 40 characters of a–z, 0–9 and hyphens — the **English name**
   and the **Norwegian name**.
3. Choose the **Class**. The form says whether the category will be restricted.
4. Press **Save**. The category is listed after the others and offered when a
   document is filed.

The key and the class cannot be changed later: the class decides when every document
in the category is erased. On a category of your own, the pencil changes the names,
**Archive** stops offering it for new documents, and **Restore** offers it again. A
category documents are filed under is not archived — the page says so: move them to
another category first.

### Competence categories

**Competence categories** lists the groups the competence catalogue is sorted in: the
**Key**, the **English name**, the **Norwegian name** and the status. The five
**Seeded** categories — Trade, Certificate, Language, Tool and Domain — never change and
have no actions.

To add one of your own:

1. Press **Add competence category**.
2. Type a **Key** — 1 to 40 characters of a–z, 0–9 and hyphens — the **English name**
   and the **Norwegian name**.
3. Press **Save**. The category is listed after the others.

The pencil changes a category's names, **Archive** stops offering it for new
competences, and **Restore** offers it again; the key never changes. A category a
competence that is not archived is filed under is not archived — the page says so:
move or archive the competence first.

### Competences

**Competences** is the catalogue people pick their competences from: each one's name,
**Category**, **Kind** — *Skill* or *Certificate* — whether it has a **Level**, its
**Validity (months)**, its **Reminder (days)**, **Who issues it** and its status:
**Seeded**, **Hidden** or **Archived**. The Norwegian certificates are seeded under
Certificate: the trade certificate, FSE, the HMS card, forklift, crane and earth-moving
machinery, documented safety training, scaffolding user and erector, the driving
licences B, C and D, code 95, hot work and work zone signage.

- **New competence** adds one of your own: the **Category**, a **Key** (1 to 60
  characters of a–z, 0–9 and hyphens), the **Name in English** and **Name in
  Norwegian**, the **Kind** — the key and the kind cannot be changed later — whether it
  **Has a level** (every row then carries one, from *aware* to *expert*) and, for a
  certificate, its **Validity (months)**, 1 to 240, left empty for one valid without
  end, and its **Reminder (days before expiry)**. Fill in **Who issues it** to show the
  person adding it who the issuer is. Press **Add**.
- **The reminder** is how many days before a certificate's last valid day its holder
  and their manager are emailed and the dashboard shows it as expiring; they are
  emailed again once it has expired. Left empty it is 30 days for a validity of up to
  12 months, 60 up to 24 and 180 beyond.
- The pencil changes a competence. A **seeded** one keeps its key, kind, level and
  validity, shown greyed out; its names, its reminder, its issuer and whether it is
  offered change. Whether one of your own has a level no longer changes once someone
  holds it — the page says so: add another competence instead.
- **Hide** stops offering a competence for new rows, and **Offer** brings it back —
  the way to retire a seeded certificate your company does not use. **Archive**
  retires one of your own, and **Restore** brings it back; one whose category is
  archived is restored only once its category is. Either way, everyone who holds the
  competence keeps it, shown by its name.

### Competence profiles

**Competence profiles** describes what a position needs: a named list of competences,
each with a minimum level when it has one and whether it must be valid. The table lists
each profile's **Key**, **English name** and **Norwegian name**, how many **Items** it
has and whether it is **Archived**.

To add one:

1. Press **New profile**.
2. Type a **Key** — 1 to 40 characters of a–z, 0–9 and hyphens; it cannot be changed
   later — the **Name in English** and the **Name in Norwegian**.
3. Under **Items**, press **Add item** for each competence the position needs and
   choose the **Competence**. If it has a level, choose the **Minimum level**; left
   empty, any level will do. For a certificate with a fixed validity, **Must be
   valid** — on by default — asks that it is valid today; turn it off when holding it is
   enough. Remove an item with its bin. A profile holds at most 50 items, each
   competence once.
4. Press **Add**.

The pencil changes a profile's names and items. An item you leave as it was stays even
if its competence has been archived since; a new or changed one must name a competence
that is offered. Turn on **Archived** and press **Save** to archive the profile: an
archived profile asks nothing of anyone until you restore it, by turning the switch off
again. It stays assigned to the people who had it, and a department keeps it as its
default, without effect while it is archived. If somebody else saved the profile since
you opened it, it is reloaded and the page says so: make your change again.

Assign a profile on a person's **Competence** tab with **Assign profiles**, or make it
a department's **Default competence profile** under [The structure](#the-structure).
The rules are under [profiles and gaps](/en/reference/staff/#profiles-and-gaps).

### Competence reviews

**Competence reviews** sets how often a manager goes through each person's competences
with them. **Review interval, months** is 0 to 60, 12 by default. A person in
employment with no review recorded within it — counted from the last time somebody
pressed **Reviewed today** on their tab, or from the start of their current employment
when that is later — becomes *The competence review of … is overdue* on the dashboard
of their manager and HR, and is listed under **Reviews overdue** on their manager's My
team. 0 turns the item off. Press **Save**. If somebody else saved the settings since
you opened the page, they are reloaded and the page says so: make your change again.

### Absence types

**Absence types** lists every type people may pick, and the hidden ones: its name, its
**Kind**, **Who sees the type** — *Manager and HR*, or *HR only* for a type that reveals
health or family matters — what it is **Reported in the a-melding as**, its rules
(**Needs a decision** or **Notification**, **Draws on the holiday account**, **May be
part of a day**, **Paid** or **Unpaid**) and whether it is **Seeded** or **Hidden**.

- **New absence type** adds one of your own: a **Key**, the **Name in English** and
  **Name in Norwegian**, the **Kind** and what it is **Counted in** — fixed from then on
  — **Who sees the type**, the a-melding description and the switches. Press **Add**.
- The pencil changes a type. A seeded type changes only its names, its **Order**,
  whether it is **Hidden** and whether it is **Paid**; its other rules are the statute's
  and shown greyed out. Turn **Paid** on for a seed when your company pays for that
  absence beyond what the law requires — the flag is for your records, and the payroll
  export does not read it.
- **Ammefri** (nursing time) is **Paid**, and stays so: it is paid within aml. § 12-8 (2)'s limits — up to one hour a day in the child's first year on a working day of seven hours or more; nursing time beyond that is unpaid by law unless agreed — register it with a company type of your own. **Antenatal check-up** (svangerskapskontroll) is **Paid** and stays so too: aml. § 12-1 gives paid time off for antenatal check-ups. The form
  says so, and refuses to save it unpaid.
- **Hide** stops offering a type for new absence; **Offer** brings it back. Absences
  that name it keep it.

A type of your own that is HR-only is not opened to colleagues while absences name it —
the form says so: add another type instead.

### Holiday schemes

**Holiday schemes** lists the company's holiday rules with what each comes to — "25
virkedager = 21 days on a five-day week" — and which is the **Default**, the scheme of
every employment that names none.

- **New holiday scheme**: the **Name**, **Entitlement (virkedager)**, **Extra from 60
  (virkedager)**, **Main period (virkedager)**, **After September (virkedager)** and
  **Most carried over (virkedager)**, each with what it comes to on a five-day week, and
  **The default**. The form holds the Holiday Act's floors: an entitlement of 25 to 60,
  a main period of at least 18, at least 6 for a start after September, and at most 12
  carried over plus the entitlement's days above 25.
- The pencil changes a scheme; what you do not touch is kept. **Archive** stops offering
  it, **Restore** brings it back — the default, or a scheme an employment follows, is
  not archived, and the page says so.
- An employment is put on a scheme on the person's **Employments** tab: **Edit
  employment**, **Holiday scheme**. *The default* follows whichever scheme is the default.

### Egenmelding and carry-over

Choose the **Policy**: *The statutory minimum (ftrl. § 8-23)* — 3 days at a time, 4
times in 12 months — *Extended (IA)* — 8 days at a time, 24 days a year — or *Custom
numbers*, with **Days per occurrence** (3 to 16), **Occurrences per year** (4 to 52) and
**Days per year** (12 to 365); the form refuses numbers below the law's floors. **Months
of employment first** (0 to 2) is how long someone must have worked before egenmelding.
**Carry untaken holiday over automatically** carries a year's remaining holiday into the
next without HR's action, from the year you turn it on. Press **Save**; the other
settings are kept as they are.

### Import and export people

The register travels as one semicolon-separated CSV file, the form a Norwegian Excel
opens and saves as it is. The columns and the rules for each are in
[the reference](/en/reference/staff/#the-registers-import-and-export).

- **Export people** downloads every person — erased ones too — with their work and
  private details and where each stands today, as `staff-people-<today>.csv`. The
  national identity number and the bank account are never in it, only whether a number
  is on file. Every export is recorded in the history with your name. A register larger
  than one file may hold is refused, and the page says so: the administrator can raise
  the bound.
- **Download template** downloads the header row alone, with every column you may
  import: the sealed columns are in it only when you also hold `staff:personal-data`.

To import:

1. Press **Import people**, then **Choose CSV file** — semicolon-separated, at most
   5 MiB. A larger file is refused on the spot. Every row needs its `employeeNumber`: a
   number no person has adds the person, a number a person has changes them.
2. Press **Check**. Every row runs and nothing is kept: the page shows the **Rows** and
   how many **Would be created**, **Would be updated** and **Have errors**, and under
   them **Problems in the file, by row**, each with its row, column and the server's own
   sentence. Fix the file and check again until you are happy.
3. Press **Import**. The same file runs for real; the rows with errors are skipped,
   and the page shows what was **Created**, **Updated** and what **Failed**. Keep the
   window open until it answers — it cannot be closed while the import runs.
4. When rows failed, press **Download failed rows**. The file holds those rows exactly
   as you wrote them, behind an `error` column saying what is wrong with each. Fix
   them and import that file as it is: the import ignores the `error` column.

A file is refused whole — nothing in it is imported — when it is comma-separated,
holds more rows than one import takes, or carries a column the import does not know
(the page names it). The **sealed columns** — `nationalIdKind`, `nationalIdCountry`,
`nationalId`, `bankAccountKind` and `bankAccount` — need `staff:personal-data` as well:
without it, a file carrying any of them is refused whole, and the page says so before
you choose a file. Remove those columns, or ask someone who holds the permission.

If the import stops before it answers — the connection drops, or the server fails —
the page says so: the rows imported before it stopped stay imported. Press **Check**
again, then **Import**; the rows already in count as updated and change nothing.

### Export a month for payroll

**Payroll export** hands a month's employments to your payroll system. It is shown to
HR who also hold `staff:personal-data`, since the file names HR-only leave by its
a-melding description.

1. Pick the **Month**. The month before today is chosen when you open the page.
2. Press **Export for payroll**. The file downloads as `staff-payroll-<YYYY-MM>.csv`.

The file has one row per employment that was in the month, with the terms in force on
the month's last day; the columns and the codes are in
[the reference](/en/reference/staff/#the-payroll-export). It carries no national
identity numbers and no bank accounts. Every export is recorded in the history with
your name and the file's SHA-256 hash, so the file payroll received can be recognised
later. The codes of the three appointment forms added in 2025 (hired out, and on call)
are not verified against Skatteetaten's code list — the card says so: check them with
your payroll system before you rely on them. Likewise, check the seven leave
descriptions the absence columns carry against the code list your payroll system
validates with.

To the right of the employment columns the file carries the month's absence: holiday,
egenmelding, sykmelding, child-sick (and the days of it beyond the employer's ten),
leave and other days, and the hours of time off. After an employment's row come its
**leave rows**, one per leave the a-melding reports — a leave longer than fourteen
days, shorter leaves that together pass fourteen days in twelve months, and every
permittering — with the leave's id (a code that stays the same every month), days,
description and percent. Holiday stays holiday in the file when the person is sick
during it, until you record their demand to postpone it.

### Retention

Staff keeps a former employee's record only as long as the law lets it, and erases the
rest on its own every hour; what each erase removes is in the
[administration guide](/en/admin/staff/#retention). **Retention** shows:

- **Grace period (days)** — how long after a person's last employment ended their
  personnel administration is erased: 0 to 3650 days, 90 unless changed.
- **Health documents (days)** — how long after the last employment ended their health
  documents are deleted: 30 to 3650 days, 365 unless changed. The same period after a
  sick-leave case closed erases its sick-leave data, whatever the employment ([the
  administration guide](/en/admin/staff/#retention)).

- **Erase pregnancy and health appointments** — off unless turned on. On, rows of
  svangerskapskontroll, pregnancy leave, birth leave and doctor or dentist visits
  become *Absent* the health period after the end of their year, their days and hours
  kept, and the history no longer says which type they were.
- **Erase a sick-leave case's dates and milestones** — off unless turned on. On, a
  closed case's erase removes the case itself: its dates, its milestones and its
  history; its absences stay as *Absent* days.

  Each switch is explained beneath it. Turned on, the next run erases everything
  already due; turning it off restores nothing ([the administration
  guide](/en/admin/staff/#erase-pregnancy-rows-and-sick-leave-cases-dates)).

  Change any of these and press **Save periods**. A period outside its bounds is
  refused on its field before anything is saved. If somebody else saved the settings
  since you opened the page, they are reloaded and the page says so.
- **Pending deletions** and **Failing deletions** — files the erases and the document
  deletes have queued for removal from the object store, and those the store has
  refused at least once. While any are failing, the card asks you to check that the
  object store is configured and reachable; they are tried again until it confirms.
- **Oldest pending since** and **Last erased** — when the oldest file was queued, and
  when an erase last ran (**Never** when none has).

**Run retention now** runs the hourly erase now, after asking: it erases for good what
is due, and cannot be undone. People under a legal hold are passed over. The card then
says what was erased — the personnel administration, health documents, pay and
contract, sick-leave cases, sick-child days, withdrawals of egenmelding and pregnancy
and health appointments, each with a count; the health ones only if you also hold the
personal-data permission — and how many files were deleted from the store. The
deletions stop after 30 seconds: what is not finished, the worker finishes on its next
look. While the worker is running already, the page says so: try again in a moment.

### Thresholds

**Thresholds** lists the four thresholds that bring the employer a duty — **5 or more
employees**, **30 or more employees**, **More than 50 employees** and **Occupational
pension (OTP)** — each with the duty in one line. While one is crossed and not
acknowledged, the dashboard shows an item for it. The headcount itself is on the
dashboard, not here.

When the duty is dealt with — or does not apply — press **Acknowledge** on its row and
confirm. An acknowledgement is **permanent**: it cannot be undone, and the threshold's
item never comes back, whatever the headcount does later. The row then says
**Acknowledged** with the date. A threshold can be acknowledged before it is crossed.

### Accounts

**Accounts** is how Staff gives people a user to sign in with — turned on per person on
their [Account](#account) tab. It lists each identity provider an installed module
offers with its status — *Microsoft Entra ID: connected.*, *Microsoft Entra ID: not
connected yet.* or *Microsoft Entra ID: failing — Staff writes nothing to the directory
but a disable until it is fixed.*, with the reason in a sentence; with none, *No
identity provider is offered: no enabled module provides one.* It also says whether an
Owner has named the roles Staff may give. Until one has, the card warns *No Owner has named the roles Staff may
give, so no local account can be enabled*, and **Open Roles & access** goes to
identity's **Roles & access** page, where an Owner names them under **Roles Staff
may give**.

- **Default account kind** — the account kind a new person gets: **Local**,
  **External identity provider** or, when the Entra module is installed, **Microsoft
  Entra ID**. People already registered keep theirs; change them one by one on their
  Account tab. On the upgrade that brought the kinds, it was set from the installation:
  Local, or External identity provider where single sign-on was on.
- **Prepare the account (days before)** — how many days before the account's day
  Staff creates the user, disabled: 0 to 90, 14 unless changed.
- **Delete an external account (days after the end)** — how many days after the last
  employment ended Staff deletes the person's account in Entra, once it is disabled and
  no legal hold is on the person: 1 to 3650, or empty for never. Entra keeps a deleted
  account restorable for thirty days. The same days after a [group](#groups) is
  archived, or Microsoft Entra ID turned off for it, the group is deleted in Entra — a
  security group is emptied and kept instead, and one Vantigo did not make is left, as
  [Groups](#groups) says; empty, it is renamed “… (archived)” and kept.

Press **Save the account settings**. A number outside its bounds is refused on its
field before anything is saved; the other settings are saved as they were.

**Run accounts now** runs one cycle of the accounts worker at once, for every account
that is due, as the worker would. A run may take minutes when identity or the mail
server is slow. The card then says, one figure each, how many accounts it worked, how many were done,
how many failed and are retried by the worker, and how many are left for the worker's
next cycle.

### Directory profile

**Directory profile** (`staff:manage`) chooses which fields Staff writes on a person's
account in the company's directory — Microsoft Entra ID — for every person of that
kind. Each field has a switch:

- On by default: **Hire date**, **Leave date**, **Employee type**, **Job title**,
  **Department**, **Company name** (the legal entity), **Cost centre and division**,
  **Office location**, **Preferred language** and **Manager**.
- Off by default: **Work address** (the location's street, postal code, city and
  country) and **Phones** (the work phone and the mobile).

The names, the employee number, the user principal name and the usage location are
always written, as the card says; they have no switch, and each person's Account tab
lists them greyed. Private contact details are never written.

1. Turn the switches on or off.
2. Press **Save the directory profile**. A notice says *The directory profile is
   saved*; if somebody else saved the settings since you opened them, they are reloaded
   instead and the notice says *Somebody else saved this; it has been reloaded*.

A field you turn on is written at each person's next update. A field you turn off is no
longer written: Staff stops sending it, and the directory keeps the value it last held.
Each person's Account tab shows the result as their directory profile.
