---
title: Staff
description: The people register — People, a person's page, New person, My page and the Staff settings with the company's structure.
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

The sidebar shows the areas your permissions give you:

| Area in the sidebar | Who sees it |
| --- | --- |
| **My page** | everyone with `staff:access` |
| **People** | `staff:view`, or `staff:manage` — an HR role without `staff:view` still lists people and reads the structure |
| **Staff settings** | `staff:manage` |

What a screen offers follows what the server says you may do: the **New person**
button, the **Status** filter and the status column are there for `staff:manage` only.
The full table is under [Permissions](/en/reference/staff/#permissions).

## Find someone in People

**People** lists the company's people by name, with their employee number, department,
manager and work email. The department and the manager are the ones in the terms in
force today; a dash means there is none.

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

### Status, for HR

With `staff:manage` you also see the **Status** control and a status column:

- **Current** (the default) lists those active today, those starting later and those
  with **No employment** yet — the person saved in the first step of
  [New person](#add-a-new-person) whose employment is still to come.
- **Ended** lists those whose every employment ended before today.
- **All** lists everyone.

The column says **Active**, **Starts on** with the first day, **Ended on** with the
last day worked, or **No employment**. "Today" is the business time zone's day
(see [Change the settings](#change-the-settings)), so the list turns over at midnight
there, not in your browser.

Press a name to open [the person's page](#a-persons-page).

## A person's page

A person's page has three tabs — **Profile**, **Employments** and **History** — and the
tab you are on is in the page's address. It shows what you may see of the person and
nothing else: a group of fields that is not yours to read is not on the page at all,
not even as an empty heading. The person themselves, a manager of theirs, a colleague
holding `staff:view` and HR each see a different page; the full table is under
[field groups and audiences](/en/reference/staff/#field-groups-and-audiences). A person
you may not see answers exactly as one who does not exist.

### Profile

- **Work** — the names, the employee number, the work email and phone, the department
  and the manager in force today, the language and the work country. Everyone who may
  open the page sees it.
- **Private** — the birth date, gender, mobile and private phone, private email and home
  address. The person and HR.
- **HR's notes** — HR alone (`staff:manage`, `staff:personal-data`).
- **Identifiers** — the national identity number as **On file** or **Not on file**, with
  its kind, and the bank account as **Ending in** and its last four digits. The page
  never shows more than that, except through **Reveal** below.
- **Next of kin** and **Roles** (Safety representative, AMU member, HSE responsible,
  each with the days they hold it).
- **Self-service** — the user the person signs in as, if HR has linked one.
- **Organisation** — **Managers** up the chain from the person's manager today, and
  the **Direct reports**. Press a name to open their page.

A manager sees the work group, the roles and the organisation of the people they
manage; a colleague the same of anyone active today.

#### Change the details (HR)

1. Press **Edit details** in the Work group. The form holds the work group, the private
   group and HR's notes. The employee number is shown but never changes.
2. Change what you need and press **Save**. A refusal lands on its field — a work email
   another person has, for one.

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

With `staff:personal-data`, the Identifiers group offers **Reveal** and **Set**:

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

With `staff:manage` the card and its rows offer:

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
  number never changes.
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
never stops a save. The person, a manager of theirs and HR see it. See
[the § 14-6 check](/en/reference/staff/#terms) for where each item is read from.

### History

**History** lists every change to the person, newest first and paged: what changed,
who changed it and when. A field shows its value before and after (*Work email:
old → new*); a field of a group you may not read only that it changed (*Birth date
changed*); a change of the pay is left out for a manager, so a pay correction shows as
the kind of change alone. The person, a manager of theirs and HR see the History tab;
a colleague does not.

## Add a new person

With `staff:manage`, press **New person** in People. The form has two steps, as a hire
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

**Add it later** closes the form with the person saved and opens their page.

## My page

**My page** (`staff:access`) is your own record in Staff, found through the user HR
linked it to, and shows what you see of yourself — whatever else you may hold. It has
the same three tabs as a person's page:

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

If HR has not linked your user to a record yet, My page says *You have no record in
Staff yet* and explains how it comes to be; ask HR if you think yours is missing.

My page is made for a phone first: on a narrow screen everything is one column and the
form takes the whole screen.

## Change the settings

**Staff settings** (`staff:manage`) has the general settings first, then the company's
structure.

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
