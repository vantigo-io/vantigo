---
title: Microsoft Entra ID
description: Connecting Vantigo to a Microsoft Entra ID tenant so Staff's accounts are made there — the app registration, the client secret, the eight application permissions and the User Administrator role, admin consent, the Temporary Access Pass policy, the account rules, sign-in and provisioning; calendar sync — Calendars.ReadWrite, its consent, and narrowing it to some mailboxes with Exchange RBAC for Applications; what a leaver's steps do and what deleting loses; Staff's groups in the tenant, the application as their owner, confirming a group Vantigo did not create and what an archived group becomes; rotating secrets; the tagged test against a real tenant; and what each reason code means.
sidebar:
  order: 46
sources:
  - apps/server/internal/entra/module.go
  - apps/server/internal/entra/graph.go
  - apps/server/internal/entra/credentials.go
  - apps/server/internal/entra/status.go
  - apps/server/internal/entra/lifecycle_activate.go
  - apps/server/internal/entra/lifecycle_deactivate.go
  - apps/server/internal/entra/groups.go
  - apps/server/internal/entra/tenant_test.go
  - apps/server/internal/entra/mailbox.go
---

The Microsoft Entra ID module makes the accounts of the people Staff registers in the
company's Microsoft Entra ID tenant: created disabled ahead of the start, enabled on the
first day with a Temporary Access Pass, kept in step as the person moves, disabled the
day after the end, deleted later or never. Staff decides; the module carries it out
through Microsoft Graph. This page is the operator's side: what the tenant needs, the
six steps of the module's wizard, what a leaver's steps do, and what to do when
something fails. What the module does and the rules it checks are in [the
reference](/en/reference/entra/); the wizard's screens are in [the user
guide](/en/user/entra/).

## Before you start

- **A Microsoft Entra ID tenant**, and someone in it who can register an application,
  grant admin consent to Microsoft Graph's application permissions and assign a
  directory role — a Global Administrator or a Privileged Role Administrator.
- **An Owner** in Vantigo, signed in with a second factor while `OWNERS_REQUIRE_MFA` is
  on — without it an Owner is refused 403 like anyone else. The registration, the
  secrets, the connection test and the last two steps are an Owner's alone; a person
  with `entra:manage` sees them read-only.
- **Staff on.** The module is Staff's provider and does nothing without it: `MODULES`
  refuses `entra` without `staff` at startup (`entra requires staff`).
- **The Single sign-on card**, under Workspace administration, for the last two steps:
  the module's wizard embeds its sign-in and provisioning forms
  ([SSO and SCIM operations](/en/admin/sso-scim/)).
- **Outbound https** from every process that runs the API or the workers to
  `login.microsoftonline.com` and `graph.microsoft.com`, without a proxy: the module
  uses none and follows no redirect.
