---
title: Staff
description: Enabling the Staff module, giving people access to it, and its settings — the business time zone and the employee number sequence.
sidebar:
  order: 44
sources:
  - apps/server/internal/staff/module.go
  - apps/server/internal/staff/settings.go
---

Staff is the company's register of the people who work for it: their records, their
employments and the company's structure. This page is the operator's side: turning
the module on, letting people in, and the settings. What the module does and the rules
it checks are in [the reference](/en/reference/staff/).

## Before you start

- Staff needs no other business module. `MODULES` unset enables it with every other
  module; an installation that lists `MODULES` adds `staff` to the list, alone if it
  likes (`MODULES=staff`).
- The migration creates the `staff` schema whether or not the module is enabled, so
  turning it on later needs no migration. If the database runs a least-privilege
  application role, it needs the `staff` schema granted like every other
  ([Installation](/en/admin/installation/)).

## Enable the module

1. Add `staff` to `MODULES` in `vantigo.env`, or leave `MODULES` unset.
2. Restart Vantigo (`docker compose up -d`).
3. Verify: `GET /api/v1/staff/meta` answers 401 without a session rather than the 404
   a disabled module's path answers.

A disabled module contributes no route and no permission; its data stays in the
database untouched.

## Give people access

Every Staff operation requires `staff:access`, and four permissions sit above it:

| Permission | For |
| --- | --- |
| `staff:access` | Everyone who should see their own record. |
| `staff:view` | Colleagues who should see the directory and the company's structure. |
| `staff:approve` | Whoever decides absence (it grants nothing until absence arrives). |
| `staff:manage` | HR: people, employments, the structure and the settings. Sensitive. |
| `staff:personal-data` | Whoever may reveal national identity numbers and bank accounts. Sensitive. |

The Owner holds them all already. Everyone else needs a role that grants them: under
**Workspace admin → Roles & access**, create or edit a role and give it the
permissions in the "Staff" category
([Manage roles and access](/en/user/workspace-administration/#manage-roles-and-access)).
No built-in role holds any of them.

## The settings

The settings are `staff:manage`'s, read and replaced through `GET` and
`PUT /api/v1/staff/settings`; the Staff app's Settings page arrives with the rest of
the module. A replace carries the revision it read in `If-Match`, so two people
changing the settings at once cannot overwrite each other unseen.

- **Business time zone** (`timeZone`, default `Europe/Oslo`). Every date in Staff —
  a start, an end, "today" — is a calendar day in this zone. Use a city zone such as
  `Europe/Oslo`: a name the server and the database read differently, such as `CET`,
  is refused.
- **Employee number prefix and next number** (`employeeNumberPrefix`, empty, and
  `nextEmployeeNumber`, 1). A person created without an employee number gets the
  prefix and the next number, unpadded: `EMP-` and 7 make `EMP-7`. The prefix is up
  to eight letters, digits or hyphens, and the two together may make at most 16
  characters.
- **Pension provider** and **injury insurer** (`pensionProvider`,
  `injuryInsurer`). The occupational pension (OTP) provider and the occupational
  injury insurer, which the employment contract check reads.

## What can go wrong

- **`timeZone` refused.** The name is unknown, or the server and the database
  disagree about it in winter or in summer. Choose the city zone the abbreviation
  stands for.
- **`employee_number_too_long`.** The prefix and the next number together exceed 16
  characters. Shorten the prefix, or lower the next number.
- **`revision_conflict`.** Somebody changed the settings since you read them. Read them
  again and repeat the change.
