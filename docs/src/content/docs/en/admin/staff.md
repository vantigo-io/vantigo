---
title: Staff
description: Enabling the Staff module, giving people access to it, linking people to their users, turning on accounts for a person and the accounts worker, the personal-data permission and health documents, the document categories, answering an innsyn request, a legal hold, rehiring an erased person, importing and exporting the register, exporting a month for payroll, retention — the erase and the deletion ledger — the headcount and pension thresholds, absence types and their a-melding mapping, holiday schemes, the self-certification policy and automatic carry-over, carrying holiday over, correcting a registered sick absence, withdrawing egenmelding, following a sick-leave case, the care-day fields and care days, who decides absence and the absence mails, the bank-change mail, and the settings — the business time zone, the employee number sequence, the retention periods and the account settings.
sidebar:
  order: 44
sources:
  - apps/server/internal/staff/module.go
  - apps/server/internal/staff/settings.go
  - apps/server/internal/staff/sealed.go
  - apps/server/internal/staff/links.go
  - apps/server/internal/staff/document_categories.go
  - apps/server/internal/staff/personal_data.go
  - apps/server/internal/staff/legal_hold.go
  - apps/server/internal/staff/erased.go
  - apps/server/internal/staff/people_import.go
  - apps/server/internal/staff/people_export.go
  - apps/server/internal/staff/csvfile.go
  - apps/server/internal/staff/payroll.go
  - apps/server/internal/staff/retention.go
  - apps/server/internal/staff/retention_worker.go
  - apps/server/internal/staff/object_deletions.go
  - apps/server/internal/staff/thresholds.go
  - apps/server/internal/staff/absence_types.go
  - apps/server/internal/staff/holiday_schemes.go
  - apps/server/internal/staff/absence_settings.go
  - apps/server/internal/staff/adjustments.go
  - apps/server/internal/staff/deciders.go
  - apps/server/internal/staff/notifications.go
  - apps/server/internal/staff/cases.go
  - apps/server/internal/staff/sickness.go
  - apps/server/internal/staff/care_days.go
  - apps/server/internal/staff/account_flag.go
  - apps/server/internal/staff/account_settings.go
  - apps/server/internal/staff/accounts_decide.go
  - apps/server/internal/staff/accounts_worker.go
  - apps/server/internal/staff/accounts_run.go
  - apps/server/internal/staff/accounts_attention.go
  - apps/server/internal/staff/holders.go
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
- People's documents are kept in the object store, so filing one needs
  `STORAGE_PROVIDER=fs` and a storage root
  ([Object storage](/en/admin/object-storage/)). Without it everything else works, and
  an upload or a download answers 503 `storage_unavailable`.

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
| `staff:approve` | Whoever decides everyone's absence: they approve, reject and cancel requests, see every person's work details and absences (an HR-only type as leave), the approvals inbox and the calendar, and open sick-leave cases (their milestones, not the notes) — no pay, no private details, no history. |
| `staff:manage` | HR: people, employments, documents other than health documents, the document categories, the structure and the settings. Sensitive. |
| `staff:personal-data` | Whoever may reveal national identity numbers and bank accounts, and read and file health documents. Sensitive. |

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

