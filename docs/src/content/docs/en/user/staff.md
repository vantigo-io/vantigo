---
title: Staff
description: The people register — People, a person's page with its documents, the legal hold, an erased record and its rehire, New person, My page, the Absence calendar and approvals, absence and the holiday account, and the Staff settings with the company's structure, the document categories, the register's import and export, the payroll export, retention and the thresholds.
sidebar:
  order: 45
sources:
  - apps/staff/frontend
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

A person's page has five tabs — **Profile**, **Employments**, **Absence**, **History** and **Documents** — and the
tab you are on is in the page's address. It shows what you may see of the person and
nothing else: a group of fields that is not yours to read is not on the page at all,
not even as an empty heading. The person themselves, a manager of theirs, a colleague
holding `staff:view` and HR each see a different page; the full table is under
[field groups and audiences](/en/reference/staff/#field-groups-and-audiences). A person
you may not see answers exactly as one who does not exist.

### Profile

- **Work** — the names, the employee number, the work email and phone, the title, the
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
- **Self-service** — the user the person signs in as, if HR has linked one.
- **Children and care days** — **Children under 12**, **Sole carer** and **Children
  with extra care days**, what the person's care days are counted from. The person, HR
  and `staff:personal-data`, and only when one of them is recorded.
- **Organisation** — **Managers** up the chain from the person's manager today, and
  the **Direct reports**. Press a name to open their page.

A manager sees the work group, the roles and the organisation of the people they
manage; a colleague the same of anyone active today. A manager who has left manages
nobody: their people go to the department's manager, or the next one up.

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
  **Business unit**, the kind of relationship and **Contract signed**. The employment
  number never changes. The employment's holiday scheme is not on the form yet; saving
  keeps it as it is ([holiday schemes](/en/admin/staff/#holiday-schemes)).
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
- **Care days** for the year, to the person, HR and `staff:personal-data`.
- **Sick-leave case from** *date* — the open case: its last day so far, the 52-week end,
  the milestones and the accommodation note. A manager, an Approver and HR **Mark done**
  and **Waive** a milestone; the manager and HR **Change the accommodation note** (an
  Approver is not shown it). HR also has **Change the estimate** — or **Reset to first
  day + 364** — and **Close the case**. A milestone's note, and why one was waived, are
  shown to `staff:personal-data` only, who has **Change the note** on each milestone.
- **Absences** — the person's rows that touch the year shown, as you may see them, a
  page at a time when there are many. A type that reveals health or
  family matters shows a manager and HR without `staff:personal-data` as an approved
  *Leave*, with nothing to change. HR has **Register absence** — a sykmelding the
  person brings, say — and on a row:
  - **Change** on a request or a registration (a sykmelding that ended early),
  - **Correct** on an approved absence whose days or type were wrong,
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

### History

**History** lists every change to the person, newest first and paged: what changed,
who changed it and when. A field shows its value before and after (*Work email:
old → new*); a field of a group you may not read only that it changed (*Birth date
changed*); a change of the pay is left out for a manager, so a pay correction shows as
the kind of change alone, marked *(details not visible to you)*. The person, a manager of theirs and HR see the History tab;
a colleague does not.

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
the [retention run](#retention) erases their personnel administration. HR
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

## My page

**My page** (`staff:access`) is your own record in Staff, found through the user HR
linked it to, and shows what you see of yourself — whatever else you may hold. It has
a person's four tabs, then **Absence** and, when you manage anyone, **My team**:

- **Profile** — your work details, your private details, your national identity number
  as **On file** and your bank account's last four digits, your next of kin, your roles
  and where you sit in the organisation. Press **Edit my details** to change your
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
  record, your next of kin, roles, employments and terms, your documents' details and
  your history — without the values HR keeps sealed (your national identity number and
  bank account in full) and without HR's notes. The file lists your documents without
  their contents: download each from the list. Downloading your data records nothing.
- **Absence** — your holiday account, your egenmelding standing, your care days and
  your absences, with **Request absence** (see
  [Request absence](#request-absence-from-your-phone) below).
- **My team** — only when you manage someone today: the requests you decide, your
  reports' absence this month and their open sick-leave cases (see
  [My team](#my-team) below).

If HR has not linked your user to a record yet, My page says *You have no record in
Staff yet* and explains how it comes to be; ask HR if you think yours is missing.

My page is made for a phone first: on a narrow screen everything is one column, the
form takes the whole screen and the request sheet opens from the bottom.

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
says so.

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
permissions: with the directory, HR's, the Approver's or the personal-data permission,
everyone employed in the month; a manager without those, their reports and themselves.

Each bar shows only what you may see of it: the type and its status, a percentage of
the day or the hours for HR and the person's manager; *Leave* for a type that reveals
health or family matters, once it is approved; and *Absent* — the days alone, without
a type or a status — to a colleague who sees the directory. Point at a bar, or read it
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
of the days, the request cannot be approved as it stands.

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
  approved, so you cannot ask twice for the same day.
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
  the employer's sixteen days, the follow-up plan at four weeks, the dialogue meetings
  at seven and twenty-six, NAV's activity requirement at eight — is your manager's and
  HR's to mark; you see it, and the accommodation notes, but never a diagnosis: there
  is nowhere to write one.
- **Care days** for a sick child count against your days for the year — 10 for one or
  two children, 15 for three or more, more as a sole carer or with a chronically ill
  child — from the details HR has recorded. Going over is shown, never refused.
- **Changing your mind.** While a request waits, you change it, remove it or cancel it
  (a cancelled request stays in your history); to turn it into a registration — or
  the other way round — remove it and enter the other. A registered absence — a sick one
  included — is HR's to change or remove: if your sykmelding ends early, tell HR.
- **Who decides.** Your manager — or, if you have none, whoever decides absence for
  everyone — approves or rejects your request; a rejection says why. Nobody approves
  their own: a manager's request goes to their own manager. Once approved, only the one
  who decided it (or HR, once it has begun) cancels it; HR corrects a decided or
  registered absence when the days were wrong. You are mailed when your request is
  decided, cancelled or corrected, and your manager when you ask for or register an
  absence — the mail never names a sickness, an HR-only type, a percentage or a note.
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
never whether it was a request or a registration, the type, a percentage, hours or a
note, so a sick day looks like a day off. A type that reveals health or family matters
(such as pregnancy leave, an antenatal check-up or a religious holiday) shows your
manager only *leave*, without its hours, like any other leave — and its history is not
shown to them at all, so nothing in how it was entered gives it away.

## The dashboard

**Home**, the dashboard, has a **Staff** card for those with `staff:view` or
`staff:manage`: the number of people employed today, and below it the full-time
equivalents in force today, how many start and how many end within 30 days, how many
people are away today — on any approved or registered absence, whatever its type —
and, when there are any, the records with no employment at all. Press the card to open
Staff. With `staff:access` alone you have the Staff app under **Modules** and no card.
The open sick-leave cases and the sick-leave rate over the last twelve months are
read through the API by `staff:manage`, `staff:approve` and `staff:personal-data`
alone, never by `staff:view`.

Staff's items join the other apps' in **Needs attention**, each worded in your
language. Most are HR's (`staff:manage`); a manager sees the probation, the requests
and the sick-leave follow-up items for the people they manage, `staff:approve` the
requests and the follow-ups of everyone, and the reassessment and the confidential
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

An item goes away when what it asks for is done. A threshold's item stays until HR
acknowledges the threshold — once, for good — under [Thresholds](#thresholds) in the
Staff settings.

## Change the settings

**Staff settings** (`staff:manage`) has the general settings first, then the company's
structure, the document categories, three cards for absence — **Absence types**,
**Holiday schemes** and **Egenmelding and carry-over** — and four cards for the data:
**Import and export people**, **Payroll export**, **Retention** and **Thresholds**.
Someone without `staff:manage` who opens the page by its address sees none of them.

### General

- **Business time zone** — every date in Staff, "today" included, is a calendar day in
  this zone.
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
  employee number to find them), a **Parent department**, a cost centre and a
  division. A department's manager manages the people whose terms name the department
  and no manager of their own.
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

### Absence types

**Absence types** lists every type people may pick, and the hidden ones: its name, its
**Kind**, **Who sees the type** — *Manager and HR*, or *HR only* for a type that reveals
health or family matters — what it is **Reported in the a-melding as**, its rules
(**Needs a decision** or **Notification**, **Draws on the holiday account**, **May be
part of a day**, **Paid** or **Unpaid**) and whether it is **Seeded** or **Hidden**.

- **New absence type** adds one of your own: a **Key**, the **Name in English** and
  **Name in Norwegian**, the **Kind** and what it is **Counted in** — fixed from then on
  — **Who sees the type**, the a-melding description and the switches. Press **Add**.
- The pencil changes a type. A seeded type changes only its names, its **Order** and
  whether it is **Hidden**; its rules are the statute's and shown greyed out.
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

**Payroll export** hands a month's employments to your payroll system.

1. Pick the **Month**. The month before today is chosen when you open the page.
2. Press **Export for payroll**. The file downloads as `staff-payroll-<YYYY-MM>.csv`.

The file has one row per employment that was in the month, with the terms in force on
the month's last day; the columns and the codes are in
[the reference](/en/reference/staff/#the-payroll-export). It carries no national
identity numbers and no bank accounts. Every export is recorded in the history with
your name and the file's SHA-256 hash, so the file payroll received can be recognised
later. The codes of the three appointment forms added in 2025 (hired out, and on call)
are not verified against Skatteetaten's code list — the card says so: check them with
your payroll system before you rely on them.

To the right of the employment columns the file carries the month's absence: holiday,
egenmelding, sykmelding, child-sick (and the days of it beyond the employer's ten),
leave and other days, and the hours of time off. After an employment's row come its
**leave rows**, one per leave the a-melding reports — a leave longer than fourteen
days, shorter leaves that together pass fourteen days in twelve months, and every
permittering — with the leave's id, days, description and percent.

### Retention

Staff keeps a former employee's record only as long as the law lets it, and erases the
rest on its own every hour; what each erase removes is in the
[administration guide](/en/admin/staff/#retention). **Retention** shows:

- **Grace period (days)** — how long after a person's last employment ended their
  personnel administration is erased: 0 to 3650 days, 90 unless changed.
- **Health documents (days)** — how long after the last employment ended their health
  documents are deleted: 30 to 3650 days, 365 unless changed.

  Change either and press **Save periods**. A period outside its bounds is refused on
  its field before anything is saved. If somebody else saved the settings since you
  opened the page, they are reloaded and the page says so.
- **Pending deletions** and **Failing deletions** — files the erases and the document
  deletes have queued for removal from the object store, and those the store has
  refused at least once. While any are failing, the card asks you to check that the
  object store is configured and reachable; they are tried again until it confirms.
- **Oldest pending since** and **Last erased** — when the oldest file was queued, and
  when an erase last ran (**Never** when none has).

**Run retention now** runs the hourly erase now, after asking: it erases for good what
is due, and cannot be undone. People under a legal hold are passed over. The card then
says what was erased — the personnel administration, health documents, and pay and
contract, each with a count — and how many files were deleted from the store. The
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
