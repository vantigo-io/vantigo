---
title: Microsoft Entra ID
description: Connecting Vantigo to a Microsoft Entra ID tenant so Staff's accounts are made there — the app registration, the client secret, the eight application permissions and the User Administrator role, admin consent, the Temporary Access Pass policy, the account rules, sign-in and provisioning; what a leaver's steps do and what deleting loses; rotating secrets; the tagged test against a real tenant; and what each reason code means.
sidebar:
  order: 46
sources:
  - apps/server/internal/entra/module.go
  - apps/server/internal/entra/graph.go
  - apps/server/internal/entra/credentials.go
  - apps/server/internal/entra/status.go
  - apps/server/internal/entra/lifecycle.go
  - apps/server/internal/entra/tenant_test.go
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
  on. The registration, the secrets and the connection test are an Owner's alone; a
  person with `entra:manage` sees them read-only.
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
needs; a **Status** card sits above them once the connection is tested. Work through
them in order, the portal in another tab.

### 1. Register

1. In the **Microsoft Entra admin center**, open **App registrations** › **New
   registration**.
2. Name it (for example `Vantigo`), choose **Accounts in this organizational directory
   only**, and leave **Redirect URI** empty for now. Choose **Register**.
3. On the registration's **Overview**, copy the **Directory (tenant) ID** and the
   **Application (client) ID**.
4. In step 1 of the wizard, paste them into the two fields and choose **Save**.

One registration can serve both Vantigo's Graph connection and the sign-in (step 5);
you may use two if you prefer.

### 2. A secret

1. In the registration, open **Certificates & secrets** › **Client secrets** › **New
   client secret**. Give it a description and an expiry: at most 24 months, and
   Microsoft recommends less than 12.
2. Copy the secret's **Value** at once — not the **Secret ID**. The portal shows it only
   now.
3. In step 2, choose **Add a secret**: a label (the description you gave it), the value,
   and the expiry date the portal shows. Choose **Save**. Vantigo seals the value at once
   and never shows it again.

### 3. Permissions and the role

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
2. Choose **Grant admin consent for** your organisation, and confirm. Each permission's
   status turns to *Granted*.
3. **Assign the User Administrator role** to the application. Open **Roles &
   admins** › **User Administrator** › **Add assignments**, and select the
   application by its name (its service principal, listed under enterprise
   applications). Without the role, Graph refuses to enable, disable or delete an
   account even with the permissions — the step a half-done setup fails on at the
   first person's first day.
4. In step 3, choose **Test connection**. Vantigo fetches a token with the secret, reads
   the organisation and its verified domains, and compares the token's permissions with
   the eight.

Verify: the test says the connection works, names the organisation, and lists no
permission as missing. A missing one is named: add it, grant consent again, and test
again. The test cannot see the directory role — Vantigo cannot read it without a
further permission — so check the assignment yourself under **Roles & admins**.

### 4. Accounts

The account rules, which a holder of `entra:manage` may set as well as an Owner:

- **The domain** the user principal names are made under, chosen from the tenant's
  verified domains (the test reads them).
- **The pattern** for a person whose work email cannot be their user principal name —
  `given.family`, `g.family` or `given`. A work email on a verified domain is used as it
  is.
- **The usage location**, the country written when Staff has none for the person (`NO`
  unless changed). Microsoft refuses a licence to a user without one.