1. As a user with `staff:manage`, open the person's page. Under **Self-service** on the
   Profile tab, type part of the user's name in **User** — up to twenty active users
   are offered — choose them and press **Link**
   ([the user guide](/en/user/staff/#link-the-person-to-their-user-hr)). **Unlink**
   removes the link. Through the API: `GET /api/v1/staff/assignable-users?q=`, then
   `PUT /api/v1/staff/people/{id}/user` with `{"userId": "<the user's id>"}`, or
   `{"userId": null}` to unlink.
2. Verify: the group reads *… — linked, not managed by Staff*; the person signs in and
   My page shows their record (`GET /api/v1/staff/me` answers it rather than
   `no_person`). The person's history shows `user_linked`.

A user can be linked to one person only, and a person to one user; linking a person to
another user replaces the link. A disabled user cannot be linked.

A user Staff's accounts manage is not linked or unlinked here: Self-service names such
a link *… — managed by Staff; see the Account tab* and offers neither **Link** nor
**Unlink**, and the request answers `link_managed` (409), which the page says in words
— as it does for a person whose link is the accounts worker's (the person's Account tab
shows its state). If identity cannot be asked whether the user is managed, the request
answers `accounts_unavailable` (503), the page says identity could not be reached, and
nothing is changed; try again. An installation where no module provides accounts answers
every link and unlink naming a user that way.

## Turn on accounts for a person

Staff can give a person a user in identity, prepared before they start and enabled on
their first day (delivery C). The account flag is set here; the accounts worker
([below](#the-accounts-worker)) acts on it.

1. Before the first account, an Owner names the roles Staff may give in identity (the
   employee roles, under **Roles Staff may give** on the Roles and access page). The
   **Accounts** card in Staff settings warns *No Owner has named the roles Staff may
   give* until one has, with **Open Roles and access**, and says the authority mode:
   *Local accounts* (identity holds the passwords) or *Federated* (an identity provider
   owns the accounts) — or *Accounts are unavailable* when this process's OIDC or SCIM
   settings disagree with the API's, or identity could not be asked. Through the API,
   `GET /api/v1/staff/meta` answers `provisioningRolesSet` and `accountMode` to
   `staff:manage`.
2. As a user with `staff:manage`, open the person's **Account** tab
   (`/staff/people/<id>?tab=account`). Under **Account flag**, turn on **Staff provides
   an account**.
3. Leave **Account from** empty to enable the account on the first day of the person's
   earliest employment that has not ended — the field says which day that is — or
   choose another day. Between two employments a day from before the first one ended
   is that one's, and the account follows the later start; a day after it is honoured.
   Choose **Private email** under **Address** to give identity the person's private
   email instead of the work email. Press **Save the account flag**. Through the API
   it is `PUT /api/v1/staff/people/{id}/account` with `If-Match` set to the flag's own
   `revision` from `GET` — never the person's: changing the flag never moves the
   person's revision — and all three fields, `{"provisionAccount": true,
   "provisionFrom": null, "accountEmailKind": "work"}`; one without
   `provisionAccount`, or with it `null`, is refused (400) and changes nothing.
4. Verify: **Account from** names the day the account is enabled
   (`provisionFromEffective`), and the tab shows no warning that the person *has no
   work email on file* (`address.present` is `true`). A flag turned on without the
   chosen address is accepted, but no account is made until the person has it. The
   person's history shows `account_flag_updated`.

How many days before that day the account is prepared, disabled, is **Prepare the
account (days before)** on the Accounts card (`createBeforeDays`, [The
settings](#the-settings)). An erased person's flag cannot be changed (`person_erased`):
the tab offers HR only **Disable now** on an erased record. Someone else saving the flag
first answers `revision_conflict`: the tab reloads and says so.

### The accounts worker

The worker `staff-accounts` acts on the flag every `STAFF_WORKERS_POLL` (default `1h`)
— in worker mode, and in api mode with `WORKERS_IN_PROCESS=1`
([Installation](/en/admin/installation/)) — unless `STAFF_ACCOUNTS_ENABLED=0`. For a
person whose flag is on it:

- `createBeforeDays` before the effective day, creates the user in identity, pending
  and disabled, and links it (the link's `state` reads `pending`);
- on the day, enables the user under the employee roles and mails them an invitation
  to choose a password; the link reads `active` once they have accepted it;
- the day after the last employment ends, disables the user and ends their sessions
  (`disabled`); a rehire enables them again on the new start;
- keeps the user's name, language and employee number in step with the person — and
  the address only while the user is still pending.

Each person is acted on once a day, at the first poll after midnight in the business
time zone. Several workers, or a worker and a run by hand, share the work person by
person; none waits for another.

1. Precondition: an Owner has named the employee roles (`provisioningRolesSet` is
   `true`). Without them the worker still creates the pending, disabled users of the
   people whose flag is on, but never enables one (`employee_role_missing`).
2. Verify, the day after the worker has run: the person's Account tab, under **User
   account**, shows the **State** in words, why the link is held when it is, *The last
   … attempts failed* after a failure, the **Invitation** and the **Next attempt**
   (`GET /api/v1/staff/people/{id}/account` answers the same as a `link` with its
   `state`, `reason`, `attempts` and `nextAttemptAt`). The person's history (as HR) shows `account_prepared`,
   `account_activated`, `account_deactivated` and `account_updated`; a manager, an
   Approver and a colleague see none of them.
3. To stop it: `STAFF_ACCOUNTS_ENABLED=0` and restart. **Nothing is then done on a
   schedule, leavers included**: a user whose employment ended keeps their access
   until the worker runs again or an Owner disables them in identity. Turning the
   `staff` module off has the same effect, and also strands pending users — prepared,
   never invited — whom an Owner then finishes or deletes in identity.

**Federated mode** (an identity provider owns the accounts): the worker never creates
or invites anyone. It finds the user the provider created by the employee number, links
it, re-enables a user it disabled and disables leavers; until the provider has created
the user the link is held with `not_found` and tried again daily. A pending user made
in local mode before the switch is held with `mode_changed` for an Owner.

**The mode mismatch.** The worker's process must have the same OIDC and SCIM settings
as the API's ([Installation](/en/admin/installation/)). When they differ, the worker
still disables leavers but holds everything else: the links read
`reason: mode_mismatch`, and the log says so. Align the settings and restart; the held
links are tried again at the next poll.

**A held link** (`reason` set; the Account tab says each reason in a sentence) is tried
again every day, so it clears by itself once its
cause is gone. The reasons an operator meets: `employee_role_missing` and
`employee_role_unsafe` (an Owner names, or fixes, the employee roles),
`account_email_taken` and `employee_number_taken` (another user in identity holds the
address or the number), `blocked_by_owner` and `disabled_by_owner` (an Owner disabled
the user; only an Owner undoes it), `account_privileged` (the user is an Owner or a
SystemAdmin, handled by hand), `account_not_managed` (an Owner stopped Staff managing
the user), `mismatch` (the user holds another employee number: someone else's account),
`user_gone` (the user was deleted in identity), `invitation_revoked` and
`invitation_unanswered` (an Owner's revoke, or three invitations unanswered). A link
whose `attempts` reach three could not reach identity: it is tried daily from then on,
and the log names the failure — but a leaver's deactivation is tried at every poll for
as long as it fails, so the leaver loses access as soon as identity answers.

### Run accounts now

The worker can be run by hand, for everyone or for one person, with you as the actor
identity records and the history names.

- **Everyone**: **Run accounts now** on the **Accounts** card in Staff settings
  (`POST /api/v1/staff/accounts/run`, `staff:manage`) runs one cycle now; the card then
  says how many accounts it worked, how many were done, how many failed and how many are
  left (`{attempted, done, failed, remaining}`): the links it worked, those identity
  answered (a step carried out, or a reason recorded), those it could not reach identity
  for (retried by the worker), and the due links it left. It begins no link after
  sixty seconds and takes at most fifty; `remaining` is for the worker's next cycle, or
  run it again. It runs beside the worker's own cycles without waiting for them.
- **One person**: **Run now** on the person's Account tab
  (`POST /api/v1/staff/people/{id}/account/run`) works the person now, whatever their
  next attempt, and asks identity for a **fresh invitation**: a pending user whose
  invitation an Owner revoked, or who left three unanswered (`invitation_revoked`,
  `invitation_unanswered`), is invited again. The tab says *Done: a new invitation was
  sent* or *Done: no invitation was sent* (`{invitationSent, account}`). Identity never
  re-sends an invitation that can still be accepted and sends none within ten minutes of
  the last; the run is done all the same — try again after ten minutes if one is needed.
  A person whose flag is off has nothing run, and the tab offers no Run now.

What can go wrong, each said in words on the tab or the card: `accounts_busy` (409) —
the worker is working the person right now; try again in a few minutes. `person_erased` (409) — an erased person's account is not
run. `accounts_unavailable` (503) — no module provides accounts, or identity could not
be reached; the link records the error and the worker retries.

A run goes on when the browser or a proxy gives up waiting. The realistic worst case is
several minutes — each call to identity may wait up to two minutes, and identity mails
the invitations — so when identity or the mail server is slow the request can time out
in the browser while the work still finishes: read the Account tab, or the dashboard,
again afterwards rather than running it twice.

### Disable an account now

**Disable now** on the person's Account tab
(`POST /api/v1/staff/people/{id}/account/disable`, `staff:manage`) **turns the person's
account flag off** and disables their user at once, so the worker leaves it disabled
until HR turns the flag on again.

1. Press **Disable now** and confirm — the dialog says *This also turns the account flag
   off*. The flag is turned off first (the history shows
   `account_flag_updated`) together with a note on the link that a disable was asked
   for, then identity disables the user (`account_deactivated`) and ends their
   sessions. The person's own revision does not move. If the browser gives up waiting,
   the disable still goes on; whatever does not reach identity, the worker finishes at
   its next polls.
2. Verify: the tab says *The account is disabled and the account flag is off*, **Staff
   provides an account** is off and the **State** reads *Disabled by Staff* (the answer
   has `provisionAccount: false` and the link `state: disabled`). A person with no link, or whose user is not created yet, only has
   the flag turned off.
3. What can go wrong — the tab says each in words, and those after the flag's write say
   that the flag is already off:
   - `accounts_busy` (409): the worker holds the person's link. Usually nothing was
     changed — try again in a few minutes. If the worker took the link in the instant
     after the flag was written, **the flag is already off**: the worker then finishes
     the disable by itself.
   - `account_not_managed` (409): the person is linked by hand to a user Staff does not
     manage; `account_privileged` (409): the user is an Owner or a SystemAdmin. The flag
     is off, but the user stays enabled: an Owner disables them in identity, or adopts
     the user for Staff again (or demotes them). Staff tries the disable again daily —
     so once an Owner has re-adopted or demoted the user, the next try disables them —
     and the dashboard shows *accountNotManaged* or *accountPrivileged* until it is done
     or the user is disabled in identity.
   - `account_mismatch` (409): the user the link names carries another employee number
     — someone else's account — and nothing is done to them. The flag is off; the link
     reads `mismatch` and the dashboard shows *accountMismatch*. Put the person's
     employee number right: the next daily try then disables the user.
   - `accounts_unavailable` (503): identity could not be reached. **The flag is already
     off**, but the user is still enabled: the link records the error, the worker
     retries the disable at every poll, and the dashboard shows *accountFailing* until
     identity answers. Send the request again if it persists.

An erased person's account can be disabled too: on an erased record the Account tab
offers HR **Disable now** and nothing else.

### The account items on the dashboard

`staff:manage` sees one item per person on the dashboard when the worker is held for
them, read from Staff's own records (identity is not asked when the dashboard loads).
Each names the person and nothing more; open their Account tab for the details. Only
people whose flag is on and who are not erased are listed, except *accountFailing* for a
disable asked for that has not reached identity — for an erased person too, titled "An
erased person".

| Item | What it means | What to do |
| --- | --- | --- |
| `accountNoEmployment` | The flag is on but the person has no employment | Add the employment, or turn the flag off |
| `accountEmailMissing` | The person has no address of the kind the flag names | Add the work (or private) email, or switch the address choice |
| `accountNotFound` | Federated: the identity provider has not created the user yet | Create the user in the provider with the person's employee number |
| `accountNotManaged` | An Owner stopped Staff managing the user — or, with the flag off, a Disable now refused for a user Staff does not manage, tried again daily | An Owner adopts the user again (the next try disables them) or disables them in identity; HR unlinks a self-service link |
| `accountBlockedByOwner` | An Owner disabled the pending user | An Owner enables them in identity, if they should start |
| `accountDisabledByOwner` | An Owner disabled the user while the person is employed | An Owner decides; Staff does not undo it |
| `accountUserGone` | The user was deleted in identity | Unlink, or an Owner restores the account |
| `accountPrivileged` | The user is an Owner or a SystemAdmin — also after a Disable now refused for them, tried again daily | An Owner disables them in identity, or demotes them (the next try disables them) |
| `accountMismatch` | The user carries another employee number: someone else's account — also after a Disable now that did nothing for it | Check the person's employee number; once it is right the disable runs |
| `accountModeChanged` | A pending user made in local mode, now under federated mode | An Owner finishes or deletes the user |
| `accountEmailDiffers` | The user's address in identity differs from the person's | An Owner changes it in identity (Staff changes it only while pending) |
| `accountEmailTaken` | Another user in identity holds the address | Resolve the duplicate in identity, or change the person's address |
| `accountNumberTaken` | A user Staff does not manage holds the employee number | An Owner adopts that user for Staff, or resolves the number |
| `accountFailing` (flag on) | Identity could not be reached three times in a row | Check identity's health and the log, then Run now |
| `accountFailing` (flag off, or an erased person) | A Disable now has not reached identity — failed, or waiting more than ten minutes | The worker retries at every poll and stops once identity answers (a user found disabled already counts); send Disable now again if it persists |
| `invitationUnanswered` | Three invitations went unanswered | Check the address; Run now sends a fresh one |
| `invitationRevoked` | An Owner revoked the invitation | Run now sends a fresh one, if the person should have it |
| `employeeRoleMissing` | No employee role named (one item, with a count) | An Owner names the roles Staff may give |
| `employeeRoleUnsafe` | A role named for Staff grants administration (one item) | An Owner takes that key out of the role, or the role out of the set |
| `accountModeMismatch` | A worker's OIDC or SCIM settings differ from the API's (one item) | Align the settings and restart the worker |

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

**Health documents** — a person's documents in a restricted category, `health` among
the seeds — are `staff:personal-data`'s too. HR holding only `staff:manage` does not see
them in a person's archive, cannot open, file, move or remove one, and does not see
that one was added in the person's history; the person themselves reads their own.
Give `staff:personal-data` to whoever handles sick notes and occupational-health
correspondence, and nobody else.

## Document categories

Every document is filed under a category, and the category decides when the document
is erased: its **retention class** — A for pay and contract documentation (five years
after the year the employment ended), D for personnel administration (with the rest of
the person's record), E for health. Six categories are there from the start: contract
and attest (A), certificate, correspondence and other (D), and health (E). A category
of class E is restricted: its documents are health documents.

The table under **Staff settings** → **Document categories** does all of this on screen
([the user guide](/en/user/staff/#document-categories)). Through the API, to add one of
your own, as a user with `staff:manage`:

1. `POST /api/v1/staff/document-categories` with a `key` (1–40 of `a–z`, `0–9` and
   hyphens), `names` in English and Norwegian and a `class` of `A`, `D` or `E`, for
   example `{"key": "courses", "names": {"en": "Courses", "nb": "Kurs"}, "class": "D"}`.
   Choose the class carefully: it never changes, because the erase reads it from every
   document filed under the category.
2. Verify: `GET /api/v1/staff/document-categories` lists it after the seeds.

To rename one, or stop offering it, send its `names` and `archived` to
`PUT /api/v1/staff/document-categories/{id}` with its revision in `If-Match`. An
archived category is offered for no new document, and only a category no document is
filed under can be archived. The six seeds never change.

## Absence types

Absence is entered in a vocabulary of types: holiday (ferie), egenmelding, sykmelding,
the statutory leaves, avspasering, a doctor's or dentist's appointment and the rest —
22 seeds that every installation starts with, each mapped to the a-melding leave
description it is reported as ([the reference](/en/reference/staff/#absence-types)).

On the screen, as a user with `staff:manage`: open **Staff settings** and find
**Absence types**.

1. The table lists every type: its name, its **Kind**, **Who sees the type** (*Manager
   and HR*, or *HR only*), what it is **Reported in the a-melding as**, its rules and
   whether it is **Seeded** or **Hidden**. The four reserved types the erases write
   (*Absent*, *Absent (hours)* and their two HR-only counterparts) are not listed.
2. **New absence type** adds one of your own: a **Key**, the **Name in English** and
   **Name in Norwegian**, the **Kind** and what it is **Counted in** (both fixed from
   then on), who sees it, the a-melding description, and the switches **Needs a
   decision**, **Paid**, **Draws on the holiday account** and **May be a percentage of
   the day** (the last two for a type counted in days). Press **Add**.
3. The pencil (**Change** *type*) opens a type. A seed changes only its names, its
   **Order**, whether it is **Hidden** and whether it is **Paid** — its other fields are
   shown greyed out; a type of your own changes everything but its key, kind and unit.
   **Ammefri** stays paid: the form says why and refuses to save it unpaid.
4. **Hide** stops offering a type for new absence (absences that name it keep it);
   **Offer** brings it back. Every field you did not change is sent back as it was.
5. A refusal is said in the form: a change to a seed's rules ("only its names, its
   order, whether it is hidden and whether it is paid"), ammefri set unpaid, or opening
   an HR-only type to colleagues while absences name it.

Through the API, as a user with `staff:manage`:

1. Read the list: `GET /api/v1/staff/absence-types` answers every type, the hidden ones
   too for you.
2. To add one of your own, `POST /api/v1/staff/absence-types` with a `key` (1–40 of
   `a–z`, `0–9` and underscores), `names` in English and Norwegian, a `kind` —
   `holiday`, `leave`, `time_off` or `other` — a `unit` (`days` or `hours`), the flags
   `paid`, `drawsHoliday`, `allowsPercent` and `needsDecision`, a `visibility`
   (`absent`, or `hr_only` for a type that reveals special-category data) and, for a
   leave, the `ameldingLeave` it is reported as. The kind and the unit never change.
3. To rename a type, re-order it or stop offering it, send it back to
   `PUT /api/v1/staff/absence-types/{id}` with `hidden` and `sort` as you want them and
   its revision in `If-Match`. A seed changes only its names, its place, whether it is
   hidden and `paid`; anything else — and `ammefri` with `paid: false` — is refused
   `seed_immutable`. A type of your own may be made
   HR-only at any time, but once someone has taken it as HR-only it cannot be opened to
   colleagues and managers (`absence_type_in_use`): add another type instead. Making a
   type in use HR-only reaches back: from then on its rows already entered show
   managers only *leave*, and their requests, refusals and history are hidden from them
   too. An HR-only type of your own neither allows a percentage nor draws on the
   holiday account: the form refuses either, since a share or a holiday deduction would
   tell managers what kind of leave it is.
4. Verify: `GET /api/v1/staff/meta` lists the types people may pick — the hidden ones
   left out.

**Mark a seed paid when you pay beyond the law.** The seeds say what the law pays: a
company whose agreement pays, say, foreldrepermisjon or omsorgspermisjon turns **Paid**
on for that seed, and off again if the agreement ends. The flag is informational — no
figure reads it, the payroll export included, which reports days and hours by kind —
so it tells HR and payroll what the company has decided, nothing more. **Ammefri** is
paid by law and stays paid: aml. § 12-8 (2) gives pay for up to one hour a day in the
child's first year on a working day of seven hours or more. Pay beyond that — a longer
break, or after the first year — is the company's; mark it with a type of your own or
in the absence's note.

**Choose the a-melding mapping by the leave's legal basis.** The a-melding accepts six
leave descriptions and permittering. Welfare leave is agreed, not statutory, so the
`velferdspermisjon` seed reports "other non-statutory leave"
(`andreIkkeLovfestedePermisjoner`); if your welfare leave rests on a statute, use the
`annen_lovfestet` type for it instead. The codes `velferdspermisjon` and
`utdanningspermisjon` expired at the end of 2022 and are refused. Only leave of more
than fourteen days is reported — and several shorter leaves within twelve months that
together pass fourteen days count together — and care days for a sick child never are.

## Holiday schemes

A holiday scheme is the company's holiday rule, in virkedager (Monday to Saturday, as
the Holiday Act counts). The statutory scheme — 25 days, 6 more from 60, 18 in the main
period, 6 for a start after 30 September, 12 days' agreed carry-over — is there from the
start and is the default. Each scheme is answered with its figures converted to working
days of a five-day week: 25 virkedager are 21 days, 30 are 25.

To give some or all employees more, as a user with `staff:manage`, on the screen:

1. Open **Staff settings**, find **Holiday schemes** and press **New holiday scheme**.
   The table shows each scheme's **Entitlement** and **Most carried over** as
   "30 virkedager = 25 days on a five-day week", and which is the **Default**.
2. Fill in the **Name**, **Entitlement (virkedager)**, **Extra from 60 (virkedager)**,
   **Main period (virkedager)**, **After September (virkedager)** and **Most carried
   over (virkedager)**; each shows what it comes to on a five-day week. The form holds
   the Holiday Act's floors before anything is sent. Turn on **The default** to make it
   the scheme of every employment that names none. Press **Add**.
3. Put an employment on it: on the person's **Employments** tab, **Edit employment**
   and choose the **Holiday scheme**.
4. **Archive** stops offering a scheme; **Restore** brings it back. Archiving the
   default, or a scheme an employment follows, is refused in words.

Through the API:

1. `POST /api/v1/staff/holiday-schemes` with a `name` and the figures, for example
   `{"name": "Five weeks", "entitlementVirkedager": 30, "extraFrom60Virkedager": 6}`
   (the main period, the after-September days and the carry-over default to 18, 6 and
   12). The Holiday Act's floors hold: at least 18 days in the main period, at least 6
   for a start after 30 September, and an agreed carry-over of at most 12 days plus the
   days above 25 — 17 for a 30-day scheme. Add `"isDefault": true` to make it the scheme of every employment that names
   none.
2. Put an employment on it: `holidaySchemeId` on `POST /api/v1/staff/people/{id}/employments`
   or `PUT /api/v1/staff/employments/{id}`. Left out, the employment follows the default.
3. Verify: `GET /api/v1/staff/holiday-schemes` lists it, with `converted`.

To stop offering a scheme, send it to `PUT /api/v1/staff/holiday-schemes/{id}` with
`"archived": true`. The default cannot be archived, nor a scheme an employment follows
(`holiday_scheme_in_use`): move the employments first. There is always exactly one
default; make another scheme the default to move it.

## The self-certification policy

How much egenmelding an employee may use is the installation's policy. On the screen:
open **Staff settings**, find **Egenmelding and carry-over**, choose the **Policy** —
*The statutory minimum (ftrl. § 8-23)*, *Extended (IA)* or *Custom numbers* — and, for
custom numbers, the **Days per occurrence**, **Occurrences per year** and **Days per
year**; the form refuses numbers below the statutory floors before anything is sent.
Set the **Months of employment first** and **Carry untaken holiday over automatically**
in the same card, and press **Save**. The card sends the general settings back as they
are, so nothing else changes.

Through the API, on `PUT /api/v1/staff/settings` (`staff:manage`):

- `selfCertPolicy: "minimum"` — the statutory rule: 3 calendar days at a time, 4 times
  in twelve months. The default.
- `selfCertPolicy: "extended"` — 8 calendar days at a time, 24 days in twelve months,
  as an inclusive-workplace agreement gives.
- `selfCertPolicy: "custom"` with `selfCertDaysPerOccurrence` (3–16),
  `selfCertOccurrencesPerYear` (4–52) and `selfCertDaysPerYear` (12–365). The floors
  are the statutory minimum (ftrl. § 8-23), which no policy goes below.

Under a preset the numbers are the preset's, whatever you send. `selfCertAfterMonths`
(0–2, default 2) is the months of employment before egenmelding may be used, under any
policy. The policy is applied to every egenmelding as it is registered: one past what
it allows is refused and the employee is told to bring a sykmelding. An occurrence is
the days in a row, a weekend or public holiday between two egenmeldinger counting as
days of it — under the minimum, egenmelding Friday and again Monday is four days. Days
a sykmelding covers between two egenmeldinger join them too, as one illness, without
counting as self-certified days.

**Automatic carry-over.** `autoCarryOver: true` carries the holiday left at a year's
end into the next year without HR's action, as a forced carry-over. The day you turn
it on is recorded (`autoCarryOverSince`), and no year before it is carried over
automatically. It is off by default.

## Carry holiday over

Holiday taken in advance of the year it is earned in (*forskuddsferie*, up to 12
virkedager by written agreement, ferieloven § 7 nr. 3) is not modelled: the account records no advance. When one is agreed, record it
as a **Correction** adjustment with a note, in the year it is taken and back in the
year it is earned.

Holiday left at a year's end does not vanish. HR carries it into the next year. On the
screen: open the person's **Absence** tab, page to the year with **Previous year**, and
press **Carry over from** *year*. Choose the **Kind** — *Agreed* or *Forced* (with a
**Note**) — leave **Days** empty for what remains, and press **Carry over**. A second
carry-over from the same year is refused in words. **Holiday adjustments** on the same
tab lists the carry-overs and corrections; **Add adjustment** records a correction (with
a note) and the bin deletes one.

Through the API, on `POST /api/v1/staff/people/{id}/carry-over` (`staff:manage`):

1. Read the person's account for the year it comes from:
   `GET /api/v1/staff/people/{id}/holiday-account?year=2025` — `remaining` is what is
   left.
2. Record the carry-over with `fromYear` and `kind`:
   - `agreed` — what you and the employee agreed, capped by the holiday scheme's
     maximum (12 virkedager, 10 working days, under the statutory scheme). Leave `days`
     out to carry the remaining days, up to the cap.
   - `forced` — days the employee could not take (sickness in the holiday, say),
     uncapped, with a `note` saying why.
3. Check the account of the next year: the days are in `carriedIn`.

A person has **at most one carry-over from a year**: a second answers
`carry_over_recorded`. To change it, delete it
(`DELETE /api/v1/staff/holiday-adjustments/{id}`; the list is
`GET /api/v1/staff/people/{id}/holiday-adjustments`) and record it again. A correction
of an account — a day registered wrongly, say — is an adjustment with
`reason: "correction"` and a note; an adjustment that is a carry-over names the year
just before the one it changes.

**Automatic carry-over.** With `autoCarryOver` on in the settings, an account carries
the year before's remaining days in by itself (`carriedInDerived`), as a forced
carry-over — but only into a year whose 1 January is on or after the day it was turned
on: turned on 12 September 2026, it first carries 2026's days into 2027. It never
reaches into a year already under way, whose carry-in HR settled by hand. A carry-over recorded by hand for a
year replaces the automatic one.

## Correct a registered sick absence

An egenmelding or a sykmelding is registered, not decided, and the employee cannot
change it once it is in. On the person's **Absence** tab, a registered or requested row
has **Change**, an approved one **Correct**, and **Register absence** enters a
sykmelding for the person; the sheet sends the token the row carries, and if somebody
changed the row meanwhile it closes and reloads rather than overwrite. Through the API:
when a sykmelding ends early, or a day was wrong, HR changes
it (`PUT /api/v1/staff/absences/{id}`, `staff:manage`, with the revision in `If-Match`)
or removes it (`DELETE /api/v1/staff/absences/{id}`). Each is recorded in the person's
history. A change keeps the row a registration: changing a sykmelding into ferie, say,
is refused (`absence_status_change`) — remove it and enter the ferie. Absence writes
are `staff:manage`'s; `staff:personal-data` alone does not write them. An absence somebody has decided is never removed; it is cancelled or corrected
(`POST /api/v1/staff/absences/{id}/correct`). A change to a sick absence derives the
person's sick-leave case again — its days, its arbeidsgiverperiode and its milestones'
dates — and keeps what was marked on the milestones.

## Withdraw egenmelding

When an employee has used egenmelding in a way that gives reason to (ftrl. § 8-27), HR
may withdraw the right for a period. On the screen: the person's **Absence** tab, the
**Egenmelding** card, **Withdraw egenmelding** — **Until** and **Reason** — then
**Withdraw**; **Lift the withdrawal** ends it early. Through the API:

1. `PUT /api/v1/staff/people/{id}/self-certification/withdrawal` (`staff:manage`) with
   `until` — a day after today, at most a year ahead — and the `reason`.
2. Check `GET /api/v1/staff/people/{id}/self-certification`: `withdrawnUntil` and
   `withdrawnReason` are there.
3. From today until that day, an egenmelding for the person is refused
   (`self_certification_withdrawn`); they register a sykmelding instead. Egenmelding for
   days before you recorded the withdrawal still stands, as a row of its own: one that
   runs into the withdrawal is refused whole. Extending a withdrawal still in
   force keeps the day you first recorded it.

To lift it early, send `{"until": null}`. The withdrawal and its lifting are in the
person's history for the person, HR and `staff:personal-data` — never for the manager,
who reads the person's egenmelding standing without it.

## Follow a sick-leave case

Every egenmelding and sykmelding belongs to a sick-leave case, which Staff keeps by
itself: a sick absence that starts no more than sixteen days after the last one joins
its case, and one that starts later opens a new one. `GET /api/v1/staff/cases` lists
the open ones you follow — the person's manager sees their reports', HR and whoever
holds `staff:approve` everyone's. On the screen the open case is a card on the person's
**Absence** tab (and, for a manager, on **My team**): **Mark done** and **Waive** on each
milestone, **Change the accommodation note**, and for HR **Change the estimate** (with
**Reset to first day + 364**) and **Close the case**; a holder of `staff:personal-data`
also has **Change the note** on each milestone. A health document is filed under the
case from the person's **Documents** tab: change the document and choose its
**Sick-leave case**.

1. **The arbeidsgiverperiode.** The milestone `employer_period_end` is the case's
   sixteenth sick day, weekends and public holidays between two sick absences counted,
   the count starting on a working day (a sick absence from a Saturday counts from
   the Monday); until then it has no date. The employer pays those days when the employee has
   worked for it four weeks.
2. **The follow-up.** The follow-up plan is due four weeks after the first day, the
   first dialogue meeting at seven, NAV's activity requirement at eight and the second
   dialogue meeting at twenty-six. A milestone is shown as due only while the case is
   open and has lasted to within a week of it, so a short sickness raises nothing.
   Mark one done (`POST /api/v1/staff/cases/{id}/milestones/{kind}/done`, today unless
   you send `doneAt`, never before the case's first day) or waive it as plainly
   unnecessary with a reason (`.../waive`) — never a diagnosis; only the employee and
   `staff:personal-data` read the reason. The manager, HR and `staff:approve` may. The
   end of the arbeidsgiverperiode follows from the sick days and is not marked.
3. **Notes.** The accommodation note (`PUT /api/v1/staff/cases/{id}`) is for
   tilrettelegging — what the employee can and cannot do — and never a diagnosis; the
   manager and HR write it. A milestone's own note is `staff:personal-data`'s, and only
   the employee and `staff:personal-data` read it. A health document is filed under the
   case with `caseId` on `PUT /api/v1/staff/documents/{id}`.
4. **The 52-week end** is estimated as the first day plus 364 days; HR sets NAV's date
   with `maxBenefitEstimate` and sends it as null to return to the estimate. A field you
   leave out of `PUT /api/v1/staff/cases/{id}` is kept. NAV counts the 248 sykepenger
   days across sicknesses unless the employee has been fit for work for 26 weeks
   between (ftrl. § 8-12), so an earlier case can bring the end forward.
5. **The close.** A case closes by itself sixteen days after its last day. HR closes it
   earlier with `POST /api/v1/staff/cases/{id}/close`; a sick absence that joins it
   later opens it again.

When a sick absence fills the gap between two cases, the two become one: the older
keeps its id and takes over the other's marked milestones and notes, appended after its
own. A note that no longer fits is not cut: the survivor's ends with a pointer, and the
other case's notes are kept whole in the case's history.

A sykmelding over an egenmelding shortens the egenmelding, or removes it when it covers
it whole.

## Postpone holiday for sickness

A sykmelding over approved ferie does not change the ferie by itself: whether the
holiday is postponed is the employee's demand, with a legeerklæring (ferieloven § 9).
Meanwhile your dashboard lists it: *… was on sick leave during approved holiday*. On the
screen, the sykmelding's row on the person's **Absence** tab has **Postpone holiday**:
set **Demanded on** and press **Record the demand**; a refusal is said in the dialog.

1. Check the sykmelding is 100 %: a graded one gives no right to postpone.
2. When the employee demands it, record the demand on the sykmelding:
   `POST /api/v1/staff/absences/{id}/postpone-holiday` (`staff:manage`) with
   `demandedOn`, the day they demanded it (today when left out).
3. Staff splits the ferie around the sykmelding and the days return to the holiday
   account; the answer shows the ferie as it now stands, and the item leaves the
   dashboard.

Sickness that began **before** the holiday must have been demanded by the last working
day before it; a later demand is refused (`holiday_postponement_too_late`), and the
dashboard stops listing such a holiday once it has begun. Sickness **during** the
holiday gives replacement days; the employee is to demand them without undue delay
once back at work. A demand is never dated before the sykmelding's first day, and the
law sets no least number of sick days. A sykmelding over
a ferie that is only requested leaves the request; its approval is then refused.

## The care-day fields

A person's care days (omsorgsdager) are derived from three fields on their record,
which HR types from what the employee declares — no child is named:

- **Children under 12** (`childrenUnder12`) — the children through the year they turn
  twelve who do **not** have NAV's decision of extra care days.
- **Chronically ill or disabled children** (`chronicallyIllChildren`) — the children
  **with** NAV's decision of extra care days, through the year they turn eighteen.
- **Sole carer** (`soleCarer`) — whether the employee cares for the children alone.

Count each child once: a child with NAV's decision goes in the second field only. On
the screen they are **Children under 12**, **Sole carer** and **Children with extra
care days** in **Edit details** on the person's **Profile**, and the profile shows them
as **Children and care days**; a field left empty is not recorded. The fields are
written with the rest of the person (`PUT /api/v1/staff/people/{id}`); the employee
reads them on their own record but cannot change them.

The counter is `GET /api/v1/staff/people/{id}/care-days?year` (the employee, HR and
`staff:personal-data`): 10 days for one or two children, 15 for three or more, 20 and
30 for a sole carer, and 10 more per chronically ill or disabled child (20 for a sole
carer). The year's sick-child working days are counted against it; the first ten are
the employer's own, the rest are refunded by NAV — from the first day when the only
children who qualify are over twelve with extra days, which payroll applies by hand.
More than the entitlement is shown, never refused; the days counted as refunded stop at
the entitlement when the fields are recorded, in the counter and in the payroll file. The right needs four weeks'
employment — counted from the start of an unbroken run of employments, one starting
the day after another ended continuing it: before that the answer says
`eligible: false` and from which day, and a sick-child day before it is counted apart
(`ineligibleDays`), not as a care day.

## Answer an innsyn request

A person has the right to a copy of the personal data held about them (GDPR art. 15,
*innsyn*). Staff's complete answer is the HR-issued personal-data file:

1. As a user with `staff:personal-data`, open the person in **People**, press
   **Download personal data** at the top of their page and confirm
   ([the user guide](/en/user/staff/#download-a-persons-data);
   `GET /api/v1/staff/people/{id}/personal-data`). The answer is a download,
   `staff-<employee number>-personal-data.json`: the person's record with every field
   group and the care-day fields, the national identity number and the bank account in
   full, HR's notes, the next of kin, the roles, every employment with its terms (pay
   and notes included), the user link, the documents' list, every absence, the holiday
   adjustments, every sick-leave case with its milestones and their notes, this year's
   care days, the standing of egenmelding with any withdrawal, and the whole history.
2. Download each document the file lists from the person's **Documents** tab (or
   `GET /api/v1/staff/documents/{id}`); the file carries their metadata, not their
   bytes.
3. Verify: the person's **History** now starts with **Personal data exported**, naming
   you. Every such export is recorded, because the file opens the sealed values.

Hand the file over the way your company hands over personal data — not by ordinary
email. The person can also download their own copy from My page
(`GET /api/v1/staff/me/export`); it leaves out the sealed values, HR's notes and the
terms' notes, so it is not the complete answer, and it records nothing.

## Put a person under a legal hold

A legal hold keeps a person's record whole — no retention erase touches it, and the
person cannot be deleted — for as long as a dispute, a claim or an investigation needs
it.

1. As a user with `staff:personal-data`, open the person and press **Put under legal
   hold** at the top of their page. Write the **Reason** — required, up to 500
   characters: what the hold is for and, if you can, until when — and press **Set the
   legal hold** ([the user guide](/en/user/staff/#a-legal-hold);
   `PUT /api/v1/staff/people/{id}/legal-hold` with `{"held": true, "reason": "…"}`).
2. Verify: the page shows **Under legal hold** with the day and the reason (the person's
   `legalHold` is `{"held": true, …}`), and their **History** has **Legal hold set**.
3. When the matter is closed, press **Lift the legal hold** and confirm
   (`{"held": false}`, no reason). The reason and who set it are cleared; the history
   keeps both.

A held person who has been erased can still be held and released; nothing else of theirs
changes.

## Rehire an erased person

When the retention run has erased a former employee, their record is read-only: every
change answers `person_erased`, except a legal hold, a reassessment, unlinking their
user, and a rehire. Their page says so in a notice and offers nothing else
([the user guide](/en/user/staff/#an-erased-record)).

1. Before adding someone who may have worked here before, fill in their **Given name**,
   **Family name** and **Birth date** in **New person** (`staff:manage`). On **Save and
   continue** the form lists erased people with the same names and birth date, with
   their employee number and when they were erased
   (`POST /api/v1/staff/people/similar-erased` with
   `{"givenName", "familyName", "birthDate"}`). The national identity number cannot
   tell — the erase removed it.
2. If it is the same person, press **Open** with their employee number, go to
   **Employments** and press **Rehire** (`POST /api/v1/staff/people/{id}/employments`).
   The record opens again under the same employee number; its history gets **Rehired**
   before the new employment.
3. Type again what the erase removed — contact details, address, next of kin, the
   national identity number and the bank account (the last two with
   `staff:personal-data`).
4. If it is someone else, press **Create anyway**; the erased record stays as it is.

To link the person's user to a new record instead, press **Unlink** under Self-service
on the erased one first (`PUT /api/v1/staff/people/{id}/user` with
`{"userId": null}`) — the one change of the link an erased record allows.

## Import and export the register

The register travels as one semicolon-separated CSV file — the form a Norwegian Excel
opens and saves as CSV without an import dialog. All three steps need `staff:manage`
and are on the **Import and export people** card in **Staff settings**
([the user guide](/en/user/staff/#import-and-export-people)); each is also an endpoint,
named below. The columns, the rules and the codes are in
[the reference](/en/reference/staff/#the-registers-import-and-export).

### Import a register

1. Download the template: **Download template**
   (`GET /api/v1/staff/people/import-template.csv`). It is the header row alone — the person's columns, the first employment's, and the sealed ones
   only if you hold `staff:personal-data`. Or start from an export.
2. Fill in one row per person. `employeeNumber` is required on every row: a number no
   person has adds the person, a number a person has changes them. For a new person,
   fill in `startDate` to add their first employment too — `businessUnit` by its name or
   org number (blank is the default unit), `department` by its code, `location` by its
   name, `managerEmployeeNumber` by the manager's employee number. A manager the file
   adds must come on an earlier row. A person already in the register only has their
   own columns changed — never an employment: change those in the app.
3. Save it as CSV UTF-8 with semicolons, and check it: **Import people**, choose the
   file, **Check** (`POST /api/v1/staff/people/import` with the file in the multipart
   part `file`). The check is a **dry run**: every row runs and is rolled back, and the
   page shows what the rows would do — created, updated, with errors — and each problem
   with its row and column. Without `dryRun=false` the endpoint does the same.
4. Fix what failed and check again. When the check is clean, press **Import**, which
   sends the same file with `?dryRun=false`.
5. Verify: the counts are what you expect, the people are in the list, and each
   person's history shows the import's changes under your name.

A row that fails writes nothing and does not stop the others; rows already imported stay
imported, so send a corrected file of the failed rows alone — **Download failed rows**
gives them back as written, behind an `error` column the import ignores — or the whole
file again: rows that change nothing write nothing. An import cut short — the connection dropped
mid-file — keeps the rows it ran, and the installation's history still records it with
the counts so far: `rows` is the whole file, and the created, updated and failed rows
add up to fewer. The file is at most 5 MiB and
`STAFF_IMPORT_MAX_ROWS` rows (5000 unless you raised it,
[configuration reference](/en/admin/authentication/#configuration-reference)).

### The sealed columns

`nationalIdKind`, `nationalIdCountry`, `nationalId`, `bankAccountKind` and `bankAccount`
need `staff:personal-data` as well. A file carrying any of them from someone without it
is refused whole (`import_sealed_columns`), and the import says so in words — remove the
columns or ask for the permission. A filled cell sets the value with the same checks as on the person's
record; a blank one leaves the value on file as it is. A bank account the import changes
mails the person, as a change by hand does ([The bank-change mail](#the-bank-change-mail)),
once their row is saved — never in a dry run. No answer, log line or history entry
carries a digit of either value.

The mail is sent row by row, as each row is saved, and the import waits for each send:
an onboarding import of a hundred people with bank accounts sends a hundred mails — a
first account mails too, as by hand — and takes correspondingly longer.

A row cannot change a person's `birthDate` and `nationalId` together: the import
writes the person's columns first, and checks the new birth date against the number on
file, which still encodes the old one — the row is refused
`national_id_birth_date_mismatch`. Make the change in two files, as you would by hand:
first the new `nationalId` with the `birthDate` cell blank, then the new `birthDate`.

### Export the register

**Export people** (`GET /api/v1/staff/people/export.csv`) downloads
`staff-people-<today>.csv`: every person
— erased ones too — with the work and private groups and where each stands today. The
national identity number and the bank account are never in it: `nationalIdOnFile` says
`yes` or `no`. Every export is recorded in the installation's history with your name and
the number of rows. The file holds personal data: keep it where you keep the register,
and delete it when you are done.

An export imports back unchanged: the columns after the private group are ignored, so
editing the person's columns in an export and importing it is a way to change many
people at once. An erased person's row comes back too and writes nothing; edit it, and
the row is refused `person_erased`.

## Export a month for payroll

The payroll file hands a month's employments to your payroll system. You need
`staff:manage` **and** `staff:personal-data`: the file's leave rows name HR-only leave,
such as pregnancy leave, by its a-melding description. Payroll is done by someone who
holds both (an interim rule; the owner may instead choose to leave HR-only leave out of
the file for HR without `staff:personal-data`).

1. In **Staff settings**, under **Payroll export**, pick the **Month** — the month
   before today is chosen to begin with — and press **Export for payroll**
   ([the user guide](/en/user/staff/#export-a-month-for-payroll)). The page asks for
   `GET /api/v1/staff/payroll/export?month=2026-09&format=csv`; called directly, `format`
   may be left out — `csv` is the one format there is.
2. Save the download, `staff-payroll-2026-09.csv`, and import it into payroll.

What the file holds: one row per employment that was in the month — started by its last
day and not ended before its first — erased people's included, with the terms in force
on the month's last day (or on the end day, for an employment that ended during the
month). Each row carries the employee and employment numbers, the name, the business
unit's org number, the start, the occupation code, the full-time hours, the position
percentage and when it last changed, the working hours scheme and the appointment form
as Skatteetaten's a-melding codes, the remuneration type, when the pay last changed,
the department's code, the cost centre (the terms' own, else the department's), and
the seniority date and pay grade when set. The end date and its reason are filled in
only in the month the employment ends. An employment with no terms on that day keeps
its row with the terms columns blank. The form is the Expenses payroll file's —
semicolons, a byte order mark and the decimal comma — so a Norwegian Excel opens it
as it is. The full column list and the codes are in
[the reference](/en/reference/staff/#the-payroll-export). The codes of the three
appointment forms added in 2025 (hired out, and on call) are not verified against
Skatteetaten's code list: check them with your payroll system before you rely on them.

**The absence columns.** To the right of the pay grade come thirteen more, so a
payroll mapping of the first nineteen keeps working. On each employment's row, the
month's totals in working days, each by its share: **Holiday days**, **Self-certified
sick days**, **Certified sick days** (by the degree), **Child sick days** and of those
the **Child sick days beyond employer** — beyond the ten the employer bears in the
year, what NAV refunds — **Leave days**, **Time off hours** and **Other days**. Only
approved and registered absence counts. Two rules to know: a sick day beside a
percentage leave counts degree × (100 − the leave's percent) / 100, and approved ferie
counts **as holiday** under any sickness — a sykmelding of any degree or an egenmelding
— since the holiday stands until the employee demands to postpone it and no sykepenger
are paid on a holiday day; once you record the demand
([Postpone holiday for sickness](#postpone-holiday-for-sickness)) the ferie is split and
the sick days count as sick. Child-sick days count by their percent.

**A person with two employments at once**: the day totals are on each employment's
row, the child-sick days beyond the employer's ten on the main one alone (the highest
position percentage, ties to the lowest employment number). An absence does not say
which employment it is from, so **a leave's row repeats on every employment in force —
check such a person's leave rows by hand** before payroll reports them.

After an employment's row come its **leave rows**: one per leave the a-melding reports
(Skatteetaten's *Permisjon*), with **Leave ID**, **Leave start**, **Leave end**,
**Leave description** and **Leave percent**. A leave is reported when it lasts more than
fourteen days, or when the person's leaves in the last twelve months together pass
fourteen days; permittering always; a child's sickness and a leave counted in hours
(*ammefri*) never. A leave extended by a new row — the next day, or after a weekend or
public holiday — stays one leave under one **Leave ID**, the id the a-melding knows it
by: a code such as `L-C098AE873961`, the same in every month's file and never an id of
the register. Before you wire
the file to a payroll system, check the seven description values against the
kodeliste that system uses: they were checked against NAV's sources on 2026-10-14,
but Skatteetaten's own kodeliste could not be reached ([the
reference](/en/reference/staff/#the-payroll-export)).

The national identity number and the bank account are **not** in the file: payroll
already holds them, so the export never reads them.

Every export is recorded in the installation's history with your name, the month, the
format, the number of rows and the file's SHA-256 hash, so the file payroll received
can be recognised later — compute the hash of a file you hold
(`shasum -a 256 staff-payroll-2026-09.csv`) and compare. A month that is not
`YYYY-MM` is refused `payroll_month_invalid`, a format other than `csv`
`payroll_format_unsupported`; neither is recorded. The file holds everyone's pay terms:
keep it where you keep payroll's own records, and delete your copy when payroll has it.

## Retention

Staff keeps a former employee's record only as long as the law lets it, and erases the
rest on its own: the `staff-retention` worker looks every hour and erases what has
become due. What is due, and what each erase removes, is in
[the reference](/en/reference/staff/#retention); in short:

- **Personnel administration** — contact details and address, the national identity
  number and the bank account, the care-day fields, HR's notes and the terms' notes, the
  next of kin, and the documents in categories of class D (certificates,
  correspondence, other) — is erased **`retentionGraceDays`** (default 90) after the
  person's last employment ended. The person is then marked erased, and the record is
  read-only.
- **Sick-leave data** is erased on its own schedule, **whatever the person's
  employment** — a current employee's included: a sick-leave case's notes, estimate and
  milestones' facts, its rows' sick type (which becomes *Absent*) and degree, the
  documents filed under it, and what its history says of the sickness go
  **`healthRetentionDays`** (default 365) after the case closed — closed by you, or
  sixteen days after its last day. A child-sick row's type goes the same period after
  the end of its year, and a withdrawal of egenmelding the same period after it
  lapsed — or all of it with the person's other health data, when their last
  employment ended more than `healthRetentionDays` ago and that comes first. The days
  stay: who was away when is kept, and an erased case keeps its dates and its
  milestones' names and dates — unless you [erase cases' dates and
  milestones](#erase-pregnancy-rows-and-sick-leave-cases-dates) too.
- **Pregnancy and health appointments** — svangerskapskontroll, svangerskapspermisjon,
  fødselspermisjon and doctor's or dentist's visits — are kept with the employment's
  record by default. With the setting on, they go like sick-child days:
  **`healthRetentionDays`** after the end of the year a row ended, the row becomes
  *Absent*, its days and hours kept, and its history no longer says which type it was.
- **Other health documents** — those in a restricted category not filed under a case —
  are deleted **`healthRetentionDays`** after the last employment ended.
- **Pay** — an employment's pay terms, seniority date and pay grade, its contracts and
  attests, and the pay in its history — is erased **five years after the year the
  employment ended**, employment by employment: a person who has come back keeps the
  pay of their current employment while the old one's goes.

**What an erase leaves**: the names, the birth date, the gender, the employee number,
the employments and their terms other than pay, the roles, the link to a user, and the
history's kinds and dates. So the record still answers who worked here, when and in
what job; the history shows a value that was erased as *changed*, and an **Erased under
the retention rules** entry names what went, by kind and count. A person with no
employment, or one still employed, is never erased.

### Set the periods

1. As a user with `staff:manage`, open **Staff settings**; the **Retention** card shows
   the periods ([the user guide](/en/user/staff/#retention)).
2. Change **Grace period (days)** (`retentionGraceDays`, 0 to 3650) and **Health
   documents (days)** (`healthRetentionDays`, 30 to 3650), and press **Save periods**.
   Through the API it is `PUT /api/v1/staff/settings` with the revision you read in
   `If-Match`; a body that leaves either period out keeps it.
3. Verify: the card shows the new periods. The next run judges by them; a period made
   shorter can make many people due at once, and the worker takes fifty a run.

### Erase pregnancy rows and sick-leave cases' dates

Two settings, off by default, erase more than the law requires you to keep (the
owner's decision on B's open questions 11 and 12). Decide them with your data
protection officer: each erases for good.

1. As a user with `staff:manage`, open **Staff settings**; the **Retention** card has
   two switches, each explained beneath it.
2. **Erase pregnancy and health appointments** (`erasePregnancyHealthTypes`): rows of
   svangerskapskontroll, svangerskapspermisjon, fødselspermisjon and *Doctor or
   dentist* become a reserved *Absent* type — an hour row keeps its hours —
   `healthRetentionDays` after the end of the year they ended, never before the payroll
   month that reported them has passed — and **svangerskapspermisjon and
   fødselspermisjon never before a full year after the end of their year**, whatever
   the period, so the holiday pay of the year after them, and a payroll re-export of
   that year, still read the leave. Only the type is erased: an absence's note and the
   decision's note stay, and go with the personnel administration. The HR-only types
   take an HR-only reserved
   type, so managers, Approvers and HR without `staff:personal-data` still read the row
   as *leave* and see no entry about the erase; the person and `staff:personal-data`
   see *Absent (HR only)*. Their history keeps its dates and reads the type as
   *changed*; an entry about a row deleted or changed to another type goes by the year
   it was recorded in. An erased HR-only row keeps its revision, so the tag HR without
   `staff:personal-data` holds for it does not change either. A payroll month
   re-exported after its pregnancy rows were erased reports those days as other days,
   not as leave.
3. **Erase a sick-leave case's dates and milestones** (`eraseSickCaseDates`): when a
   closed case's health content is erased, the case itself goes too — its dates, its
   milestones and its history, which keeps only the entries' kinds and dates. Its
   absences stay as *Absent* days, in no case; its documents are deleted as before.
4. Press **Save periods**. Through the API it is `PUT /api/v1/staff/settings` with
   either field and the revision in `If-Match`; a body that leaves one out keeps it,
   and a `null` is refused.
5. Verify: the next run — on its own within the hour, or **Run retention now** —
   erases **everything already due**, cases erased before included, and its answer
   names *pregnancy and health appointments* or *sick-leave cases* with a count.

**Turning a setting off restores nothing**: what it erased stays erased; it only stops
the next runs erasing more. A person under a legal hold is passed over by both, and
`STAFF_RETENTION_ENABLED=0` stops both with the rest.

### Turn the worker off, or change how often it looks

The worker runs unless `STAFF_RETENTION_ENABLED=0`, every `STAFF_WORKERS_POLL` (default
`1h`, at least `1m`) — in worker mode, and in api mode with `WORKERS_IN_PROCESS=1`
([Installation](/en/admin/installation/)). An erase finds nothing due a second time, so
an hourly look erases each day's due people once. Several replicas take turns through a
lock; one runs the erase at a time.

Turned off, nothing is erased on a schedule — **the sick-leave cases' erase
included**: it runs in the same cycle, with no worker of its own — and the objects
queued for deletion wait; the run by hand below still works.

### Run it by hand

**Run retention now** on the **Retention** card in **Staff settings** (`staff:manage`;
`POST /api/v1/staff/retention/run`) runs the worker's erase and a pass over the
deletion ledger now, with you as the erased entries' author, after you confirm. The
card then says what was erased per kind and how many objects were deleted from the
store. Its pass over the deletion ledger stops after thirty seconds, so a slow object
store does not hold the request; the worker deletes the rest on its next look. While
the worker is running, the card says so (`retention_busy`, 409); try again in a minute.

### The legal hold

A person under a [legal hold](#put-a-person-under-a-legal-hold) is passed over by every
erase, of every kind, for as long as the hold stands. Lift the hold when the matter is
closed, and the next run erases what has been due meanwhile.

### The deletion ledger

An erase deletes a document's record at once and queues its file for deletion from the
object store; so does deleting a document by hand, when the store does not confirm at
once. The worker asks the store again on every look, waiting longer after each refusal
(at most an hour), and never gives up — a legal erase is not best-effort.

The **Retention** card in **Staff settings** shows the ledger — **Pending deletions**,
**Failing deletions**, **Oldest pending since** and **Last erased** — as
`GET /api/v1/staff/settings` reports it in its `retention` block: `pendingDeletions`
(files waiting), `failingDeletions` (files the store has refused at least once),
`oldestPendingSince` and `lastErasedAt` (the last erase). A `failingDeletions` that
stays above zero means the object store is refusing: check that
storage is configured and reachable ([Object storage](/en/admin/object-storage/)) and
the server log, which names the store's error. Once the store answers, the queued files
go at the next look.

### After an erase

An erased person who comes back is [rehired](#rehire-an-erased-person) on the same
record: what the erase removed stays empty until you type it again, and the rehired
employment's pay is untouched by the old one's erase.

## The thresholds

Four thresholds bring the employer a duty, and Staff watches each. While one is crossed
and not acknowledged, people with `staff:manage` see an item for it in **Needs
attention** on the dashboard ([the dashboard](/en/user/staff/#the-dashboard)):

| Threshold | Crossed when | What the employer must do |
| --- | --- | --- |
| `5` | 5 or more people are employed today | Have written whistleblowing routines (aml. § 2 A-6) and a safety representative, verneombud (§ 6-1). |
| `30` | 30 or more | Have a working environment committee, AMU (§ 7-1). |
| `50` | More than 50 — 51 or more | The activity and reporting duty on equality, ARP (likestillings- og diskrimineringsloven § 26), which applies to an employer with more than fifty employees. At exactly 50 there is no item yet. |
| `otp` | Someone is at 75 % or more, or the people at 20 % or more add up to two full-time positions — the percentages employed today, summed per person | Have an occupational pension scheme, OTP (OTP-loven § 2) — unless the only such person is the owner, which Staff cannot tell, since it records no owner. |

When the duty is dealt with — or does not apply — acknowledge the threshold. It is
acknowledged **once and for good**: its item never comes back, whatever the headcount
does later, and a threshold can be acknowledged before it is crossed.

1. As someone with `staff:manage`, open **Staff settings**, press **Acknowledge** on the
   threshold's row under **Thresholds**, and confirm
   ([the user guide](/en/user/staff/#thresholds)). Through the API it is
   `POST /api/v1/staff/thresholds/{key}/acknowledge` with the key — `5`, `30`, `50` or
   `otp`.
2. Verify: the row says **Acknowledged** with the date, as `thresholdsAcknowledged` in
   `GET /api/v1/staff/settings` does; the item is gone from the dashboard.

Each acknowledgement is recorded in the settings with its time, and as a
`threshold_acknowledged` event of the installation naming who acknowledged it.
Acknowledging a threshold again changes nothing and records nothing. A key other than
the four is refused with `threshold_unknown`.

## Who decides absence

A request for absence — ferie, a leave, time off in lieu — is decided by the person's
manager today (the manager their terms name, else their department's, skipping anyone
who has left), by anyone holding `staff:approve`, and by HR (`staff:manage`). To let
someone decide everyone's absence without HR's other rights, give them a role with
`staff:approve`. Nobody decides their own request: a manager's goes to their own
manager, and HR's to another holder of `staff:approve` or `staff:manage`. A manager
decides only once their person is linked to an active user
([Link people to their users](#link-people-to-their-users)).

**HR-only leave is decided by `staff:personal-data`**: a request of an HR-only type —
pregnancy leave, a religious holiday, any company type marked HR-only — goes to no
manager, no holder of `staff:approve` and no HR without `staff:personal-data`, and none
of them is mailed about it. Anyone holding `staff:personal-data` but the person decides
it in their approvals inbox, and only HR with `staff:personal-data` registers, changes,
cancels or corrects such a row — HR without it is answered as for a type that does not
exist. Give that permission to whoever should decide such leave. To everyone else the
leave appears only once approved, as *leave*.

**The sole holder of `staff:personal-data` decides their own HR-only request** (the
owner's rule). When exactly one active user holds `staff:personal-data` — and it is the
person asking — they approve their own request in their approvals inbox. Who holds the
permission is what identity says: a role of the user's own, a role mapped to an access
group they belong to, or the **Owner** role, which holds every permission — so with an
Owner and one other holder, neither is the sole holder, and the request goes to the
other. A delegation gives no permission. A disabled user, a user who has not yet
accepted their invitation and, with SCIM, a user the directory deactivated do not
count. If Staff cannot read the holders from identity, nobody is the sole holder and the
request waits. Each such approval is the attention item `hrOnlySelfApproved` for every
holder of `staff:personal-data` (the person's name, never the type) until the absence
ends: give a second person the permission and they see it — the second pair of eyes the
rule lacks. To avoid the rule, give `staff:personal-data` to two people.

**The one exception, for now** (provisional, awaiting the owner's word): someone holding
`staff:approve` or `staff:manage` who heads the structure — they manage a department
with no manager above it — and has no manager with an active user may approve their
own. Each such approval is an attention item for `staff:manage` (`selfApproved`, the
person's name) until the absence ends, so somebody sees it. To
avoid it, give that person a manager in their terms, linked to an active user.

HR corrects an approved or registered absence — its days, type, percentage or degree —
with **Correct** (an approved one) or **Change** (a registered one) on the person's
**Absence** tab; the correction is recorded with what it was before, and the
person is mailed about a correction. A decided absence that has begun is cancelled by HR only.
Neither a change nor a correction turns an HR-only type into any other, or the other way
round (`absence_visibility_change`): the two are decided and seen by different people. To
fix an absence entered as the wrong kind, cancel it (**Cancel absence**) — or, a
registered one, which cannot be cancelled, remove it (`DELETE /absences/{id}`) — and
enter the other anew.

## The absence mails

Who is mailed about absence, and when:

- a **request** or a **registration** goes to the person's managers who decide it (the
  manager today, linked to an active user), at the work email Staff holds for them,
  else their private one, in that person's language;
- a **request** of a person with **no such manager** goes instead to everyone holding
  `staff:approve` or `staff:manage` — **Owners included**, who hold every permission —
  once each, never to the person themselves, at the address the user has in identity,
  in the language of the person they are linked to, else their own, else Norwegian. A
  registration of such a person mails nobody. Give the person a manager linked to an
  active user to keep these mails to the manager;
- a **request of an HR-only type** goes to no manager and no holder of `staff:approve`
  or `staff:manage`, but to every other holder of `staff:personal-data` (Owners
  included), as a neutral mail — "An absence request awaits your decision" — that names
  no person, type or day. That mail is only ever sent for an HR-only request, so its
  arrival alone says one exists: give `staff:personal-data` only to users whose address
  in identity is their own, never a shared mailbox or a list;
- an **approval**, a **rejection**, a **cancel** of an approved absence by its decider and HR's **correction** go to
  the person. A self-approval mails nobody.

Everyone who decides also sees what waits for them in the approvals inbox. A mail names
the type only when it is neither sickness nor an HR-only type: an HR-only type is
"leave", a sick or child-sick absence "an absence"; it never carries a degree, hours, a
note or a rejection's reason. Like the bank-change mail below, it needs
`MAIL_DRIVER=smtp`; with another driver the server logs
`staff: absence mail skipped: the mail driver is not smtp`, and a manager without any
email is skipped with `staff: absence mail skipped: the recipient has no email`. A
holder whose identity address is an identity provider's or a directory's placeholder is
skipped with `staff: absence mail skipped: the holder has no address a mail reaches`,
naming the user's id; when Staff cannot read the holders from identity it logs
`staff: absence mail skipped: the holders could not be read` and mails none of them. A
notice's mails are sent one after the other within 30 seconds together; what is left
when the time runs out is logged with its count. A send that fails is logged as a
warning naming the person's or the user's id, never an address; the absence stays
saved.

## The bank-change mail

When a bank account is set or changed, the person is mailed — at their private email
when they have one, else at their work email, in their preferred language — naming the
account's last four digits and nothing more, so a change somebody else made does not go
unnoticed. The mail goes through the installation's own SMTP server after the change is
saved. Sending the account already on file again changes nothing and mails nobody.

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
`PUT /api/v1/staff/settings`, and changed on the Staff app's Settings page
([Change the settings](/en/user/staff/#change-the-settings)). A replace carries the revision it read in `If-Match`, so two people
changing the settings at once cannot overwrite each other unseen.

- **Business time zone** (`timeZone`, default `Europe/Oslo`). Every date in Staff —
  a start, an end, "today" — is a calendar day in this zone. Use a city zone such as
  `Europe/Oslo`: a name the server and the database read differently, such as `CET`,
  is refused.
- **Employee number prefix and next number** (`employeeNumberPrefix`, empty, and
  `nextEmployeeNumber`, 1). A person created without an employee number gets the
  prefix and the next number, unpadded: `EMP-` and 7 make `EMP-7`. The prefix is up
  to eight letters, digits or hyphens, and the two together may make at most 16
  characters. Two employee numbers that differ only in case are the same number
  (`emp-7` is refused beside `EMP-7`), and that is judged the same way whatever
  locale the database was created with — a Turkish one included — and the same way
  identity judges the employee number it keeps on a user.
- **Pension provider** and **injury insurer** (`pensionProvider`,
  `injuryInsurer`). The occupational pension (OTP) provider and the occupational
  injury insurer, which the employment contract check reads.
- **Retention periods** (`retentionGraceDays`, 90, from 0 to 3650, and
  `healthRetentionDays`, 365, from 30 to 3650): how many days after a person's last
  employment ended their personnel administration and their health documents are
  erased ([Retention](#retention)). Left out of a replace, each keeps its value.
- **Self-certification policy** (`selfCertPolicy`, `minimum`, with
  `selfCertDaysPerOccurrence`, `selfCertOccurrencesPerYear`, `selfCertDaysPerYear` and
  `selfCertAfterMonths`) and **automatic carry-over** (`autoCarryOver`, off, with the
  read-only `autoCarryOverSince`): [The self-certification policy](#the-self-certification-policy).
  Left out of a replace, each keeps its value.
- **Accounts** — the **Accounts** card, with **Prepare the account (days before)** and
  **Delete an external account (days after the end)**, empty for never
  (`createBeforeDays`, 14, from 0 to 90, and `deleteExternalAfterDays`, none, from 1 to
  3650 or `null` for never): how many days before an account's
  effective date it is prepared, disabled ([Turn on accounts for a
  person](#turn-on-accounts-for-a-person)), and when an external directory's account is
  deleted after the last employment ended — stored but not used yet; the Entra module
  will read it. Left out of a replace, each keeps its value; `null` sets
  `deleteExternalAfterDays` to never.
- **Retention state** (`retention`, read-only): the deletion ledger's pending and
  failing files, the oldest pending, and the last erase ([The deletion
  ledger](#the-deletion-ledger)). Files the store has refused at least once and that
  were queued more than 24 hours ago also raise an item on the dashboard for
  `staff:manage`.
- **Thresholds acknowledged** (`thresholdsAcknowledged`, read-only): when each
  threshold was acknowledged ([The thresholds](#the-thresholds)).

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
- **`storage_unavailable`.** The object store could not be written to or read from:
  storage is not configured, or its root is not writable. Nothing was recorded; fix
  the storage and try again ([Object storage](/en/admin/object-storage/)).
- **`seed_immutable`.** One of the six seeded categories cannot be changed. Add a
  category of your own instead.
- **`category_in_use`.** Documents are still filed under the category you are
  archiving. Move them to another category first.
- **`threshold_unknown`.** The key is none of `5`, `30`, `50` and `otp`.
- **`document_count_exceeded`.** The person has 200 documents, the most one person may
  have — counted whatever you may see, so it can include health documents you cannot.
  Remove documents that are no longer needed.
- **`person_erased`.** The retention run has erased the person, and the record is
  read-only. Rehire them with a new employment, or leave it.
- **`legal_hold`.** The person you are deleting is under a legal hold. Lift it first, if
  the matter is closed.
- **`reassessment_not_due`.** The person is not erased, has been reassessed already, or
  their last employment ended less than twelve months ago.
- **`import_not_semicolon`.** The file is comma-separated. Save it again as CSV with
  semicolons — in a Norwegian Excel, *CSV UTF-8* does.
- **`import_too_large`** or **`import_too_many_rows`.** The file is over 5 MiB or has
  more rows than `STAFF_IMPORT_MAX_ROWS`. Import it in slices.
- **`import_sealed_columns`.** The file carries a national identity number or bank
  account column and you do not hold `staff:personal-data`. Remove the columns, or ask
  for the permission.
- **An unknown column.** The import names it: a misspelt header is refused rather than
  ignored. Correct it to one of the template's names.
- **`export_too_many`.** The register holds more people than `STAFF_IMPORT_MAX_ROWS`.
  Raise it, within its bounds, and restart.
- **`retention_busy`.** The retention worker is running, on this server or another.
  Wait a minute and run it again.
- **`failingDeletions` above zero.** The object store refuses to delete the queued
  files. Fix the storage; the worker retries without end.
- **A retention period refused.** `retentionGraceDays` is 0 to 3650 days,
  `healthRetentionDays` 30 to 3650.
- **Nobody is erased.** The worker is off (`STAFF_RETENTION_ENABLED=0`), the person
  is under a legal hold, still has an employment that has not ended, or has none at
  all; or the period has not run yet.