- The migrations create the `entra` schema whether or not the module is enabled; a
  least-privilege application role needs it granted like every other
  ([Installation](/en/admin/installation/#database-roles)).

Nothing about the module is in `vantigo.env`: there is no `ENTRA_*` variable. The
tenant, the client, the secrets and the account rules are in the database, set on the
module's page.

## Enable the module

1. Leave `MODULES` unset, or add `entra` beside `staff`.
2. Restart Vantigo (`docker compose up -d`).
3. Verify: the tile **Microsoft Entra ID** is in the app switcher for an Owner, and
   `GET /api/v1/entra/connection` answers 401 without a session rather than the 404 a
   disabled module's path answers.

The module has no worker of its own: Staff's accounts worker, `staff-accounts`, calls it
([Staff](/en/admin/staff/#the-accounts-worker)).

## Connect the tenant

Open **Microsoft Entra ID** from the app switcher. The page is the wizard: six steps,
each a card with the portal path, what to copy, and *Done* once Vantigo has what it
needs (*Optional* on the last two); a **Status** card sits above them once the
connection has been tested. Work through them in order, the portal in another tab.

### 1. Register the application

1. In the **Microsoft Entra admin center**, open **App registrations** › **New
   registration**.
2. Name it (for example `Vantigo`), choose **Accounts in this organizational directory
   only**, and leave **Redirect URI** empty for now. Choose **Register**.
3. On the registration's **Overview**, copy the **Directory (tenant) ID** and the
   **Application (client) ID**.
4. In step 1 of the wizard, paste them into the two fields and choose **Save**.

One registration can serve both Vantigo's Graph connection and the sign-in (step 5);
you may use two if you prefer.

### 2. Add a client secret

1. In the registration, open **Certificates & secrets** › **Client secrets** › **New
   client secret**. Give it a description and an expiry: at most 24 months, and
   Microsoft recommends less than 12.
2. Copy the secret's **Value** at once — not the **Secret ID**. The portal shows it only
   now.
3. In step 2's **Add a secret** form, fill in **Description** (the description you gave
   it, optional), **Expires** (the day the portal shows — Vantigo takes the start of
   that day in your browser's time zone) and **Value**, and choose **Add the secret**.
   Vantigo seals the value at once and never shows it again.

### 3. Grant the permissions and the role

1. In the registration, open **API permissions** › **Add a permission** › **Microsoft
   Graph** › **Application permissions**, and add these eight — the wizard lists them to
   copy:
   - `User.ReadWrite.All`
   - `User-LifeCycleInfo.ReadWrite.All`
   - `User.RevokeSessions.All`
   - `UserAuthMethod-TAP.ReadWrite.All`
   - `LicenseAssignment.ReadWrite.All`
   - `Group.ReadWrite.All`
   - `Domain.Read.All`
   - `Organization.Read.All`

   A ninth, `Calendars.ReadWrite`, is needed only for [calendar sync](#calendar-sync),
   and the wizard lists it once that is on.
2. Choose **Grant admin consent for** your organisation, and confirm. Each permission's
   status turns to *Granted*. `User.ReadWrite.All` also covers the person's photo,
   which Vantigo writes to the account: no further permission or consent.
3. **Assign the User Administrator role** to the application. Open **Roles &
   admins** › **User Administrator** › **Add assignments**, and select the
   application by its name (its service principal, listed under enterprise
   applications). Without the role, Graph refuses to enable, disable or delete an
   account, or to issue a Temporary Access Pass, even with the permissions — the step a
   half-done setup fails on at the first person's first day.
4. In step 3, choose **Test connection**. Vantigo fetches a fresh token with the secret,
   reads the organisation and its verified domains, and compares the token's
   permissions with the eight.

Verify: the test says *Connected to* the organisation, lists the verified domains, and
says *Every permission the module needs is granted.*; each permission in the list is
marked granted. A missing one is named: add it, grant consent again, and test again.
The test cannot see the directory role — Vantigo cannot read it without a further
permission — so check the assignment yourself under **Roles & admins**.

### 4. Set the account rules

The account rules, which a holder of `entra:manage` may set as well as an Owner:

- **The domain** the user principal names are made under, chosen from the tenant's
  verified domains once the test has read them (typed before that). The form asks for
  one; were it unset, the module would use the first verified domain not ending in
  `.onmicrosoft.com`.
- **The pattern** for a person whose work email cannot be their user principal name —
  `given.family`, `g.family` or `given`. A work email on a verified domain is used as it
  is.
- **The usage location**, the country written when Staff has no work country for the
  person, or one that is not a two-letter country code (`NO` unless changed). Microsoft
  refuses a licence to a user without one.
- **The leaver's steps** — revoke the sessions, write the leave date, remove the
  licences, and which groups to leave — and whether to write the hire date; each is on
  unless you turn it off ([what they do](#what-a-leavers-steps-do)).
- **The Temporary Access Pass**: its lifetime (10 minutes to 30 days, 8 hours unless
  changed), whether it works once, and who hands it over. HR shows it once, on the
  person's Account tab in Staff; the hand-over setting is stored for a later release
  and does not yet send the manager anything.

Choose **Save the account rules**. Once the connection is connected, the card also
lists **The tenant's licences**, each with its seats assigned and bought — the part
numbers a Staff group's licences can name.

**The pass needs the tenant's policy.** Vantigo can create a pass for any user, but the
person can sign in with it only if the tenant's **authentication methods policy**
enables it for them: in the **Microsoft Entra admin center**, open **Authentication
methods** › **Policies** › **Temporary Access Pass**, turn it on, and target all users
or a group the new people are in. The policy has its own maximum lifetime (eight hours
by default): keep the module's lifetime within it. Without the policy, Graph refuses the
pass and the person's Account tab in Staff says *The directory does not allow a
Temporary Access Pass for this account.*

### 5. Sign-in

Optional, and an Owner's. The wizard embeds the **Single sign-on** card's provider
form — once step 1 is saved — filled in with the tenant's authority
(`https://login.microsoftonline.com/<tenant id>/v2.0`) and the client id when the form
holds nothing yet, for people to sign in to Vantigo with their Microsoft account:

1. In the registration, open **Authentication** › **Add a platform** › **Web**, and
   paste the **Callback URL** the form shows.
2. The form needs a client secret of the registration too — a second one, since the
   portal shows a value only once — or a token file.
3. Complete the form and choose **Save**, as [SSO and SCIM
   operations](/en/admin/sso-scim/#microsoft-entra-id-client-secret) describes.

Without it, Staff still creates the Microsoft accounts, and nobody signs in to Vantigo
with them.

### 6. Provisioning

Optional, and an Owner's. The wizard embeds the card's SCIM block: choose **New
token**, and in the
portal open **Enterprise applications** › your application › **Provisioning**, set
**Provisioning Mode** to **Automatic**, paste the **Endpoint URL** as **Tenant URL** and
the token as **Secret Token**, test, and save
([SCIM](/en/admin/sso-scim/#scim)). Vantigo accepts the enterprise extension's
attributes Entra maps by default, the employee number among them
([the enterprise extension](/en/admin/sso-scim/#the-enterprise-extension-and-the-employee-number)).

Without it, a person's Vantigo user appears at their first sign-in rather than within
Entra's provisioning cycle after the account is enabled. Either way, the user that
arrives is adopted for Staff as the account Staff announced.

The step also offers **Open Staff's settings**, where **Default account kind** can be
set to **Microsoft Entra ID** for new people ([Accounts](/en/user/staff/#accounts)). The
module never sets it itself.

### Verify the connection

- The **Status** card reads **Connected**, with the organisation, *Every permission the
  module needs is granted.*, and the secret **In use** with its expiry.
- Staff's settings, **Accounts**, read *Microsoft Entra ID: connected.*
- Give one test person the **Microsoft Entra ID** kind and their account flag, and
  choose **Run now** on their Account tab: the **Microsoft Entra ID** card shows the
  account with its user principal name, and the module's recent events show
  `entra.user.prepared`. If the tenant already held a user with that employee number,
  confirm it first ([below](#accounts-vantigo-did-not-create)).

## Calendar sync

**Calendar sync** lets people keep a copy of their Vantigo calendar in their Outlook
([the user guide](/en/user/calendar/#sync-with-outlook)): the calendar's
`calendar-mailbox-sync` worker writes each person's appointments, and the items they
choose, to their Exchange Online mailbox through this module's connection. It is off by
default, and provisioning works the same with it on or off. What it writes, and how, is
in [the reference](/en/reference/entra/#the-mailbox-calendar).

### Preconditions

- The connection is set up and **Connected** ([above](#connect-the-tenant)), and the
  Calendar module is enabled ([Calendar](/en/admin/calendar/)).
- The people who will sync have the **Microsoft Entra ID** account kind in Staff, and
  their account is one Vantigo made or whose adoption is confirmed
  ([below](#accounts-vantigo-did-not-create)): Vantigo writes only to a mailbox it knows
  is the person's, never one matched by address.
- Their accounts have an Exchange Online mailbox (a licence that includes Exchange
  Online).

### Turn it on

1. On the module page, in step **4 Set the account rules**, turn on **Let people sync
   their Vantigo calendar to Outlook** under **Calendar sync**, and choose **Save the
   account rules** (`entra:manage`). Step 3 now lists `Calendars.ReadWrite` as the
   ninth permission.
2. In the registration, open **API permissions** › **Add a permission** › **Microsoft
   Graph** › **Application permissions**, and add `Calendars.ReadWrite`.
3. Choose **Grant admin consent for** your organisation, and confirm.
4. In step 3, an Owner chooses **Test connection**.

Verify: the line under the switch reads **Ready**, and the **Status** card no longer
lists `Calendars.ReadWrite` as missing. A person who opens **Calendar → My calendar**
can turn on **Sync to my Outlook calendar**; within a minute their card says **Last
synced** and the appointments are in Outlook. Until a test sees the permission, the
line reads **Calendars.ReadWrite is not in the token. That is expected if you granted it
with Exchange RBAC for Applications; otherwise add it, grant admin consent and test
again.** and step 3 is *Not done* — but the line is advisory: people can turn their sync
on all the same, and a mailbox the permission does not reach is refused for that person
alone. The rest of the module works as before.

**Turning it off** stops everyone's sync, and the worker removes the copies it wrote,
using the connection while a credential still works. Turning it on again resumes
everyone whose own toggle is on.

### Limit it to some mailboxes

`Calendars.ReadWrite` as an application permission reaches **every mailbox in the
tenant**. Vantigo only writes to the mailboxes of the accounts it manages, for the
people who turn the sync on, but the credential could reach more. To narrow what the
credential itself may reach, use Exchange Online's **RBAC for Applications**, in
Exchange Online PowerShell, as an Exchange administrator:

1. Make a mail-enabled security group (or a Microsoft 365 group) holding the people
   whose calendars may be synced. Nested members do not count.
2. Connect: `Connect-ExchangeOnline`.
3. Register the application's service principal in Exchange, with the **Application
   (client) ID** and the **Object ID** of the enterprise application (not of the app
   registration):

   ```powershell
   New-ServicePrincipal -AppId <client id> -ObjectId <enterprise application object id> -DisplayName "Vantigo"
   ```

4. Make a scope of the group's members, with the group's distinguished name
   (`Get-Group <group> | Select DistinguishedName`):

   ```powershell
   New-ManagementScope -Name "Vantigo calendar sync" -RecipientRestrictionFilter "MemberOfGroup -eq '<group distinguished name>'"
   ```

5. Grant the calendar role in that scope:

   ```powershell
   New-ManagementRoleAssignment -App <client id> -Role "Application Calendars.ReadWrite" -CustomResourceScope "Vantigo calendar sync"
   ```

6. Check a mailbox inside the scope and one outside it:

   ```powershell
   Test-ServicePrincipalAuthorization -Identity <client id> -Resource <mailbox address>
   ```

7. **Remove the tenant-wide grant in Entra**: in the registration's **API permissions**,
   remove `Calendars.ReadWrite` (or revoke its admin consent). Exchange adds the two
   grants together, so while the tenant-wide consent stands the scope restricts nothing.

The changes take **30 minutes to 2 hours** to apply. A grant made with RBAC for
Applications never shows in the token, so once the tenant-wide consent is removed, the
next **Test connection** lists `Calendars.ReadWrite` as missing, step 3 reads *Not done*
and the line under the switch reads **Calendars.ReadWrite is not in the token. That is
expected if you granted it with Exchange RBAC for Applications; …** — that is expected,
and the sync goes on. Verify with a person inside the scope: their card says **Last
synced**. A person outside the scope sees **Your organisation has not
allowed Vantigo to reach your mailbox.** on their Outlook card: Vantigo tries them again
every six hours, and never reads it as the connection failing.

## Accounts Vantigo did not create

When Staff prepares a person and the tenant already holds a user with their employee
number — one made by hand, by another tool, or before Vantigo — the module links that
user but **writes nothing to it until a holder of `entra:manage` confirms it is the
person's**: no details, no enable, no pass, no disable and no delete. Otherwise anyone
who can register a person in Staff could take over someone else's account by giving
the new person that account's employee number.

Until then the person's Account tab in Staff says *The directory holds an account
under the person's employee number that Vantigo did not create. Staff writes nothing
to it until someone who manages Microsoft Entra ID confirms the adoption on the
Microsoft Entra ID page.*, and the dashboard raises an item that links to the page.

To confirm:

1. Open **Microsoft Entra ID**. In the **Status** card's list of the module's
   accounts, the user reads *Adopted — confirm*.
2. Check in the portal that the user is the person's — the name, the principal name,
   who uses it.
3. Choose **Confirm** beside it, and **Confirm** again in *Confirm the adoption of …?*.

Vantigo then manages the account from Staff's next accounts run: it writes the
person's details, enables it on the first day and disables and deletes it when they
leave. A leaver whose account was waiting for the confirmation is disabled within one
poll of it. Never confirm an account you have not checked: confirming hands it to
whoever is registered under that number in Staff.

## What a leaver's steps do

The day after a person's last employment ends, Staff has the module disable the
account, whatever the connection's status as long as a secret works — unless the
account is one Vantigo did not create and nobody has confirmed. In this order,
each but the first switched in step 4:

| Step | What happens in the tenant |
| --- | --- |
| Disable | Always, first: the account can no longer sign in. Apps that support continuous access evaluation react within minutes. |
| Revoke the sessions | Every refresh token and browser session ends within a few minutes; an access token already issued lives out its hour. |
| Write the leave date | `employeeLeaveDateTime` is set to the last day at 23:00 in the business time zone — what a Lifecycle Workflows leaver trigger reads. Skipped when Staff sends no end day. |
| Remove the licences | Every licence assigned to the person directly is removed and its seat freed. A licence that comes from a group goes with the group. |
| Leave the groups | **All**: every group except dynamic, role-assignable and mail-only (distribution) ones, groups Vantigo never made included. **Managed**: only the groups Vantigo made or adopted for Staff's groups, which the module page's **Groups** table lists. **None**: every membership stays. |

The account is disabled first whatever else fails, and any pass not yet shown is
discarded at once. A step that fails stops the steps after it and is reported as
`leaver_step_failed:` and the step's name (`revoke_sessions`, `leave_date`,
`strip_licences`, `remove_groups`), which the person's Account tab in Staff words as
*A leaver step in the directory was not finished: signing the person out everywhere.*
(or the step it was). Staff disables the account again once a day while that code
stands, and the module runs the steps again from the first, the code clearing when
they all succeed; a step that fails again waits for the next day.

**Deleting** comes later, when Staff's **Delete an external account (days after the
end)** says, and never while the account is enabled or the person is under a legal
hold. It is a soft delete: the account is in the tenant's **Deleted users** for 30 days
and can be restored there. **After the 30 days it is gone, with its mailbox and its
OneDrive.** To keep a leaver's mail, convert the mailbox to a shared mailbox or hand
over the OneDrive in the Microsoft 365 admin center before then — Vantigo does neither
— or leave the setting empty, which never deletes. A person hired again after a delete
gets a new account.

## Staff's groups in the tenant

HR keeps groups in Staff and turns Microsoft Entra ID on for a group on its page
([Groups](/en/user/staff/#groups)). Staff's accounts worker then has the module make the
group in the tenant — a security group or a Microsoft 365 group, by the group's
**Group type** — with the group's owners as its owners, and keep its name, its description
and its members in line with Staff at each cycle. A Microsoft 365 group can be made a
team. Nothing else is needed in the tenant: `Group.ReadWrite.All`, one of the eight
permissions, covers the groups, their owners, their members and the team.

**Vantigo's application owns every group it makes.** The module names its own service
principal — the application from step 1, as **Enterprise applications** lists it — as
an owner of each group, beside the people HR named. Microsoft does not let an
application change a group it created with no owner, so **do not remove the
application from a group's owners in the portal**: Vantigo never removes it, and a
group without it may no longer be changed from Vantigo. An owner HR names is made an
owner in the tenant only once their account there is enabled.

**A group already in the tenant is adopted, untrusted.** Before it makes a group, the
module looks for one holding the mail nickname — one made by hand earlier with the same
nickname — and links it to the Staff group, but **writes nothing to it until a holder of
`entra:manage` confirms it**: no name, no owner, no member, no delete. Otherwise anyone
who keeps groups in Staff could take over any group in the tenant by giving a Staff
group its nickname. Until then the group's page in Staff says *Found in Microsoft Entra
ID and awaiting confirmation there* — not a failure, and no item on Staff's dashboard —
and the module page's **Groups** table marks the group *Adopted — confirm*. The module
never touches a group holding the nickname that is dynamic, role-assignable, a
distribution list or a mail-enabled security group (which Exchange keeps), or that
another Staff group already uses, nor a nickname two groups hold; and it refuses a group
of the other type than the Staff group's **Group type**. The group's page in Staff then
says why: rename that group's nickname in the portal, or give the Staff group another
**Mail nickname** before it is made.

To confirm an adopted group:

1. Open **Microsoft Entra ID**. In the **Groups** table, the group reads *Adopted —
   confirm*.
2. Check in the portal that it is the group meant for that Staff group — its name, its
   owners, its members.
3. Choose **Confirm** beside it, and **Confirm** again in *Confirm the adoption of …?*.

From Staff's next accounts run Vantigo manages the group: Staff's name, description and
visibility are written to it, **its owners become exactly HR's and the application —
anyone else is removed —** and its members exactly Staff's. Never confirm a group you
have not checked. If it is not the group meant, leave it unconfirmed — Vantigo writes
nothing to it — and give the people another Staff group with another key: the Staff
group stays linked to the group it found until it is confirmed or let go of.
**Vantigo never deletes a group it did not create**, confirmed or not.

**Edits in the portal** do not last: the name, the description and the visibility are
written again, and the owners set to Staff's, when HR changes the group's name,
description, settings or owners; the members are set to Staff's exactly whenever
Staff's members change, and at least once a day — a member added in the portal, or one
who joined a public group themselves, is removed by the next day. A nested group or a
device you add as a member is left alone. Keep a group's membership and owners in Staff, not in the
portal. A group deleted in the portal is made again the next time Staff sends its
settings; until then the group's page may say the directory holds a group Vantigo may
not write — save the group's settings again to have it made.

**Licences come from the groups.** A person whose account is enabled is assigned
directly the licences of every group they are in that has Microsoft Entra ID on, and a
licence Vantigo assigned is removed again when Staff next brings their account in line
and none of their groups names it any more. **Vantigo removes only the licences it
assigned itself**: one an administrator assigned directly, one the person held before a
group named it, and one that comes from a group in the tenant (group-based licensing)
are left alone. Vantigo keeps the list per account from this release on; an account it
enabled earlier starts with an empty list, so nothing assigned to it before is removed
by a group change. The leaver's **Remove the licences** step is the exception: it
removes every licence assigned to the person directly, Vantigo's or not. The choices a
group's
**Licences** offer are the tenant's licences as the last connection test, or the last
look at **The tenant's licences** in step 4, read them: test again after you buy a new
licence.

**An archived group, or one HR turned Microsoft Entra ID off for**, follows Staff's
**Delete an external account (days after the end)**:

| The setting | What happens to the group in the tenant |
| --- | --- |
| A number of days | That many days after the archive (or the switch turned off), Vantigo lets the group go, by its kind and by who made it. **A Microsoft 365 group Vantigo made** is deleted — a soft delete: it is in the tenant's **Deleted groups** for 30 days and can be restored there. **A security group Vantigo made** is never deleted, since Microsoft deletes a security group for good: its members and its person owners are removed (the application stays an owner), it is renamed with ` (archived)` after its name, and kept. **A group Vantigo did not create** is left in the tenant as it is. Until the days have passed the group stays as it was. |
| Empty (never) | At the next cycle the group is renamed with ` (archived)` after its name and kept, its members as they stood; Vantigo writes nothing to it afterwards. |

A deleted Microsoft 365 group takes its mailbox, its SharePoint site and its team with it
when the 30 days are over. The module page's **Groups** table shows a kept security group
as *Emptied and kept*, and a group Vantigo did not create as *Deleted* — Vantigo has let
go of it, though the tenant keeps it. Should HR turn Microsoft Entra ID on again for the
Staff group, a kept security group is found by its nickname and is Vantigo's own again,
with no confirmation; delete one in the portal when it is no longer wanted.

## Rotate a secret

1. In the registration, add a new client secret, and copy its value.
2. In step 2, fill in **Add a secret** with it and choose **Add the secret**. The newest
   valid secret is tried first, so the new one takes over at once; **In use** moves to
   it.
3. Choose **Revoke** on the old one, then delete it in the portal. Vantigo refuses to
   revoke the only valid secret while it keeps accounts (`credential_in_use`), since
   nothing could then disable a leaver.

Thirty days before the last valid secret to expire ends, Staff's dashboard shows
`secretExpiring`; once every secret has ended, `secretExpired`, and the connection is
failing: Staff then writes nothing to the tenant until a new secret is added — not even
a leaver's disable. A secret the tenant refuses (`invalid_client`, for instance one
deleted in the portal) is marked failed — the table says *Refused by the tenant* and
the date — and tried after the others until it fetches a token again. **Last used** is
when a secret last fetched a token.

## Change the tenant or the application

Step 1's **Save** with another tenant or client id sends the connection back to *not
connected* — the **Status** card is gone and Staff's settings say *Microsoft Entra ID:
not connected yet.* — until it is tested again; the verified domains and the
organisation are forgotten with it. A change of the tenant also discards every
Temporary Access Pass not yet shown; a change of the application alone keeps them. The
same ids again change nothing. While the module keeps accounts, Vantigo asks first,
**Change the tenant or the application?** (`connection_in_use`), and **Change it**
saves: the accounts were made in the old tenant, the module will look for people in
the new one, and **the old tenant's accounts are no longer disabled or deleted by
Vantigo** when their people leave — handle those leavers in the old tenant yourself.
Nothing is deleted in either tenant.

## The tagged test

For a contributor: the module's tests run against a fake Graph, and one tagged test
runs the real thing against a dedicated tenant, to settle what the fake cannot know —
whether an application may write `employeeHireDate`, whether the pass is issued with
its permission, and what the module answers for a user synchronised from on-premises.

**The tenant needs:**

- A dedicated tenant — Entra ID Free covers users and groups through Graph. (Whether
  the Microsoft 365 Developer Program sandbox is still available without a Visual
  Studio subscription is uncertain; a trial or pay-as-you-go tenant does.)
- An app registration with the eight application permissions, admin consented, and
  **the User Administrator role assigned to its service principal** (steps 1 to 3).
- The Temporary Access Pass enabled in the authentication methods policy for all users.
- A client secret, the tenant id, the client id and a verified domain
  (`<name>.onmicrosoft.com` is fine).
- For the third fact, a user synchronised from on-premises: its object id and its
  employee number (the module finds a user by the employee number).

**Run it** from `apps/server`:

```sh
ENTRA_TEST_TENANT_ID=<tenant id> \
ENTRA_TEST_CLIENT_ID=<client id> \
ENTRA_TEST_CLIENT_SECRET=<secret value> \
ENTRA_TEST_DOMAIN=<verified domain> \
go test -tags entra -run TestEntraTenant -v ./internal/entra/
```

`ENTRA_TEST_SYNCED_USER_ID` and `ENTRA_TEST_SYNCED_EMPLOYEE_NUMBER` together add the
synchronised user's check, which writes nothing to that user. Without the four
variables the test is skipped, naming the one missing. It tests the connection, sets
the account rules, then prepares one user — employee number `VT-` and six random
characters, given name `Vantigo`, family name `Test` and the same characters, so its
user principal name starts `vantigo.test` — enables it with the hire date and a pass,
reveals the pass once, updates it, disables it with the leave date and soft-deletes it.
It logs what Graph answered for the three facts (`UNCERTAIN FACT 1` to `3`) and the
module's events, never the secret or a pass. When a step fails first, its cleanup
disables and deletes the user it made; a deleted user stays restorable under
**Deleted users** for 30 days. It also makes a group with the application and the user
as its owners (a security group whose mail nickname starts with `vt-test-`), makes it
again to check it finds the same group, adds the user as its member and removes them
again in a `$batch`, and lets it go — the team is not tried,
since a group must be fifteen minutes old first. Being a security group, it is not
deleted but emptied, renamed with ` (archived)` and kept, so each run leaves one
`vt-test-` group in the tenant: delete them in the portal. It goes through
the same guarded transport as production. Keep the secret out of your shell history.

## What can go wrong

The **Status** card names the reason; Staff's settings and dashboard say the same in
Staff's words.

| Reason | Means | Do |
| --- | --- | --- |
| `credential_invalid` | No secret fetched a token: the value was mistyped, the secret was deleted in the portal, the client id is wrong, or `APP_SECRET` changed and the stored secrets can no longer be opened. | Add the right secret (step 2), check the ids (step 1), and test again. |
| `tenant_unreachable` | The tenant did not answer. | Check outbound https to `login.microsoftonline.com` and `graph.microsoft.com` from every process, then test again. |
| `consent_missing` | The token carries no permission — admin consent was never granted, or was withdrawn — or Graph refused to read the organisation. | **Grant admin consent** (step 3) and test again. |
| `secret_expired` | Every secret has ended. | Add a new one (step 2). |
| `secret_expiring` | The last valid secret to expire ends within 30 days. | [Rotate](#rotate-a-secret). |
| `role_missing` | Three creates, enables, disables, phone changes or deletes in a row were refused, none succeeding since: the User Administrator role is most likely not assigned. | Assign the role (step 3), then **Run now** on one of the people held up; a success clears it. |
| Permissions missing | The test's token lacks one or more of the eight. | Add them, grant consent again, test again. |

Calendar sync's troubles show on the line under its switch, and on each person's
**Outlook** card in Calendar:

| Where | Means | Do |
| --- | --- | --- |
| *Calendars.ReadWrite is not in the token. …* under the switch | The switch is on, the connection connected, and the last test's token lacked `Calendars.ReadWrite`. Expected under RBAC for Applications; the sync runs either way. | Nothing under RBAC for Applications. Otherwise add the permission, grant consent and test again ([calendar sync](#turn-it-on)); until then every person's sync is refused as their own mailbox denied. |
| *Waiting for the connection* under the switch | The connection is not connected; its reason is under the line. | Fix the connection as the table above says. |
| *Your organisation has not allowed Vantigo to reach your mailbox.* on a person's card | Graph answered 403 for their mailbox (`mailbox_denied`): outside an RBAC for Applications scope. Tried again every six hours. | Add the person to the scope's group, or accept it; they press **Sync now** to try at once. |
| *Your mailbox could not be found.* on a person's card | The user has no Exchange Online mailbox, or no longer exists (`mailbox_gone`). Tried again every six hours. | Give the account a licence with Exchange Online. |
| *Your Vantigo user is not connected to a Microsoft 365 account that Vantigo manages.* on a person's card | Staff links the user to no account of this module, or to one Vantigo did not create and nobody confirmed, or one deleted (`no_account`). | Give the person the Microsoft Entra ID kind in Staff, or [confirm the account](#accounts-vantigo-did-not-create). |

On a person's Account tab in Staff, the **Microsoft Entra ID** card says what the
tenant reported for them:

| On the tab | Means | Do |
| --- | --- | --- |
| Two accounts with the employee number | Two users in the tenant hold it as their employee id. | Clear it from the wrong one in the portal. |
| The work email already another account's | Another user has it as user principal name. | Change the person's work email in Staff, or resolve the other account. |
| Synchronised from an on-premises directory | The user is mastered by Active Directory, which Graph will not write. | Manage the account on-premises; hybrid tenants are not supported. |
| An administrative role in Entra | The user holds a role (or is in a role-assignable group) the application may not touch — or the application lacks the role. | An administrator handles the account by hand; see `role_missing` above. |
| The hire date not written | Graph refused `employeeHireDate`. | Nothing is needed for sign-in; turn **Write the hire date** off if it keeps failing. |
| No licence left (the licence named) | No seat was free. | Buy seats or free one, then **Run now**. |
| A pass not allowed | The tenant's authentication methods policy does not enable the pass for the person; the account is enabled without one. | [Enable the policy](#4-set-the-account-rules). Vantigo issues a pass only on the call that enables an account, so for this person create one in the portal (the user's **Authentication methods**). |
| *The directory refused the Temporary Access Pass; the account is enabled without one.* | The tenant refused the pass with a 403 (`pass_forbidden`) — the application lacks the role or the pass permission, or the user is one it may not touch. | Check the User Administrator role and `UserAuthMethod-TAP.ReadWrite.All`; create the pass in the portal for this person. |
| *The directory holds an account under the person's employee number that Vantigo did not create …* | `external_unconfirmed`: the module found the user and writes nothing to it. | [Confirm it](#accounts-vantigo-did-not-create) after checking it is the person's; if it is not, correct the employee number where it is wrong. |
| The manager's account not found | The manager has no account in the tenant. | Give the manager the Microsoft Entra ID kind, or accept it. |
| The photo not written (*The directory refused the photo.*) | Graph refused the person's picture, or its removal (`photo_not_written`): a 400, 403 or 413 — or a 404, for a user without a mailbox — or the picture could not be converted to a JPEG under 4 MB. Vantigo does not try the same picture again. | Nothing is needed for sign-in. HR may set another photo, or the person another picture, which Vantigo tries at its next run; check the module's recent events (*An account's photo was set*) for the account. |
| *A leaver step in the directory was not finished: …* | A leaver's step after the disable failed, and the steps after it were not tried; the account is disabled. | Nothing, usually: Staff tries again the next day. If it keeps failing, check the module's recent events and the log, and do the step in the portal. |

On a group's page in Staff, the **Microsoft Entra ID** card says how the group stands
in the tenant:

| On the card | Means | Do |
| --- | --- | --- |
| *Failed: this cannot change once the group is made* beside **Group type** or **Mail nickname** | The value differs from the one the group was made or adopted with (`trait_immutable`). | Set it back; a group of another type is a new Staff group. |
| *Failed: it needs a person as an owner* beside **Team in Microsoft Teams** | A team needs a person among the group's owners (`trait_needs_owner`). | Add an owner on the group's page; Staff sends it at the next cycle. |
| *Failed: the value is not one the directory accepts* beside a setting | `trait_invalid` — a visibility or a team on a security group, say. | Change the value and save. |
| *Found in Microsoft Entra ID and awaiting confirmation there* | `group_unconfirmed`: a group already in the tenant holds the mail nickname; Vantigo linked it and writes nothing to it. | [Confirm it](#staffs-groups-in-the-tenant) on the module page after checking it is the group meant. |
| *Failed: the directory holds a group by this key that Staff may not write …* | `group_not_manageable`: a group holding the mail nickname is dynamic, role-assignable or Exchange's, another Staff group's, held by two groups, or gone from the tenant. | Rename the other group's nickname in the portal, or give the Staff group another **Mail nickname** before it is made; for a group deleted in the portal, save the settings again. |
| *Failed: the directory could not be reached; the worker tries again* | Three calls in a row failed (`call_failed`). | Check the connection's **Status** card and the log. |
| *… members could not be changed; they are tried again at the next sync* | The tenant refused adding or removing some members (`members_failed`). | Nothing, usually: Staff tries them again at the next cycle. |
