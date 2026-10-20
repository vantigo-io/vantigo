---
title: Workspace administration
description: Users, invitations, roles and access, delegated administration, first-run setup and system administration.
sidebar:
  order: 90
sources:
  - apps/host/frontend
  - apps/server/internal/identity
---

Workspace administration is where an **Owner** manages who can use Vantigo and what
they may do. Open the avatar menu at the top right and choose **Workspace admin**;
its sidebar has **Overview**, **Users**, **Invitations** and **Roles & access**.

Everything here needs the Owner role, with one exception: a person to whom an Owner
has delegated role administration sees **Roles & access** alone, both in the avatar
menu and in the sidebar. The last active Owner can never be demoted, disabled or
deleted, so the workspace always has one.

## Read the overview

**Overview** opens the **Admin dashboard**, "An overview of accounts and identity
integrations, and the workspace's own settings.":

- **People** — **Total**, **Active**, **Disabled** accounts and **Pending
  invitations**, with a link to **View users**.
- **Invitations** — the pending count and **Manage invitations**.
- **Access control** — **Manage roles & access**.
- **Identity integrations** — whether **SSO** is on and which provider (Microsoft
  Entra ID, Google Workspace, or just **Enabled**), that **Client authentication** is
  **Set on the Single sign-on card**, and the **Last successful SSO use**.
- **SCIM provisioning** — its **Status** and the **Last authenticated request**.
- **Operational activity** — a note that these status cards report only
  non-sensitive identity activity. No credential is ever displayed; the **Single
  sign-on** card below shows the provider's settings, but never its secret or a
  token.

