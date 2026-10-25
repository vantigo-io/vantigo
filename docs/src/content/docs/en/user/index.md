---
title: Finding your way around
description: Apps, the sidebar, the dashboard, search and your account.
sidebar:
  order: 0
  label: Overview
sources:
  - apps/host/frontend
---

Vantigo gathers the modules a business needs into one application. Which modules you
see depends on two things: which ones your organisation has switched on, and which
permissions your roles give you. A module you have no permission for is simply not
shown.

## Apps and the switcher

Each module is an **app** with its own sidebar: Customers, Projects, Time, Calendar,
Expenses, Staff, Invoices, Communications, Products and Energy — and Microsoft Entra ID,
where an administrator connects the company's tenant. The app switcher in the top bar lists
the apps you can open; **Home** is the dashboard, which opens with Calendar's **Today and
this week** agenda when you use Calendar ([the dashboard](/en/user/calendar/#the-dashboard)),
and where every app you use shows its own card with the numbers that need your attention — Staff's, the people employed today
with the full-time equivalents, who starts and ends soon and who is away today, for
`staff:view` or `staff:manage` ([the dashboard](/en/user/staff/#the-dashboard)). Its **Needs attention**
list gathers the apps' items that wait for someone — Invoices' among them: overdue
invoices, money owed back on an invoice, and, with `invoices:payments`, bank lines in the
exception queue, reminders that failed or wait for a rate or a review, print batches
not confirmed posted, each with its printed letters counted, and Vipps payments that
need a person — whose outcome is unknown, that could not be taken, that are stuck, or
that were refunded in the Vipps portal — each leading to its invoice
([the dashboard card](/en/user/invoices/#the-dashboard-card)); and Staff's: contracts
not signed, probations ending, temporary employments past a year or three, people
without a manager, reassessments due, documents the store will not delete, the
headcount and pension thresholds not yet acknowledged, and the absence items: requests
awaiting your decision, sick-leave follow-ups due, holiday left from last year or too
little holiday in the main period, a self-approved absence and sickness during approved
holiday — for a holder of `staff:personal-data` also an HR-only absence the only such
holder approved for themselves, which opens that person's Absence tab; the competence
items, for a manager their own people's and for HR everyone's: a competence declared
and awaiting verification, a certificate expiring or expired, a competence review
overdue and a competence profile not met, each opening the person's Competence tab; and, for
`staff:manage`, the accounts: each person whose account Staff cannot create, enable or
keep up to date, saying why — no employment or no address to give it from, an account
an Owner disabled, stopped managing or that belongs to someone else, a number or an
address another account holds, invitations unanswered or revoked, attempts that keep
failing — which opens the person's Account tab, and the accounts that wait on the
installation (no roles Staff may give, a role among them that grants administration,
a sign-in mode the worker disagrees with), which open Staff's settings, and each group
not in sync with its directory, which opens the group's page.

Inside an app, the sidebar lists its areas. Many of them are only shown to people who
hold the matching permission — and a few, such as Invoices' **Quick invoice**, only to
people who hold several permissions together — so a colleague's sidebar may be longer or
shorter than yours:

| App | Areas |
| --- | --- |
| [Customers](/en/user/customers/) | Customers, Contacts, Follow-ups |
| [Projects](/en/user/projects/) | Projects, My tasks, Project economy |
| [Time](/en/user/time/) | My week, Approvals, People, Time settings |
| [Calendar](/en/user/calendar/) | Calendar (your own, and a colleague's with the picker), My calendar (your calendar on your phone, and calendars from elsewhere) |
| [Expenses](/en/user/expenses/) | My expenses, Expense approvals, Reimbursements, Expense settings |
| [Staff](/en/user/staff/) | My page (its **Absence** and **Competence** tabs and, for a manager, **My team**), Staff directory, Absence (the calendar and the approvals), Competence (the matrix and, for HR, the interests), Groups, Staff settings |
| [Invoices](/en/user/invoices/) | Invoices, Quick invoice, Invoice journal, Overdue, Payments, Invoice settings |
| [Communications](/en/user/communications/) | Inbox, Channels, Suppressions |
| [Products](/en/user/products/) | Products, Categories, Tax categories |
| [Energy](/en/user/energy/) | Metering points |
| [Microsoft Entra ID](/en/user/entra/) | Microsoft Entra ID (the connection's status and its six setup steps), for `entra:manage` |

A few screens belong to an area without a sidebar entry of their own, and are opened
from a button in that area's header instead — **Import customers**, for example, from
**Import** on the customer list ([Export and
import](/en/user/customers/#export-and-import)), and **Import metering points** from
**Import** on the metering-point list ([Import metering
points](/en/user/energy/#import-metering-points)).

## Search

The search box in the top bar (or the keyboard shortcut shown in it) opens a spotlight
that jumps to any screen you have access to, so you rarely need the switcher for a
screen you know the name of.

## Your account

The avatar menu at the top right leads to **Settings**, with your profile (name,
language) and your security (password, multi-factor authentication, passkeys).
**Help** in the same menu opens this documentation, in your language, as the copy
built into the Vantigo you are using, so it always describes the version you have.

If you are an owner of the workspace, the same menu leads to **Workspace
administration**: users, invitations, and roles and access. A system administrator
additionally sees **System administration**.

Signing in, invitations, passwords, multi-factor authentication and passkeys are covered in [Getting started](/en/user/getting-started/); users, invitations and roles in [Workspace administration](/en/user/workspace-administration/).

## Language

Vantigo speaks English and Norwegian. The language follows your browser unless you
choose one under Settings → Profile; dates, numbers and currency follow the language.