- **The leaver's steps** — revoke the sessions, write the leave date, remove the
  licences, and which groups to leave — and whether to write the hire date; each is on
  unless you turn it off ([what they do](#what-a-leavers-steps-do)).
- **The Temporary Access Pass**: its lifetime (10 minutes to 30 days, 8 hours unless
  changed), whether it works once, and who hands it over. HR shows it once, on the
  person's Account tab in Staff; the hand-over setting is stored for a later release
  and does not yet send the manager anything.

Choose **Save**.

**The pass needs the tenant's policy.** Vantigo can create a pass for any user, but the
person can sign in with it only if the tenant's **authentication methods policy**
enables it for them: in the **Microsoft Entra admin center**, open **Authentication
methods** › **Policies** › **Temporary Access Pass**, turn it on, and target all users
or a group the new people are in. The policy has its own maximum lifetime (eight hours
by default): keep the module's lifetime within it. Without the policy, Graph refuses the
pass and the person's Account tab in Staff says *a pass not allowed*.

### 5. Sign-in

Optional. The wizard embeds the **Single sign-on** card's provider form, filled in with
the tenant's authority (`https://login.microsoftonline.com/<tenant id>/v2.0`) and the
client id, for people to sign in to Vantigo with their Microsoft account:

1. In the registration, open **Authentication** › **Add a platform** › **Web**, and
   paste the **Callback URL** the form shows.
2. The form needs a client secret of the registration too — a second one, since the
   portal shows a value only once — or a token file.
3. Complete the form and choose **Save**, as [SSO and SCIM
   operations](/en/admin/sso-scim/#microsoft-entra-id-client-secret) describes.

Without it, Staff still creates the Microsoft accounts, and nobody signs in to Vantigo
with them.

### 6. Provisioning

Optional. The wizard embeds the card's SCIM block: choose **New token**, and in the
portal open **Enterprise applications** › your application › **Provisioning**, set
**Provisioning Mode** to **Automatic**, paste the **Endpoint URL** as **Tenant URL** and
the token as **Secret Token**, test, and save
([SCIM](/en/admin/sso-scim/#scim)). Vantigo accepts the enterprise extension's
attributes Entra maps by default, the employee number among them
([the enterprise extension](/en/admin/sso-scim/#the-enterprise-extension-and-the-employee-number)).

Without it, a person's Vantigo user appears at their first sign-in rather than within
Entra's provisioning cycle after the account is enabled. Either way, the user that
arrives is adopted for Staff as the account Staff announced.

The step also offers a link to Staff's settings, where **Default account kind** can be
set to **Microsoft Entra ID** for new people ([Accounts](/en/user/staff/#accounts)). The
module never sets it itself.

### Verify the connection

- The **Status** card reads connected, with the organisation, no permission missing,
  the secret in use and its expiry.
- Staff's settings, **Accounts**, read *Microsoft Entra ID: connected.*
- Give one test person the **Microsoft Entra ID** kind and their account flag, and
  choose **Run now** on their Account tab: the **Microsoft Entra ID** card shows the
  account with its user principal name, and the module's recent events show
  `entra.user.prepared`.

## What a leaver's steps do

The day after a person's last employment ends, Staff has the module disable the
account. In this order, each but the first switched in step 4:

| Step | What happens in the tenant |
| --- | --- |
| Disable | Always, first: the account can no longer sign in. Apps that support continuous access evaluation react within minutes. |
| Revoke the sessions | Every refresh token and browser session ends within a few minutes; an access token already issued lives out its hour. |
| Write the leave date | `employeeLeaveDateTime` is set to the last day at 23:00 in the business time zone — what a Lifecycle Workflows leaver trigger reads. |
| Remove the licences | Every licence assigned to the person directly is removed and its seat freed. A licence that comes from a group goes with the group. |
| Leave the groups | **All**: every group except dynamic, role-assignable and mail-only ones, groups Vantigo never made included. **Managed**: Vantigo's own groups only. **None**: every membership stays. |

A step that fails is tried again on the next cycle; the account is disabled first
whatever else fails. Any pass not yet shown is discarded.

**Deleting** comes later, when Staff's **Delete an external account (days after the
end)** says, and never while the account is enabled or the person is under a legal
hold. It is a soft delete: the account is in the tenant's **Deleted users** for 30 days
and can be restored there. **After the 30 days it is gone, with its mailbox and its
OneDrive.** To keep a leaver's mail, convert the mailbox to a shared mailbox or hand
over the OneDrive in the Microsoft 365 admin center before then — Vantigo does neither
— or leave the setting empty, which never deletes. A person hired again after a delete
gets a new account.

## Rotate a secret

1. In the registration, add a new client secret, and copy its value.
2. In step 2, **Add a secret** with it. The newest valid secret is the one used, so the
   new one takes over at once; **In use** moves to it.
3. Choose **Revoke** on the old one, then delete it in the portal. Vantigo refuses to
   revoke the only valid secret while it keeps accounts (`credential_in_use`), since
   nothing could then disable a leaver.

Thirty days before the newest valid secret ends, Staff's dashboard shows
`secretExpiring`; once it has ended, `secretExpired`, and the connection is failing:
Staff then writes nothing to the tenant until a new secret is added — not even a
leaver's disable. A secret the tenant refuses (`invalid_client`, for instance one
deleted in the portal) is marked failed and the next valid one is tried.

## Change the tenant or the application

Step 1's **Save** with another tenant or client id sends the connection back to *not
connected* until it is tested again. While the module keeps accounts, Vantigo asks
first (`connection_in_use`): the accounts were made in the old tenant, and the module
will look for people in the new one. Nothing is deleted in either tenant.

## The tagged test

For a contributor: the module's tests run against a fake Graph, and one tagged test
runs the real thing against a dedicated tenant, to settle what the fake cannot know —
whether an application may write `employeeHireDate`, the pass's permission, and which
properties a user synchronised from on-premises refuses.

**The tenant needs:**

- A dedicated tenant — Entra ID Free covers users and groups through Graph. (Whether
  the Microsoft 365 Developer Program sandbox is still available without a Visual
  Studio subscription is uncertain; a trial or pay-as-you-go tenant does.)
- An app registration with the eight application permissions, admin consented, and
  **the User Administrator role assigned to its service principal** (steps 1 to 3).
- The Temporary Access Pass enabled in the authentication methods policy for all users.
- A client secret, the tenant id, the client id and a verified domain
  (`<name>.onmicrosoft.com` is fine).
- For the third fact, a user synchronised from on-premises, by object id.

**Run it** from `apps/server`:

```sh
ENTRA_TEST_TENANT_ID=<tenant id> \
ENTRA_TEST_CLIENT_ID=<client id> \
ENTRA_TEST_CLIENT_SECRET=<secret value> \
ENTRA_TEST_DOMAIN=<verified domain> \
go test -tags entra -run TestEntraTenant ./internal/entra/
```

`ENTRA_TEST_SYNCED_USER_ID` adds the synchronised user's check. Without the four
variables the test is skipped. It creates a user prefixed `vantigo-test-`, enables it
with the hire date, issues a pass, updates it, disables it with the leave date and
deletes it, and creates and deletes a prefixed group; it logs what Graph answered for
the three facts, and removes everything it made when it ends. It goes through the same
guarded transport as production. Keep the secret out of your shell history.

## What can go wrong

The **Status** card names the reason; Staff's settings and dashboard say the same in
Staff's words.

| Reason | Means | Do |
| --- | --- | --- |
| `credential_invalid` | No secret fetched a token: the value was mistyped, the secret was deleted in the portal, or the client id is wrong. | Add the right secret (step 2), check the ids (step 1), and test again. |
| `tenant_unreachable` | The tenant did not answer. | Check outbound https to `login.microsoftonline.com` and `graph.microsoft.com` from every process, then test again. |
| `consent_missing` | The token carries no permission: admin consent was never granted, or was withdrawn. | **Grant admin consent** (step 3) and test again. |
| `secret_expired` | Every secret has ended. | Add a new one (step 2). |
| `secret_expiring` | The newest valid secret ends within 30 days. | [Rotate](#rotate-a-secret). |
| `role_missing` | Three enables, disables, passes or deletes in a row were refused, none succeeding since: the User Administrator role is most likely not assigned. | Assign the role (step 3), then **Run now** on one of the people held up; a success clears it. |
| Permissions missing | The test's token lacks one or more of the eight. | Add them, grant consent again, test again. |

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
| A pass not allowed | The tenant's authentication methods policy does not enable the pass for the person. | [Enable the policy](#4-accounts) for them, then **Run now (new pass)**. |
| The manager's account not found | The manager has no account in the tenant. | Give the manager the Microsoft Entra ID kind, or accept it. |