SSO and SCIM are set further down the same overview, on the **Single sign-on** card;
see [Set up single sign-on](#set-up-single-sign-on).

## Find a user

**Users** lists every account with its **Avatar**, name and email, **Role** (User or
Owner), **Access** and **2FA** (On or Off). Access reads **Active**, **Pending** (an
account Staff made that waits for its invitation to be accepted), **Disabled by an
Owner**, **Disabled by Staff** or **Temporarily locked out** — the last after five
wrong passwords in a row, which clears by itself after 15 minutes. An account Staff
manages carries **Managed by Staff · no.** and its employee number under the address
(see [Accounts Staff manages](#accounts-staff-manages)).

The cards above the list count **Active**, **Disabled**, **SSO** (accounts that
sign in through the identity provider) and **Admins** (Owners); select one to filter
the list by it, and select it again to clear the filter. **Pending invitations**
opens the Invitations page. **Search users** matches name or email.

Your own row has no actions menu — "This is your account. Manage it in Account
settings."

## Add a user

Choose **Add user**. The **Add a user** dialog asks you to "choose how this person
should get started":

- **Send invitation** — the person receives an email and chooses their own password
  (see [Getting started](/en/user/getting-started/#accept-an-invitation-and-create-your-account)).
- **Set initial password** — you choose the **Initial password** ("12+ characters
  with upper and lower case letters and a digit") and give it to the person
  yourself. The account exists at once, with its email already confirmed.

In both cases, fill in **Display name**, **Email** and **Role** — **User** or
**Owner** — and choose **Save**. **Invitation sent** or **User created** confirms it.
"An account already exists for this email address." means that email is taken;
inviting it again replaces any invitation that was still pending for it.

**User** is "the standard user role with no permissions by default": a new User sees
only the dashboard until a role grants an app, as described under
[Roles and access](#manage-roles-and-access). **Owner** gives full access to
everything.

## Edit a user, reset a password

The actions menu on a row (the three dots) offers:

- **Edit details** — change the **Display name**, **Email** or **Role** in the
  **Edit user** dialog. Any change to these ends every session the person has, so
  they must sign in again; a new email also cancels pending invitations to the old
  and the new address.
- **Send reset email** — mails the person a password reset link: "The user will
  receive instructions shortly."
- **Set initial password** — choose a new password for the person on their behalf.
  "The administrator-chosen password is active now." Use this for someone locked
  out without email access, and ask them to change it afterwards.

Resetting another administrator's two-factor authentication is not offered on this
screen; it exists in the API for an Owner whose own session is MFA-verified.

## Disable, enable or delete a user

To stop someone signing in without losing anything, choose **Disable access** and
confirm **Disable access?** — "_name_ will no longer be able to sign in." Their
sessions end at once, and the account keeps its data, roles and sign-in methods.
The row then reads **Administrator-disabled**, and the same menu offers **Enable
access** — "_name_ will be able to sign in again." — which lets them back in with
the password they had.

**Delete user** asks **Delete this user?** — "This permanently removes the account
and cannot be undone." The account disappears from the list together with its
sessions, role assignments, passkeys and authenticator. What the person created in
the apps — customers, hours, expenses, projects, invoices — stays where it is; only
the account goes.

Deletion is refused in three cases, each explained on screen: the last active Owner;
an account that was provisioned by SCIM or signed in through the identity provider
("This user has SCIM or federated identity history and cannot be deleted." —
disable it instead, or deprovision it from the provider); and an Owner who has
granted delegations that still exist — revoke those first. An account Staff manages
has **Delete user** greyed out, with the reason beneath it (below).

## Accounts Staff manages

When HR turns the account flag on for a person in [Staff](/en/user/staff/), Staff's
provisioning creates their account ahead of their first day, enables it on the day and
disables it after their last — never deleting it. Such an account is **Managed by
Staff**, with the employee number that ties it to the person, and the Users page shows
it that way. Staff acts only on accounts it manages: an account someone already had
becomes Staff's only when an Owner adopts it.

**A pending account.** Staff creates the account **Pending**: it exists, with the
person's name and the roles Staff may give, but it cannot be used until the person
accepts the invitation Staff sends them and chooses a password (see
[Getting started](/en/user/getting-started/#when-your-account-was-set-up-from-the-staff-register)).
Its menu's **Enable access**, **Send reset email** and **Set initial password** are
greyed out — "Not accepted their invitation yet: the account opens only when they
accept it." — and making it an Owner is refused: "A pending user cannot be made an
Owner until they have accepted their invitation." You may still **Disable access**;
Staff then does not enable it, and HR sees why on the person's Account tab.

**Who disabled it.** Staff disables an account the day after the person's last
employment ends; the row reads **Disabled by Staff**. When you disable an account
yourself it reads **Disabled by an Owner**, also over Staff's own disable, and Staff
never enables an account an Owner disabled. **Enable access** clears either.

**Adopt an account for Staff.** For a person who already had an account before HR
registered them, open the account's menu and choose **Adopt for Staff**. Type the
person's **Employee number** as Staff has it — "1 to 16 characters: letters a–z,
digits and hyphens." — and choose **Adopt**: "Staff now manages the account", and
Staff's accounts worker takes it over on its next run. The menu does not offer it for
an Owner, and refuses, in words:

- "Another user already has this employee number." — another account holds it,
  however it is cased.
- "An Owner or a system administrator cannot be managed by Staff; an Owner looks after
  those accounts by hand."
- "Enter 1 to 16 characters, each a letter a–z, a digit or a hyphen." — a number
  outside Staff's alphabet, said before anything is sent.

**Stop managing.** **Stop managing** on a managed account asks **Stop Staff managing
this account?** — "Staff will no longer enable, disable or update _name_. The account
stays as it is now; a pending account stays pending until the person accepts their
invitation or you delete it." Afterwards Staff leaves the account alone and HR sees
"not managed" on the person's Account tab. Making a managed account an Owner stops
Staff managing it in the same change; the **Edit user** dialog says so when you choose
**Owner**: "Making them an Owner stops Staff managing this account." To give an
adopted account another employee number, stop managing it and adopt it again with the
new one.

**Delete.** A managed account cannot be deleted: **Delete user** is greyed out, with
"Staff manages this account, so it cannot be deleted. Stop managing it first."
beneath it. Stop managing it, then delete it as any other.

What Staff does on its side, and when, is described in the
[administration guide for Staff](/en/admin/staff/).

## Follow up invitations

**Invitations** lists every invitation ever sent, with **Recipient**, **Role**,
**Status** and **Created / expires**. **Invite someone** takes you to the Users page.
Filter by **All**, **Pending**, **Expired**, **Revoked** or **Accepted**, or search by
name or email; the list refreshes itself every half minute.

An invitation is **Pending** from the moment it is sent, and becomes **Accepted**
when the person creates their account, **Expired** when its date passes — seven
days after sending unless the installation sets another lifetime — or **Revoked**
when you withdraw it. The actions menu on a pending or expired row offers:

- **Resend** — issues a fresh link with a fresh expiry and emails it; any earlier
  pending invitation for the address stops working. This is the way to renew an
  expired one.
- **Revoke** — after **Revoke invitation?** ("_email_ will no longer be able to use
  this invitation."), the link is dead. Nothing can be done with an accepted or
  revoked invitation.

A revoked invitation says why under its status:

- **Revoked by an Owner** — you or another Owner revoked it.
- **Replaced by a newer invitation or an account** — the address was invited again, or
  an account was made for it.
- **The email could not be sent** — see below.
- **The user's address changed** — the account the invitation was for got another
  address.
- **Staff deactivated the account** — Staff disabled the pending account it was for.

If the email cannot be sent, the invitation is revoked immediately and the error is
shown, so a link nobody received never stays live. The email templates and links
are described under
[Invitation and recovery URLs](/en/admin/authentication/#invitation-and-recovery-urls).

## Manage roles and access

**Roles & access** builds "additive permission sets" and assigns them. It opens for
an Owner, and for anyone holding a delegation (below); everyone else sees "Your
account cannot manage roles and access." The page has three tabs.

### Roles

**Role permissions** lists every role. Two are built in and marked **Protected**:
**User**, with no permissions, and **Owner**, with full installation access. The
badge counts the **managed custom** roles, which you create.

**Create custom role** opens a dialog with:

- **Internal name** — lowercase letters, numbers and hyphens; it cannot be changed
  after saving.
- **Display name** and **Description** — what people see.
- The permission catalog, grouped by module and category (Identity ·
  Administration, Customers · Contacts, Time · Time, Invoices · Invoices, and so
  on), each with a short description of what it allows. A permission that reveals
  more than it seems — a person's cost rate, for example — carries a **Sensitive**
  badge.

Pick at least one permission and choose **Save role**. **Edit** on a custom role
opens the same dialog; the delete button asks **Delete custom role?** — "Assignments
using this role may change." — because everyone who held it loses what it granted.

### Roles Staff may give

Below the roles, an Owner sees **Roles Staff may give**: the custom roles every account
[Staff manages](#accounts-staff-manages) is given when Staff enables it, besides the
built-in User role. Staff creates no account until you name at least one; until then
the section says "No role named: Staff creates no accounts until you name one." and
Staff's dashboard lists the people waiting.

Each custom role is listed with a box to tick and the permissions it carries. Tick the
roles employees should have and choose **Save roles Staff may give**, which is available
once you have changed the set: the whole set is saved at once, so a role you untick is
taken out of it — "Roles Staff may give saved".

A role that grants administration cannot be one of them, so that HR, who decides who
gets an account, can never hand anyone administration: one carrying **Manage
identity**, **Manage staff**, **Decide absence**, **Reveal and erase personal data** or
any other permission that cannot be delegated is marked **Grants administration** and
cannot be ticked. One that came to be in the set before can still be unticked; saving a
set that keeps it is refused, naming the role and the permission — "_role_ carries
_permission_ (_key_), which grants administration; Staff may not give it." — and
nothing changes. A role in the set keeps that limit when edited: giving it such a
permission in the role dialog is refused with "This role is one Staff may give, so it
cannot receive a permission that grants administration. Take it out of Roles Staff
may give first." Deleting a role takes it out of the set.

Permissions are additive: a person holds the union of their roles' permissions. What
each permission unlocks in an app is listed in that app's reference page, for example
[Customers](/en/reference/customers/), [Projects](/en/reference/projects/),
[Time](/en/reference/time/), [Expenses](/en/reference/expenses/),
[Invoices](/en/reference/invoices/) and [Staff](/en/reference/staff/). The descriptions
in the catalog say what a title does not: in Invoices, **Issue invoices** also sends a document by e-mail or as EHF
and cancels or resolves its EHF transmissions, records that an invoice was handed
over, makes or revokes a Vipps pay link and takes a Vipps payment on site — and, with
**Create invoices**, makes a quick invoice; **Manage invoicing** also holds the Peppol id,
the KID agreement, the e-invoicing access point, the reminder settings, the collection
rates, the Vipps keys and the payment settings, and abandons a stuck Vipps payment; and
**Register payments** covers all of credit control — bank imports and the exception
queue, reminder runs and letters, holds, hand-offs to collection, a customer's reminder
policy and marking a day's Vipps payments reconciled. In Staff,
**Reveal and erase personal data** is the only permission that shows a national identity
number or a bank account — **Manage staff** does not — and **Decide absence** can be
granted already but allows nothing until absence arrives in Staff. **Manage identity**
(`identity:manage`) is the one permission of the identity module itself.

### Assignments

**User assignments** adds or removes custom roles for a person. Choose a **User**
(you cannot pick yourself) to see their **Current roles**, their **Effective
permissions** (an Owner shows **All permissions**) and the **Assignable custom
roles** picker. Ticking or unticking a role saves at once: **Roles assigned**. The
built-in User and Owner roles are not assigned here but under
[Users](#edit-a-user-reset-a-password), through the account's Role.

### Delegations

**Delegated administration** lets an Owner hand part of role administration to
someone "without granting business data access". Only Owners see the **Delegate
administration** button; a delegate sees "Delegations are managed by the account
owner."

**Delegate role administration** asks for the **Administrator** (not yourself), the
**Delegable permissions** the person may put into roles, the **Stewarded custom
roles** they may edit and assign, an optional **Expiry** in the form
`2027-01-31T00:00:00Z`, and whether they **May create custom roles**. **Grant
delegation** confirms: "This grants administration only, not data access."

The delegate then gets **Roles & access** in their avatar menu. Inside it they see
the protected roles and the roles stewarded for them — other custom roles read
**Outside current delegation boundary** — and must pick a **Delegation scope** or
**Assignment scope** before creating a role or assigning one. Roles they cannot
touch are left as they are: "Roles outside your delegated scope are kept."

Active delegations are listed under the button, each with **Revoke** — "This
removes the delegated administration boundary." — which ends the delegate's access
to this page at once unless another delegation remains.

### Groups

Groups — sets of users with roles mapped to them — exist in Vantigo for SCIM
provisioning and the API, not as a screen here. When the identity provider manages
groups, their members receive the mapped roles automatically; see
[SSO and SCIM operations](/en/admin/sso-scim/).

## Set up a new installation

The very first time anyone opens a fresh installation, the sign-in page sends them
to **Set up Vantigo**: "Create the first Owner account for this installation. It
becomes the full administrator, with every permission and access to system
administration." Fill in **Display name**, **Email** and **Password** and choose
**Create Owner account**; you are signed in and, when the installation requires
two-factor authentication for administrators, taken straight to set it up. Whoever
submits this form first owns the installation, so do it before the address is
reachable by anyone else. Once an Owner exists the page only says **Setup
unavailable** — "This Vantigo installation has already been set up." An installation
can instead be configured to email the first Owner an invitation; see
[First Owner and local accounts](/en/admin/authentication/#first-owner-and-local-accounts).

## Workspace settings

The **Workspace** card on the overview holds what is true of the whole installation
and is read by every module. Today that is one setting, the **Business time zone**:
the calendar every bookkeeping day is judged in — the day an invoice is issued and
falls due, a period lock, the day a trip departed, "today" in Staff. It is
`Europe/Oslo` until an Owner changes it. The Staff and Expenses settings pages show
it too, but only here can it be changed.

1. On the overview, under **Workspace**, pick the zone from the list. Use a city
   zone such as `Europe/Oslo`; a name the server and its database read differently,
   such as `CET`, is refused with a sentence saying so.
2. Choose **Save workspace settings** and confirm **Change the business time zone?**
   The dialog says what changes: from the next request on, "today" and every day
   derived from a point in time are read in the new zone. Dates already written —
   an invoice's issue date, a trip's per-diem days — stay what they are.

Every change is recorded in the authorization audit trail as
`workspace.business_time_zone_changed`, with the zone before and after. Where a
person sees a point in time, they see it in this zone too, until a personal time
zone of their own exists.

### Set up single sign-on

The **Single sign-on** card, across the full width below the overview's other cards,
connects the
workspace to a workforce identity provider — Microsoft Entra ID or Google Workspace —
so that people sign in with their work account, and gives a directory the token it
provisions accounts with (SCIM). It needs the Owner role and, when the installation
requires it, a second factor. What to do at the provider — the app registration, the
secret, the redirect — is described step by step in
[SSO and SCIM operations](/en/admin/sso-scim/).

To let people sign in through a provider:

1. Copy the **Callback URL** and register it with the provider.
2. Choose the **Provider**: **Microsoft Entra ID** or **Google Workspace**.
3. Fill in the **Authority**, the provider's issuer address —
   `https://login.microsoftonline.com/<tenant-guid>/v2.0` for Entra; for Google the
   card fills in `https://accounts.google.com` — and the **Client ID**.
4. Give the credential: paste the **Client secret**, or, for Entra on a platform that
   supplies one, choose **Token file** as the **Credential** and enter its path. Once a
   secret is saved the field is blank and reads "Set on _date_. Leave it blank to keep
   it.": type a new one only to replace it. A new **Provider** or **Client ID** needs
   its own secret or token file.
5. For Google, list the **Domains** people may sign in from.
6. Optionally, give the button on the sign-in page a **Display name**; it is
   `Workforce SSO` when left empty.
7. Choose **Save**. The card says "OIDC settings saved"; a field the provider would
   not accept is marked with the form it must have.

Within 20 seconds the sign-in page offers **Continue with** and the display name, on
every server, with no restart.

If you change the **Authority** while people already sign in through the provider,
Vantigo asks first — "Change the provider's authority?" — because people linked
under the old authority are not recognised under the new one, so their next sign-in
through the provider is refused while their account exists. Choose **Change
authority** only when you are moving to another tenant or provider. **Turn off** — or
**Off** as the provider and **Save** — asks first too, "Turn off SSO?", and while
people are linked to the provider a second dialog, "People are linked to this
provider", asks again. If someone else saved the card after you opened it, it says
"The settings have changed": choose **Reload** and make your change again.

To give a directory its SCIM token:

1. Copy the **Endpoint URL** into the directory's provisioning settings.
2. Choose **New token**. When a token already exists, you are asked first, "Issue a
   new token?", how long to "Accept the current token for (hours)" — 1 to 24, 24 by
   default — so the directory can switch without a gap; choose **Issue token**.
3. The dialog, "The new SCIM token", shows it **once**: choose **Copy token**, paste
   it into the directory, and only then choose **Done**. It will not be shown again;
   if it is lost, issue a new one.
4. Once the directory uses the new token, choose **End overlap** to stop accepting
   the old one, or let the overlap run out.

**Turn off** in the SCIM block asks first, "Turn off SCIM?", and stops SCIM: within
20 seconds the directory's requests are refused, and the accounts and groups it
created stay as they are.

When the settings were imported from the installation's environment, the card says
so — **Imported from the environment** — and asks for the variables to be removed;
that is for whoever runs the installation
([Before the card](/en/admin/sso-scim/#before-the-card-the-environment-variables)).
The notice stays until the next change made on the card.
Every change is recorded in the authorization audit trail as `sso-settings.changed`,
never with the secret or a token.

## System administration

The account created at setup is also the installation's **system administrator**,
a status no screen can grant to anyone else. It adds **System admin** to the avatar
menu, and opening Vantigo's root address lands a system administrator there rather
than on the dashboard.

The page, **System status**, controls maintenance mode for the whole system. Under
**Maintenance mode** ("Temporarily block non-administrators while system
maintenance is in progress."), switch on **Enable maintenance mode**, optionally
write a **Message shown to users** (up to 500 characters) and choose **Save
maintenance settings**. Within half a minute every other user sees **Temporarily
unavailable** with your message, or "Vantigo is undergoing maintenance. Please try
again later." when there is none, and cannot use the application until you switch
it off. System administrators keep working, with a yellow banner — **Maintenance
mode is active** — on every page as a reminder.

Signing a user out everywhere is also a system-administrator operation, available
through the API rather than a screen; see
[Session lifetime and revocation](/en/admin/authentication/#session-lifetime-and-revocation).

## When SSO or SCIM provisioning is in use

With workforce single sign-on configured, people sign in with **Continue with
_provider_** and their accounts show in the **SSO** count on the Users page. Their
password and second factor live with the provider, and their own Security page says
**Local password unavailable**. With SCIM
provisioning, the provider creates, updates and deactivates accounts and groups by
itself, and such an account cannot be deleted from the Users page — disable it, or
let the provider deprovision it. Owner accounts are protected from the provider:
SCIM cannot change or remove them, and a local Owner with a password remains the
way in if the provider is unavailable — also to the **Single sign-on** card, where an
Owner [sets both up](#set-up-single-sign-on). The operator runbook is in
[SSO and SCIM operations](/en/admin/sso-scim/).

An account the provider creates for someone in Staff whose account Staff had the
provider make — the **Microsoft Entra ID** account kind — arrives on the Users page
already marked **Managed by Staff · no.** with the person's employee number, and no
invitation pending: Staff announced it, and Vantigo recognises it by the employee number the
provider sends. You do not adopt it for Staff. An account nobody announced arrives as
before, and an Owner adopts it if it is Staff's
([the announced account](/en/admin/authentication/#the-announced-account)).
