---
title: Staff
description: Enabling the Staff module, giving people access to it, linking people to their users, the personal-data permission, the bank-change mail, and the settings — the business time zone and the employee number sequence.
sidebar:
  order: 44
sources:
  - apps/server/internal/staff/module.go
  - apps/server/internal/staff/settings.go
  - apps/server/internal/staff/sealed.go
  - apps/server/internal/staff/links.go
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

## Link people to their users

A person sees their own record — My page in the Staff app, `GET /api/v1/staff/me` —
only once HR has linked the person to the user they sign in as. The link makes that
user the person themselves to Staff and nothing else: it grants no permission, and it
never puts the user under Staff's management. Any active user can be linked, the Owner
included.

1. As a user with `staff:manage`, find the user: `GET /api/v1/staff/assignable-users?q=`
   with part of their name lists up to twenty active users.
2. Link them: `PUT /api/v1/staff/people/{id}/user` with `{"userId": "<the user's id>"}`.
   To unlink, send `{"userId": null}`.
3. Verify: the person signs in and `GET /api/v1/staff/me` answers their record rather
   than `no_person`. The person's history shows `user_linked`.

A user can be linked to one person only, and a person to one user; linking a person to
another user replaces the link. A disabled user cannot be linked.

## The personal-data permission

The national identity number and the bank account are sealed: stored encrypted, shown
to nobody — not to HR holding `staff:manage`, not to the person — except as the number's
kind and the account's last four digits. Only `staff:personal-data` writes them and
reveals them, and every reveal is recorded in the person's history with who looked.
Give `staff:personal-data` to as few people as payroll needs; it is a separate role from
HR's on purpose, so that changing where someone's pay goes takes a second pair of hands.

The values are sealed under keys derived from `APP_SECRET`. Rotating it makes them
unopenable and the national identity numbers' duplicate check blind — see
[Installation](/en/admin/installation/#the-settings-you-must-decide-on).

## The bank-change mail

When a bank account is set or changed, the person is mailed — at their private email
when they have one, else at their work email, in their preferred language — naming the
account's last four digits and nothing more, so a change somebody else made does not go
unnoticed. The mail goes through the installation's own SMTP server after the change is
saved.

- It needs `MAIL_DRIVER=smtp` and the `SMTP_*` settings
  ([Installation](/en/admin/installation/#email-and-observability)). With any other
  driver the mail is skipped, and the server logs
  `staff: bank-account change mail skipped: the mail driver is not smtp`.
- A person with neither email is not mailed, and the server logs
  `staff: bank-account change mail skipped: the person has no email`.
- A send that fails is logged as a warning naming the person's id; the change itself
  stays saved.

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
- **`user_inactive`.** The user you are linking is disabled. Enable them first, or link
  another.
- **`user_linked_elsewhere`.** The user is linked to another person. Unlink them there
  first.
- **`no_person`.** The person asking for My page is linked to nobody. Link them.
- **`national_id_duplicate`.** Another person already has this national identity
  number. The refusal does not say who; someone with `staff:personal-data` can compare
  by revealing.
- **No bank-change mail arrived.** Check the log for the skip line above, and that the
  person has an email.
