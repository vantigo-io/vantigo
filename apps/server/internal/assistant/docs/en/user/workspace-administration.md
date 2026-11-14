---
title: Workspace administration
description: Users, invitations, roles and access, delegated administration, first-run setup and system administration.
sidebar:
  order: 90
sources:
  - apps/host/frontend
  - apps/server/internal/identity
  - apps/server/internal/db/migrations/00098_identity_ai_task_models.sql
---

Workspace administration is where an **Owner** manages who can use Vantigo and what
they may do. Open the avatar menu at the top right and choose **Workspace admin**;
its sidebar has **Overview**, **Users**, **Invitations**, **Roles & access** and, while
Manufacturing is on, **Stations**, and,
under **Integrations**, **Microsoft Entra ID**, **Google Workspace**, **Meta**, **Shipping**, **Bank** and, once Vantigo has set up AI for the
installation, **AI models** and **Assistant**.

Everything here needs the Owner role, with one exception: a person to whom an Owner
has delegated role administration sees **Roles & access** alone, both in the avatar
menu and in the sidebar; someone who holds `entra:manage`, `google:manage`, `orders:carriers-manage` or `invoices:manage` without being an Owner
sees **Integrations** alone ([below](#integrations)); and someone who holds `identity:manage`
sees **Stations** ([below](#shop-floor-stations)). The last active Owner can never be
demoted, disabled or deleted, so the workspace always has one.

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
(see [Accounts Staff manages](#accounts-staff-manages)). While SSO is on, the
**Single sign-on** column says who is connected to the provider (see
[Connect an account to single sign-on](#connect-an-account-to-single-sign-on)).

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

While the Assistant module is on, the **Edit user** dialog also carries the switch **Has the
assistant**, which takes effect when the assistant is set to *Per person*
([Assistant](#assistant)).

While Manufacturing is on, the dialog also shows the person's **Shop-floor station**
credentials: whether it **Has a PIN** — **Clear PIN** removes it; only the person sets
one — and whether it **Has a badge**. To enrol a badge, put the cursor in **Badge**, scan
the card with the reader and choose **Enrol badge**; **Remove badge** takes it away. A card
already enrolled for someone else is refused.

Resetting another administrator's two-factor authentication is not offered on this
screen; it exists in the API for an Owner whose own session is MFA-verified.

## Shop-floor stations

**Stations** lists the shared tablets on the shop floor where operators switch in with a
PIN or a badge ([working at a station](/en/user/manufacturing/#work-at-a-shared-station)).
It needs `identity:manage`, and is there while Manufacturing is on. Each row shows the
station's **State** (*Waiting to be paired*, *Paired* — with *New code waiting* while a new
pairing code has not been used — *Pairing expired*, *Not paired* or *Revoked*), its **Last
activity** with the address, who is **Signed in** at it, and **Failed sign-ins** when
repeated wrong PINs or badges there mailed the administrators.

1. Choose **Register a station** and give it a **Name**. Set the **Idle timeout** (keep it
   short: anyone at the tablet can tap a signed-in person's chip and work as them until
   then), whether to **Require the PIN with a badge** (on unless only cards are used in a
   controlled area), the **Allowed networks** it may be used from (empty for any) and the
   **Work centers** it serves — changing these needs Manage bills of materials and work
   centers; without it the field only shows them.
2. Choose **Save**. The **Pairing code** appears once: on the tablet, open the address shown
   and enter the code within 15 minutes. If the work centers could not be saved, the code
   is shown all the same and a message says so: edit the station to set them.
3. To change a station, choose **Edit** in its row's menu. **New pairing code** pairs it
   again — after its 90 days, or on a new tablet — and **Revoke** stops it at once and signs
   everyone at it out. If the work centers cannot be read, the form says so and does not
   save; close it and try again.
4. Turning **Require the PIN with a badge** off asks for your **Current password** unless
   you signed in within the last ten minutes, and the administrators are mailed.
5. Beside each person **Signed in**, **End** signs them out of that station at once; the
   tablet shows their chip as **Signed out** within a few seconds. A
   person with failed sign-ins in a row there is listed with how many; **Reset** lets them
   try again at once.

On a user's dialog, **Shop-floor station** shows whether they have a PIN and a badge —
*Needs re-enrolment* when one no longer works and must be set again. **Clear PIN** removes
the PIN. To enrol a badge, scan the card into **Badge**, add your **Current password**
unless you signed in within the last ten minutes, and choose **Enrol badge**: a badge can
only be enrolled for someone who may work on the shop floor, the person is mailed about it,
and a card that cannot be enrolled is refused without saying why. Setting or clearing a PIN
or a badge signs the person out at every station.

The administrator's details — the token, the backoff and the alerts — are under
[Shop-floor stations](/en/admin/authentication/#shop-floor-stations).

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

## Agents among the users

A **virtual employee** is an AI agent, and its account is a user of a separate kind. It
is listed in a section of its own after the people, never among them, marked with the **AI**
badge on its avatar, and the counts treat the two apart: the workspace has so many
people and so many agents, and nothing limits either. An agent
never signs in, has no password, no authenticator and no sign-in link, so none of the
actions above — **Edit user**, a password, **Disable access**, **Delete user**, a link to
single sign-on, an invitation — applies to it. It is made, renamed, paused and removed
under [Virtual employees](/en/user/agents/), and it never receives mail from Vantigo.

Give an agent its roles under **Roles & access**, as for anyone. Two things are refused
for an agent: the **Owner** and **SystemAdmin** roles, and a delegation. An agent is not
offered where a person is chosen for a customer, a project, a rate card or a staff
record; only the assignee of a conversation in Communications offers agents.

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

## Connect an account to single sign-on

When single sign-on is turned on in a workspace that already has users, those users
still sign in with a password, and an address alone never links a provider sign-in to
an existing account: their first attempt through the provider is refused. You, as an
Owner, say that an account and a directory account are the same person, and the
connection completes the next time that person signs in through the provider (or the
provider pushes the account). **Users** gets a **Single sign-on** column for this
while SSO is on; it is not there while SSO is off, and your own row has no button.

The column reads one of three things:

- **Connected** with a green check — the person has signed in through the provider and
  the account is linked.
- **Awaiting first sign-in** — you have announced the connection and it has not
  completed yet. The announced user principal name (or the object id, when you gave
  only that) stands beneath.
- **Connect…** — a button: nothing is connected or announced.

**Connect an account.**

1. In **Users**, choose **Connect…** in the account's row, or **Connect to single
   sign-on…** in its menu.
2. In **Connect _name_ to single sign-on**, check **User principal name**. It is filled
   in with the account's address; change it when the person's directory account has
   another name. Under **More**, **Object id** is the directory account's id, for when
   you have it instead or besides. When the provider's directory can be searched, the
   dialog starts with **Search the directory**: type two characters or more of the
   person's name or address and pick their account, which fills in both fields (see
   [Pick the account in the directory](#pick-the-account-in-the-directory)).
3. Choose **Connect**. The row now reads **Awaiting first sign-in**.

**Connect many at once.** When the provider's directory can be searched, **Find
matches in the directory…**, next to the search in **Users**, proposes for every user
not yet connected the one directory account that matches them exactly, and **Connect
selected** connects the ones you keep checked; see
[Review the exact matches](#review-the-exact-matches).

At the person's next sign-in through the provider — or when the provider pushes their
account — Vantigo links the directory account to the account you named, whatever
email address the provider reports, and signs them in as that account. Their name,
address and roles stay as they were. The announcement then goes, and the row reads
**Connected**.

**Change or cancel it.** While a row reads **Awaiting first sign-in**, its menu offers
**Change the connection…**, which opens the same dialog and replaces what you gave,
and **Cancel the connection**, which withdraws the announcement.

**Disconnect.** For a **Connected** account the menu offers **Disconnect from single
sign-on…**. The confirmation says the person keeps their account and signs in with a
local credential, if they have one. An account with no password then cannot sign in
until you send a password reset or connect it again. The provider's own record of the
account, if it pushed one, is not removed; that ends when the provider deletes it.

Connecting is refused, in words, when:

- single sign-on is not turned on;
- the account is **Pending**: the person's way in is their invitation (or the account
  Staff announced for them);
- the account is already connected: disconnect it first;
- "Another user is already connected to this account." — the directory account is
  announced for, or already named by, another user, or the provider's provisioning
  already gave it to another user;
- the principal name is not an address, or neither field is filled in.

A disabled account can be connected: the person signs in only once you enable it.
After the provider's **Authority** is changed, an announced connection is for the old
provider and fulfils nothing: the row reads as not connected, and connecting the account
again replaces the old announcement.

### Look the accounts up in the directory

You need not type the accounts when the configured provider's directory can be
searched: single sign-on is on, and the module that serves the provider's directory is
enabled — **Microsoft Entra ID**, connected to the tenant in its steps 1 to 3
([Microsoft Entra ID](/en/user/entra/#connect-existing-users)), or **Google Workspace**,
connected to the organisation and tested
([Google Workspace](/en/user/google-workspace/#connect-existing-users)). Vantigo then
looks the accounts up in the directory of the provider single sign-on is set to. Both ways below are an **Owner**'s, signed in with a
second factor when Owners must use one; while there is no directory to search, they are
not offered and the typed fields are all there is.

#### Pick the account in the directory

**Connect…** in a user's row opens **Connect _name_ to single sign-on**, which then
starts with **Search the directory**. Type two characters or more of the person's
name, address or principal name; after a pause the list shows the matching accounts,
by display name, each with its principal name, its employee number, *Guest* for a guest
invited from another organisation, and *Disabled* when the account is. An account
somebody already holds reads *Awaiting* and the user's name (or *Awaiting another
user*), *Provisioned to another user* or *Announced by Staff*, and cannot be picked. At
most 25 accounts are shown (20 from Google Workspace); when there are that many, it asks
you to narrow the search. From Google Workspace the list shows the accounts whose full
name or address — the primary email or an alias — starts with what you typed; the
principal name is the primary email, and there are no guests.

Picking an account fills in **User principal name** and, under **More**, **Object id**.
The two belong together: change the principal name and the object id is cleared;
search again after a pick and both are cleared. Check them and choose **Connect**, as
before. The typed fields stay: you can still type the name yourself, and they are all
there is while the directory cannot be read.

#### Review the exact matches

1. In **Users**, choose **Find matches in the directory…**, next to the search.
2. **Users who match an account in the directory** opens. A line at the top counts the
   *Users not yet connected*, the ones *Matched exactly*, and the ones *Left out because
   more than one account matched*. Below it each proposal is a row: the **User**, with
   their address; the **Account in the directory**, with its display name, principal
   name, employee number when it has one, and *Disabled* when the account is; and
   **Matched by**: **Mail**, **Principal name**, **Employee number**, or several.
3. Check the rows. A row starts ticked only when the account is enabled, the
   principal name matched, and the user is not an Owner. Any other row starts
   unticked and is marked **Check this match**, with the reason: *Matched by mail or
   employee number only, not by the principal name the person signs in with. Make sure
   it is the same person.*, or *This user is an Owner. Whoever holds this account will
   sign in with the Owner's access.* Tick those you have checked, and clear the ones
   you do not want.
4. Choose **Connect selected**, which shows how many are checked. Vantigo connects the
   pairs one at a time, and each row's **Result** says *Connected*, or why it was
   refused in words — for instance that another user is already connected to the
   account. **Users** is refreshed when it is done.

A user is proposed only when the match is **exact**: their address is the account's
mail or principal name, ignoring upper and lower case, or their employee number is
the account's — and every one of those that matched points to the same account. In
Google Workspace the primary email is both the mail and the principal name, an alias
counts as **Mail**, and the employee number is the one Google keeps as the account's
organization id. There
is no guessing on names or similar addresses. Left out are:

- a user who matches more than one account, or whose account also matches another
  user — counted as *Left out because more than one account matched*; connect them one
  at a time;
- guest accounts, invited from another organisation: they are never proposed and not
  counted;
- an account somebody else already holds: one announced for another user, one the
  provider's provisioning gave to another user, or one Staff announced for a new
  person. An account the provider's provisioning already gave to the same user is still
  proposed;
- users already connected, or awaiting their first sign-in; **Pending** users; and you
  — you connect yourself by signing in through the provider.

When no one matches, the window says so and that you connect them one at a time from
the **Single sign-on** column. When not every user was compared (both providers
compare the first 1,000; Google Workspace also reads at most 10,000 of its accounts, and
past that matches by address alone), it says so — connect these, then look again. Finding the
matches can take a while in a large directory; opening the window again meanwhile waits
for the same answer.

A connected row reads **Awaiting first sign-in** in **Users** until the person next
signs in through the provider, or the provider pushes their account; then the
connection completes and it reads **Connected**. Their name, address and roles stay as
they were.

A search or a review is refused, in words, when there is no directory to search —
single sign-on was turned off, or the provider's module is not enabled — and when the
directory cannot be asked: its connection is refused, it does not answer, or it asks
Vantigo to wait. Try again in a minute, and check the provider's connection.

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
[Invoices](/en/reference/invoices/), [Staff](/en/reference/staff/),
[Planning](/en/reference/planning/) and [Orders](/en/reference/orders/). The descriptions
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
number or a bank account — **Manage staff** does not, though it keeps the competence
catalogue — and **Decide absence** decides anyone's absence and also verifies people's
competences and records their competence reviews, never the holder's own. In Planning,
**Use Planning** gives the app, the holder's own confirmed bookings and wishes, and
those bookings on the Calendar; **See the plan**, marked sensitive, shows the board,
every person's timeline and every project's code, name, customer, status and dates,
bids included, with the requests and the intake; and **Plan**, also sensitive, writes
the allocations and their terms — a customer contract's notice, handover and
non-compete — and the requests, finds a request's candidates and fills it, reads the
planners' notes and the people's wishes, and keeps the Planning settings. A
planner also needs **View the staff directory** from Staff, since the plan shows only
the people Staff answers them. In Calendar, **Use Calendar** gives the app, **See
colleagues' calendars** a colleague's busy and free times, and **Manage Calendar
settings**, marked sensitive and in the Administration category, the channel invitations to
people outside the company are sent from, since it decides whose address customers' mail
comes from ([where invitations are sent from](/en/user/calendar/#where-invitations-are-sent-from)). **Manage identity**
(`identity:manage`) is the one permission of the identity module itself. In Orders, every
permission needs **Use Orders** beside it; **Confirm orders** also accepts a quote straight
into a confirmed order; **Manage Orders settings** keeps the quote templates as well and
cannot be delegated; **Manage subscriptions** makes, changes, pauses and ends
subscriptions; **Manage deliveries** makes, picks, posts and cancels deliveries; and
**Manage carriers** is listed for the shipping still to come and does nothing yet
([Orders](/en/admin/orders/#give-people-access)). In Inventory, **Use expired lots** lets a person
take a lot past its expiry date on a delivery or a production, when they choose that lot
themselves; it does nothing until lot tracking is switched on
([Inventory](/en/reference/inventory/#expiry-and-today)). **Manage lots** lets a person change a
lot's dates, supplier lot and note, add and remove its files, and put and release its holds
on the lot's page ([Inventory](/en/user/inventory/#lots-and-serial-numbers)).

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

## Integrations

**Integrations** is the second section of the **Workspace admin** sidebar: the one place
the company's connections to systems elsewhere are managed, and the Owner's settings for the
assistant. It has five items.
**Microsoft Entra ID** connects Vantigo to the company's Microsoft Entra ID tenant; its
page has four tabs — **Status**, **Connection**, **Accounts** and **Outlook and mail** —
described in [Microsoft Entra ID](/en/user/entra/#where-it-is-and-the-four-tabs). It is there
only while the Microsoft Entra ID module is on in this installation, and only for someone
who holds `entra:manage`; an Owner holds every permission. **Google Workspace** connects
Vantigo to the company's Google Workspace organisation; its page has four tabs —
**Status**, **Connection**, **Accounts** and **Calendar and Gmail** — described in [Google
Workspace](/en/user/google-workspace/#where-it-is-and-the-four-tabs). It is there only
while the Google Workspace module is on, and only for someone who holds `google:manage`.
**Meta** connects Facebook Messenger: the Meta app, the webhook, the Human Agent setting and
the Facebook Pages, described in [Meta](/en/admin/meta/). It is an Owner's alone, and is
there only while the Meta module is on.
**Shipping** (*Frakt*) keeps the carrier accounts Orders books shipments with — Bring's
Mybring user and API key, the customer numbers, the test mode — and the default account per
site, described in [Shipping](/en/admin/shipping/). It is there only while the Orders module
is on, and only for someone who holds `orders:carriers-manage`.
**Bank** connects the company's bank — Folio, with an API key — and chooses the
accounts Invoices reads incoming payments from and from which day, described in [Connecting
the bank](/en/user/invoices/#connecting-the-bank). It is there only while the Invoices
module is on, and only for someone who holds `invoices:access` and `invoices:manage`.
**AI models** is the Owner's
alone, and is there only while Vantigo has set up AI for this installation
([below](#ai-models)). **Assistant** is the Owner's alone too, and is there while the Assistant
module is on and AI is set up ([below](#assistant)).

- An Owner sees all of Workspace admin, with **Integrations** after the rest.
- Someone who holds `entra:manage`, `google:manage`, `orders:carriers-manage` or
  `invoices:manage` but is not an Owner sees **Integrations** alone in the sidebar — the way
  a person with delegated role administration sees **Roles & access** alone — with the pages
  their permissions open, and finds **Microsoft Entra ID**, **Google Workspace**,
  **Shipping** or **Bank** in the avatar menu. The steps that
  are an Owner's they read without their forms. They do not see **AI models** or **Assistant**.
- Without `entra:manage`, `google:manage`, `orders:carriers-manage` or `invoices:manage`
  (and not an Owner), there is no **Integrations** section. For an Owner there is none
  either while the Microsoft Entra ID, Google Workspace, Orders and Invoices modules are off
  and AI is not set up.

Your own accounts elsewhere, such as your Outlook, are connected under **Settings** ›
**Connections** ([Connections](/en/user/connections/)); the mailboxes Communications
sends from are its **Channels**, not here. [Configuration](/en/admin/configuration/) says
which settings are the workspace's and which the installation's.

### AI models

Some features ask an AI model for help — today, Communications' *Draft a reply* and
*Suggest the customer*. **AI models** (an Owner's page under **Integrations**) is where you
choose which model each of those jobs uses. It is there only while Vantigo has set up AI for
the installation: otherwise there is no item in the sidebar, and the address answers "page
not found".

**What you see.** The card titled **The plan** states what Vantigo set up, as facts with no field
to edit: the **Provider**, the **Models the plan allows**, and "An API key is configured by
Vantigo." — or, for a model that runs on your own network, which needs none, "No API key is
configured." The key itself is never shown, to anyone, anywhere. The provider reads
**OpenAI-compatible** for such a model, **OpenAI** for OpenAI and **Anthropic** when the models are Claude
models, whose questions and data are sent to Anthropic (the Assistant's settings say so). Below it, one card for each
module that uses AI, named as the module is, with a row for each **task** it performs: what
the model is asked to do and with what data, and a select of the models the plan allows.
Tasks are named in English. A module that is turned off is not listed.

A badge says where a model comes from while nobody has chosen: **Default** is the model the
task's module recommends, **Plan default** the plan's first model. A task someone has
chosen for has no badge.

**What you change.** Open a task's select and choose a model. It is saved at once — "Model
saved" — and the task uses it the next time it runs; every server follows within about 20
seconds. The choice is for that one task; the others are unchanged. If someone else changed
the same task after you opened the page, you are told "The AI models have changed", the page
reloads, and you choose again. When the plan allows only one model, every select is locked
with that model shown, and there is nothing to choose.

**What Vantigo decides.** The provider, the key, the models the plan allows and what each
task is — a task is added with the feature that uses it. A model you chose that the plan
later stops allowing is ignored: the task uses its module's recommendation, or the plan's
first model, until you choose again. Every choice is recorded in the audit trail as
`ai-task-model.changed`, with the model before and after, and nothing is recorded of what
the model was asked or answered beyond what the feature itself keeps (Communications keeps
digests and counts, never text:
[AI draft and customer suggestion](/en/reference/communications/#ai-draft-and-customer-suggestion)).
The settings that belong to the installation are in
[Configuration](/en/admin/configuration/#ai).

### Assistant

The [Assistant](/en/user/assistant/) answers people's questions from the documentation and
reads the data they may read. **Assistant** (an Owner's page under **Integrations**) is where you
decide who has it, how long conversations are kept, what it may use, how many tokens a person
may use a day and who has used it. It is there while the
Assistant module is on in this installation and the installation has an AI provider; without
one, the assistant is not offered to anybody. It is the Owner's alone.
[Configuration](/en/admin/configuration/#ai) says what the installation sets up.

**Who has the assistant.** Choose one of three, and it is saved at once:

| Mode | Who has the assistant |
| --- | --- |
| **Enabled for all** (the default) | Every active user. The per-person switches are ignored. |
| **Per person** | Only the people whose own switch is on. |
| **Disabled for all** | Nobody, whatever their switches say. |

The per-person switch is **Has the assistant**, in the **Edit user** dialog of a person under
**Users** (the actions menu on their row › **Edit details**). It saves by itself and counts only
in **Per person** mode; the dialog says so beside it. It is off until you turn it on. Someone
whose account is disabled or locked out has no assistant in any mode.

**How long conversations are kept.** **Keep conversations for** takes 1 to 60 months, 12 by
default; choose **Save** after changing it. A conversation is deleted, whole, once its last
activity is older than that, and the deletion runs once a day, so a shorter time takes effect at the next
daily run. Nobody, an Owner included, can read a person's conversations; only how long they live is yours
to set.

**Where the data goes.** A notice beside the settings says it plainly. When the model is on
this installation's own network: *The model runs on this installation. Questions and data are
not sent anywhere else.* Otherwise: *Questions and the data the assistant reads are sent to*
the provider — the questions, the documentation it finds and whatever it reads for the person
asking. Turning on **Can read** below for a provider that is not local is a decision to
let that data leave; the notice is repeated beside the switch.

#### What the assistant may use

The assistant reads through the same API the screens use, as the person asking, so it can only ever
use what that person may. This card decides how much of that it may use *at all*. The levels
can only **narrow** what the person may do, never widen it, and they never change what the person
sees on a screen.

- **Sensitive data** has two values, **Not available** (the default) and **Can read**. It sets the
  default level of every permission that is marked **Sensitive** in the role editor — in Customers,
  for example, the contacts, the timeline, the legal identity and the billing profiles. With **Not
  available** the assistant cannot read behind a sensitive permission even for a person who holds
  it.
- **The permission table** lists the permissions of the modules switched on, grouped by module and
  category, with the **Sensitive** badge where there is one. Each has a **level**, chosen in the row
  and saved at once:

  | Level | The assistant may |
  | --- | --- |
  | **Off** | Use no operation whose access rule names the permission. |
  | **Read** | Read behind the permission. Operations that change data are not available to it. |
  | **Read and change** | Use the permission as far as the person may. The assistant may **propose** changes behind it: each one is shown to the person on a confirmation card and runs, as them, only when they press **Confirm**. |

- **Defaults and overrides.** A permission that is not sensitive starts at **Read and change**; a
  sensitive one takes the **Sensitive data** switch's value, **Off** or **Read**. Only the levels
  you set yourself are stored, marked **Changed**, so a permission a later release adds takes its
  default. **Reset to default** on a row removes your choice, and the **Show** filter narrows the
  table to **All**, **Sensitive** or the **Changed** ones.
- **A change takes effect at the assistant's next step**, within about a second on any server, also
  in an answer already under way.
- **Operations whose rule names no permission** — those open to any signed-in person or to a business
  account — are not touched by these levels. Operations that belong to Owners and administrators,
  and file uploads and downloads, are never available to the assistant, whatever you set.
- **Every change is recorded** with the level before and after, the person who made it and when,
  in the assistant's own record. The [reference](/en/reference/assistant/#the-owners-audit) says
  where.

**Bulk changes obey the levels for every item.** When the assistant changes many records at once
([Bulk changes](/en/user/assistant/#bulk-changes)), each item is changed as the person, through the
same operation, so the level of the permission it names applies to every one of them. **Narrowing a
level stops a bulk change that is running**: the job checks the levels between its chunks of 100 items,
ends *Stopped* with the reason *the Owner narrowed what the assistant may change*, and the items it has
not reached stay unchanged. A bulk change waiting for the person's **Start** or **Continue** can no
longer be started or continued. What has already been changed stays changed.

A card that is waiting when you narrow a level can no longer be confirmed: pressing **Confirm**
answers *The assistant may no longer make this change*, and the person can still cancel it.

If someone else changed the settings while you had the page open, you are told *Somebody else saved
these settings*, the page reloads, and you make your change again.

#### Daily token budget

**Daily token budget per person** limits how many model tokens one person may use in a business day,
counting what they send and what comes back. It is empty by default, which is no limit. Enter a number
from 1,000 to 1,000,000,000 and choose **Save**; empty the box and save to remove the limit again. The day
is the business time zone's.

When a person has used the budget, their next message is refused with *You have used the assistant's token
budget for today*, and an answer that reaches it on the way stops and says so; a change the person had
already confirmed is not undone. The assistant is available to them again the next business day. Deleting
a conversation does not give tokens back: the use is counted as it happens. The budget is saved under the
same version as the other settings and is recorded in the assistant's audit with the value before and after.

#### Use of the assistant

**Use of the assistant** lists, per person and business day, how many conversations they began, how many
times they asked (runs), the model tokens that went in and came out, and how many changes they confirmed and
how many of those failed. Choose **Last 7 days**, **Last 30 days** or **Last 90 days**. It is for cost and for
abuse. It shows **no content**: no titles, no questions, and not what a change was; nobody, an Owner
included, can read a conversation. The counts are kept as long as conversations are.

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
   supplies one, choose **Token file** as the **Credential** and enter its path, inside
   the directory the deployment allows for it. Once a
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

The same card is on a provider's own page, filled in for you: **Microsoft Entra ID**'s
**Accounts** tab fills in the tenant's authority and the application's client ID, and
**Google Workspace**'s **Accounts** tab, in its **Sign-in** step, fills in Google, the
Internal web client's ID and every verified domain of the organisation as the
**Domains** ([Google Workspace](/en/user/google-workspace/#the-accounts-tab)). It fills in
only a form that holds nothing yet, never over what is saved.

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
invitation pending: Staff announced it, and Vantigo recognises it by what the provider
sends — the employee number, the user principal name or the account's object id. You do not adopt it for Staff. An account nobody announced arrives as
before, and an Owner adopts it if it is Staff's
([the announced account](/en/admin/authentication/#the-announced-account)).
