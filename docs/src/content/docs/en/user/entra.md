---
title: Microsoft Entra ID
description: Connecting the company's Microsoft Entra ID tenant on the Microsoft Entra ID page — the Status card, the wizard's six steps and what Done means on each, the permissions to copy, the secret shown once, the connection test, the account rules and the Outlook connection switch with its status, counts and redirect URI, the warning for permissions Vantigo does not use, the Groups table confirming a group Vantigo did not create, connecting existing users by the directory picker and the review of exact matches — and who sees what.
sidebar:
  order: 47
sources:
  - apps/entra/frontend
  - apps/host/frontend/src/routes/entra
  - apps/host/frontend/src/routes/entra.tsx
---

The **Microsoft Entra ID** app connects Vantigo to your company's Microsoft Entra ID
tenant, so that the accounts Staff gives people are made there: each person's Microsoft
account is created ahead of their first day, enabled on it with a Temporary Access Pass,
kept up to date, and disabled when they leave. Staff decides who gets an account and
when ([Staff's Account tab](/en/user/staff/#account)); this app is where the connection
is set up and watched. The operator's procedure, with what to do in the Microsoft portal,
is in [the administration guide](/en/admin/entra/); the rules are in [the
reference](/en/reference/entra/).

## Who sees what

| What | Who |
| --- | --- |
| The **Microsoft Entra ID** tile, the page, the **Status** card, the accounts and the **Groups** table — **Confirm** on an account or a group Vantigo did not create — and the recent events | `entra:manage` |
| Steps **1 Register the application**, **2 Add a client secret** and **3 Grant the permissions and the role** — **Save**, **Add the secret**, **Revoke**, **Test connection** | an **Owner**, signed in with a second factor; to anyone else with `entra:manage` they are shown read-only, with *An Owner does this step. You can see what is set, but not change it.* |
| Step **4 Set the account rules** — **Save the account rules** | `entra:manage` |
| Steps **5 Sign-in** and **6 Provisioning** | an **Owner**: they are the **Single sign-on** card's forms; anyone else reads the step without them |
| **Search the directory** in the connect dialog and **Find matches in the directory…** on **Users** ([connect existing users](#connect-existing-users)) | an **Owner**, signed in with a second factor when Owners must use one |

`entra:manage` is in the category Administration and cannot be delegated: only an Owner
gives it to a role. The Owner holds it already. An Owner who signed in without a second
factor while Owners must use one sees the forms, but saving them is refused.

## The page

Open **Microsoft Entra ID** from the app switcher. The page is a wizard of six steps,
one card each, in the order you do them. Each card says where to go in the Microsoft
Entra admin center, what to copy, and carries a mark: *Done* once Vantigo has what the
step needs, *Not done* until then, and *Optional* on the last two steps — the mark comes
from what Vantigo has stored, so it is right however the step was done. Keep the portal
open in another tab and work down the page.

### The Status card

Once the connection has been tested, a **Status** card sits above the steps; before
that, and again after step 1 changes, it is not shown:

- **Connected** or **Failing**, and, when there is one, the reason in a sentence: a
  secret refused, the tenant not answering, no consent granted, the secrets expired, a
  secret expiring within 30 days, or the User Administrator role apparently missing;
- **Organisation**, as the tenant gives it; **Last checked**, when the connection was
  last tested; **Accounts**, how many Vantigo keeps in the tenant — enabled,
  disabled and deleted; and **Groups**, *… kept*, the groups present there;
- **Permissions**: *Every permission the module needs is granted.*, or each one still
  missing by name, and the ones granted. When the application holds permissions
  Vantigo does not use, an orange notice says *The application holds permissions
  Vantigo does not use:* with the list, and that they may be left from an earlier setup
  or wider than needed — remove them under **API permissions** unless another product
  uses the registration. It is a warning only: the connection stays **Connected**;
- **Client secrets**: each by its **Description**, with **Added**, **Expires**, **Last
  used** (when it last fetched a token) and its **State** — **In use** for the one
  tried first, *Revoked* and the date, and *Refused by the tenant* and the date for one
  the tenant turned down — and **Revoke** (an Owner);
- **The module's accounts**, once there are any: each **User principal name**, its
  **State** and what was **Reported** for it. An account Vantigo found in the tenant
  rather than created reads *Adopted — confirm* until someone confirms it
  ([below](#confirm-an-account-vantigo-did-not-create));
- **Recent events**: what Vantigo did in the tenant or to the connection, when, the
  user principal name, *by Staff's accounts run* when Staff's worker did it, and what
  was reported. Never a secret, a password or a pass. A group's event is worded too —
  *A group was created*, *A group was found and adopted*, *A group's members were
  reconciled*, *A group was emptied and kept*, *A group's adoption was confirmed* and the
  like — with what was reported in words, such as *3 members added* or *the team waits
  until the group is fifteen minutes old and Teams has seen it*.

### The Groups table

Under the **Status** card, shown with it, **Groups** lists the groups Vantigo keeps in
the tenant for Staff's groups ([Groups in Staff](/en/user/staff/#groups)), deleted and
kept ones included, greyed out: each group's **Name** as Vantigo last wrote it, with its
mail nickname under it; its **Type**, *Security* or *Microsoft 365*; **Team**, *Yes* once
it has become a team in Microsoft Teams, else *No*; its **State** — *Present*, *Deleted*,
or *Emptied and kept* for a security group Staff let go, which Vantigo never deletes —
with *Found in the tenant and adopted* under it for a group Vantigo found holding the
nickname rather than made, or *Adopted — confirm* and **Confirm** while nobody has
confirmed it ([below](#confirm-a-group-vantigo-did-not-create)); and **Last members
sync**, when Vantigo last brought its members in line, or *Never*. Before there is any
it says *No groups yet. A Staff group turned on for Microsoft Entra ID appears here once
the accounts worker has made it.* Apart from **Confirm** it is read-only: HR creates,
changes and archives groups in Staff, and Staff's accounts worker brings them here.

### 1 Register the application

Register an application for Vantigo in the portal (**App registrations** › **New
registration**, *Accounts in this organizational directory only*), then copy its
**Directory (tenant) ID** and **Application (client) ID** into the two fields and choose
**Save**. Both are GUIDs, as the registration's **Overview** shows them.

*Done* once both are saved. Saving another tenant or client later sends the connection
back to not connected until it is tested again. While Vantigo keeps accounts in the
tenant, it asks first — **Change the tenant or the application?** — and **Change it**
saves, since the accounts were made in the old one: Vantigo no longer manages them, and
no longer disables or deletes them when their people leave. A change of tenant also
discards every Temporary Access Pass not yet shown. Nothing is deleted in either
tenant.

### 2 Add a client secret

Make a client secret in the portal (**Certificates & secrets** › **New client
secret**) and copy its **Value** at once. Then fill in **Add a secret**: **Description**
to tell it apart (optional), **Expires**, the day the portal showed — in the future, at
most 24 months ahead; Vantigo takes the start of that day where you are — and **Value**,
and choose **Add the secret**. The value is a password field and is **never shown
again**, here or anywhere: Vantigo seals it as it arrives.

The list above the form shows each secret with its description and dates. The newest
one that has not expired or been revoked is tried first and marked **In use**; one the
tenant refused is tried after the others. To rotate, add the new secret first, then
choose **Revoke** on the old one and confirm; Vantigo refuses to revoke the only
working secret while it keeps accounts, because nothing could then disable a leaver.
Revoking only stops Vantigo using it: delete it in the portal yourself.

*Done* once a secret is in use.

### 3 Grant the permissions and the role

The card lists the eight Microsoft Graph **application** permissions to add under **API
permissions** — nine while the [Outlook connection](#outlook-connection) is on, the
ninth, `Calendars.ReadWrite`, marked *(for calendar sync)* — each ready to copy, then **Grant admin consent**. It also says the step
no other product asks for: assign the **User Administrator** role to the application
under **Roles & admins** — without it, Microsoft refuses to enable, disable or delete
an account and to issue a Temporary Access Pass, even with every permission.

Choose **Test connection**. Vantigo fetches a token with the secret, reads the
organisation and its verified domains, and compares the token's permissions with the
eight. The card then says *Connected to* the organisation and lists its verified
domains, or *The connection failed* and why; it names each permission still missing,
and marks each in the list granted or missing. The role cannot be checked from here —
check it in the portal; if it is missing, the first account Vantigo tries to enable is
refused and reports it, and after three such refusals in a row the **Status** card
says the role is most likely missing.

*Done* once the last test connected with no permission missing.

### 4 Set the account rules

The rules every account follows, which anyone with `entra:manage` may change:

- the **Domain for user principal names**, chosen from the verified domains the test
  read (typed before the first test) — a person whose work email is on one of them
  keeps it as their user principal name;
- the **Name pattern** for everyone else: `given.family`, `g.family` or `given`, with
  Norwegian letters folded (`Ørjan Åsmundsen` becomes `orjan.asmundsen`) and a number
  added when the name is taken;
- the **Default usage location**, the country written when Staff has none for the
  person — Microsoft gives no licence without one;
- **When someone leaves** — the account is always disabled first — **Sign them out
  everywhere**, **Write the leave date**, **Remove directly assigned licences**, and
  **Remove from groups**: every group they can be removed from, only the groups Vantigo
  manages, or no group;
- **When someone starts**: **Write the hire date**, the **Temporary Access Pass
  lifetime (minutes)** (10 to 43,200), whether **The pass can be used once only**, and
  **Who hands over the pass**. The card says, with the path, that the tenant's
  authentication methods policy must enable the pass for the users, or Microsoft
  refuses it;
- **Outlook connection**: **Outlook connection: let people connect their Outlook to
  Vantigo** ([below](#outlook-connection)).

Choose **Save the account rules**. A value outside its rule is marked on its field with
the reason. If someone else saved the connection since you opened the page, the page
says so and reloads it: check what is there now and make your change again.

Once the connection is connected, the card also lists **The tenant's licences**, each
with its seats assigned and bought — what a Staff group's licences can name. Reading
the list here also refreshes the choices a Staff group's **Licences** offer, as each
connection test does.

*Done* once a domain is saved.

### 5 Sign-in

Optional: lets people sign in to Vantigo with their Microsoft account. The card is the
**Single sign-on** card's provider form — for an Owner, once step 1 is saved (*Register
the application in step 1 first.* until then) — filled in with your tenant's address
and the client id from step 1 when it holds nothing yet, with the **Callback URL** to
paste into the registration's **Authentication** › **Web** platform. Complete it and
choose **Save**. Without it, Staff still makes the Microsoft accounts, but nobody signs
in to Vantigo with them.

### 6 Provisioning

Optional: has Entra create each person's Vantigo user as soon as their account is
enabled. The card is, for an Owner, the **Single sign-on** card's SCIM block: **New token**, the
**Endpoint URL**, and where to paste both in the portal (**Enterprise applications** ›
the app › **Provisioning**). Without it, a person's Vantigo user appears at their
first sign-in instead.

The card also offers **Open Staff's settings**, where **Default account kind** can be set to
**Microsoft Entra ID** for the people registered from now on
([Accounts](/en/user/staff/#accounts)). People already registered keep their kind until
HR changes it on their Account tab.

## Outlook connection

The **Outlook connection** lets each person connect their own Outlook to Vantigo under
**Settings → Connections**, signing in to Microsoft once ([Connections](/en/user/connections/)).
It is off until someone with `entra:manage` turns it on. The same switch lets people
[sync their calendar to Outlook](/en/user/calendar/#sync-with-outlook), which in this
release still uses the application permission rather than each person's connection.

1. An Owner prepares the app registration first — the **Redirect URI** shown under the
   switch added to its **Web** platform, and the delegated permissions `openid`,
   `profile`, `offline_access` and `Calendars.ReadWrite` with admin consent. The
   [administration guide](/en/admin/entra/#outlook-connection) has the steps.
2. In step **4 Set the account rules**, under **Outlook connection**, turn on **Outlook
   connection: let people connect their Outlook to Vantigo**, and choose **Save the
   account rules**.
3. Connect your own Outlook under **Settings → Connections** to check that it works.

Calendar sync still needs the `Calendars.ReadWrite` **application** permission too:
step **3 Grant the permissions and the role** lists nine permissions while the switch is
on, the last marked *(for calendar sync)*, and the **Status** card lists it as missing
until a test sees it — step 3 does not wait for it, since the permission is advisory. If
it was granted with Exchange RBAC for Applications instead, the token never shows it:
then it stays listed, and that is expected.

Under the switch, the card shows the **Redirect URI** to add to the registration, with a
copy button, and a line says where the connection stands:

- **Ready** — someone has connected since the switch was turned on;
- **Nobody has connected yet: connect your own Outlook under Settings › Connections to
  check that it works.**;
- **The last connect lacked a delegated permission: grant admin consent for it in the
  app registration and connect again.** — a person's connect came back without the
  permission it asked for;
- **Waiting for the connection** — the connection is not connected, with the reason under
  it.

Under it, *People connected: … Waiting to reconnect: …* counts the people whose
connection works and those whose connection Microsoft refused, who see **Reconnect
Outlook** on their own page.

Under that, while the switch is on, **Change notifications** says whether Microsoft tells
Vantigo of changes made in Outlook, so they are taken within about a minute instead of
at the next sync, every 15 minutes:

- **On, watching 3 mailboxes. Last notification: …** — it works; the time is the last
  notification received, or *none yet*;
- **Turned off by the operator …** or **Needs PUBLIC_BASE_URL …** — the operator turned
  it off, or the installation has no public address; changes in Outlook arrive
  at the next sync;
- **Microsoft could not reach the address since …** — Vantigo tries again within the
  hour; changes arrive at the next sync meanwhile. The [administration
  guide](/en/admin/entra/#change-notifications) has what to check.

Nothing here needs choosing: the lines only report.

**Turning it off** closes every person's connection when you save — while anyone is
connected or waiting to reconnect, the page first asks *Turn the Outlook connection
off?*: *Everyone will have to connect Outlook again.*, and **Turn off and save** saves
it. It also stops every person's calendar sync and removes the copies Vantigo put in
their Outlook — the calendar permission is not asked for while it is off. Turning it on
again resumes everyone who had their sync on; each person connects their Outlook again.

## Confirm an account Vantigo did not create

When the tenant already holds a user with the employee number of a person Staff
registers, Vantigo links it but writes nothing to it — no details, no enable, no pass,
no disable, no delete — until someone with `entra:manage` confirms it is the person's.
Staff's Account tab and dashboard say so, and the dashboard links here.

1. In the **Status** card's accounts, find the one reading *Adopted — confirm*.
2. Check in the portal that it is the person's.
3. Choose **Confirm** beside it, then **Confirm** in *Confirm the adoption of …?*.

The page says *Adoption confirmed*, and Vantigo manages the account from Staff's next
accounts run. Confirm only an account you have checked: confirming hands it to whoever
holds that employee number in Staff.

## Confirm a group Vantigo did not create

When the tenant already holds a group with the mail nickname of a Staff group turned on
for Microsoft Entra ID, Vantigo links it but writes nothing to it — no name, no owner, no
member, no delete — until someone with `entra:manage` confirms it is that group. The
Staff group's page says *Found in Microsoft Entra ID and awaiting confirmation there*
meanwhile.

1. In the **Groups** table, find the group reading *Adopted — confirm*.
2. Check in the portal that it is the group meant — its name, its owners, its members.
3. Choose **Confirm** beside it, then **Confirm** in *Confirm the adoption of …?*, which
   says what follows: *Confirming lets Vantigo manage it for Staff. Its first write makes
   the group's owners exactly Staff's owners and the application — the owners it has now
   are removed — and writes Staff's name, description and visibility to it; Vantigo then
   makes its members Staff's, removing everyone else. Vantigo never deletes a group it did
   not create.*

The page says *Adoption confirmed* — *Vantigo manages the group from Staff's next
accounts run.* Confirm only a group you have checked.

## Connect existing users

When single sign-on is turned on in a workspace that already has users, each of them
must be connected to their directory account before they can sign in through
Microsoft ([Connect an account to single sign-on](/en/user/workspace-administration/#connect-an-account-to-single-sign-on)).
While this app is enabled, you need not type the accounts: Vantigo looks them up in the
tenant. Both ways below are an **Owner**'s, on **Workspace admin** › **Users**, signed in
with a second factor when Owners must use one; they need the connection to the tenant
from steps 1 to 3, and the **Single sign-on** column, which is there while single
sign-on is on.

### Review the exact matches

1. In **Users**, choose **Find matches in the directory…**, next to the search.
2. **Users who match an account in the directory** opens. A line at the top counts the
   *Users not yet connected*, the ones *Matched exactly*, and the ones *Left out because
   more than one account matched*. Below it each proposal is a row: the **User**, with
   their address; the **Account in the directory**, with its display name, principal
   name, employee ID when it has one, and *Disabled* when the account is; and
   **Matched by**: **Mail**, **Principal name**, **Employee ID**, or several.
3. Check the rows. A row starts ticked only when the account is enabled, the
   principal name matched, and the user is not an Owner. Any other row starts
   unticked and is marked **Check this match**, with the reason: *Matched by mail or
   employee ID only, not by the principal name the person signs in with. Make sure it
   is the same person.*, or *This user is an Owner. Whoever holds this account will
   sign in with the Owner's access.* Tick those you have checked, and clear the ones
   you do not want.
4. Choose **Connect selected**, which shows how many are checked. Vantigo connects the
   pairs one at a time, and each row's **Result** says *Connected*, or why it was
   refused in words — for instance that another user is already connected to the
   account. **Users** is refreshed when it is done.

A user is proposed only when the match is **exact**: their address is the account's
mail or principal name, ignoring upper and lower case, or their employee number is
the account's employee ID — and every one of those that matched points to the same
account. There is no guessing on names or similar addresses. Left out are:

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
  — you connect yourself by signing in through Microsoft.

When no one matches, the window says so and that you connect them one at a time from
the **Single sign-on** column. Only the first 1,000 users not yet connected are
compared; when there are more, it says so — connect these, then look again. Finding
the matches can take a while in a large tenant; opening the window again meanwhile
waits for the same answer, and after two minutes it gives up and says the tenant did
not answer.

A connected row reads **Awaiting first sign-in** in **Users** until the person next
signs in through Microsoft, or the provider pushes their account; then the connection
completes and it reads **Connected**. Their name, address and roles stay as they were.

### Pick the account when connecting one user

**Connect…** in a user's row opens **Connect _name_ to single sign-on**, which, while
this app is enabled, starts with **Search the directory**. Type two characters or more
of the person's name, address or principal name; after a pause the list shows the
matching accounts, by display name, each with its principal name, its employee ID,
*Guest* for a guest of the tenant, and *Disabled* when the account is. An account somebody already holds reads *Awaiting*
and the user's name (or *Awaiting another user*), *Provisioned to another user* or
*Announced by Staff*, and cannot be picked. At most 25 accounts are shown; when there
are that many, it asks you to narrow the search.

Picking an account fills in **User principal name** and, under **More**, **Object id**.
The two belong together: change the principal name and the object id is cleared;
search again after a pick and both are cleared. Check them and choose **Connect**, as
before. The typed fields stay: you can still
type the name yourself, and they are all there is while the directory cannot be read.

A search or a review can be refused when the tenant cannot be asked: the secrets are
refused, the tenant does not answer, or it asks Vantigo to wait — try again in a
minute. The review is refused when single sign-on is not configured.

## After the setup

Nothing more happens here day to day: Staff's accounts worker does the work, and each
person's account shows on their **Account** tab in Staff, where HR shows the
Temporary Access Pass once. Come back to the page when Staff's dashboard says a secret
is about to expire or the connection is failing, and look at the **Status** card and
the recent events.
