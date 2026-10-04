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

**Overview** opens the **Admin dashboard**, "a read-only overview of accounts and
identity integrations":

- **People** — **Total**, **Active**, **Disabled** accounts and **Pending
  invitations**, with a link to **View users**.
- **Invitations** — the pending count and **Manage invitations**.
- **Access control** — **Manage roles & access**.
- **Identity integrations** — whether **SSO** is on and which provider (Microsoft
  Entra ID, Google Workspace, or just **Enabled**), that **Client authentication** is
  **Managed by deployment**, and the **Last successful SSO use**.
- **SCIM provisioning** — its **Status** and the **Last authenticated request**.
- **Operational activity** — a reminder that this overview shows only non-sensitive
  identity activity; credentials and provider configuration are never displayed.

SSO and SCIM are configured by whoever runs the installation, not from this screen;
see [SSO and SCIM operations](/en/admin/sso-scim/).

## Find a user

**Users** lists every account with its **Avatar**, name and email, **Role** (User or
Owner), **Access** and **2FA** (On or Off). Access reads **Active**,
**Administrator-disabled** or **Temporarily locked out** — the last after five wrong
passwords in a row, which clears by itself after 15 minutes.

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
granted delegations that still exist — revoke those first.

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

Permissions are additive: a person holds the union of their roles' permissions. What
each permission unlocks in an app is listed in that app's reference page, for example
[Customers](/en/reference/customers/), [Projects](/en/reference/projects/),
[Time](/en/reference/time/), [Expenses](/en/reference/expenses/) and
[Invoices](/en/reference/invoices/). The descriptions in the catalog say what a title
does not: Invoices' **Issue invoices** also sends a document by e-mail or as EHF and
cancels or resolves its EHF transmissions, and **Manage invoicing** also holds the
Peppol id, the KID agreement and the e-invoicing access point. **Manage identity**
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
way in if the provider is unavailable. The configuration and the operator runbook
are in [SSO and SCIM operations](/en/admin/sso-scim/).
