---
title: Staff
description: The people register — People, New person and the Staff settings with the company's structure.
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

Press a name to open the person's page.

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
