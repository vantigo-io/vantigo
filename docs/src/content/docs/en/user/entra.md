---
title: Microsoft Entra ID
description: Connecting the company's Microsoft Entra ID tenant on the Microsoft Entra ID page — the Status card, the wizard's six steps and what Done means on each, the permissions to copy, the secret shown once, the connection test, the account rules, the Groups table and confirming a group Vantigo did not create — and who sees what.
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
  missing by name, and the ones granted;
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
permissions**, each ready to copy, then **Grant admin consent**. It also says the step
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
  refuses it.

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
   says what follows.

The page says *Adoption confirmed* — *Vantigo manages the group from Staff's next
accounts run.* Vantigo then writes Staff's name and description to the group, makes its
owners Staff's and its members Staff's, removing everyone else. It never deletes a
group it did not create. Confirm only a group you have checked.

## After the setup

Nothing more happens here day to day: Staff's accounts worker does the work, and each
person's account shows on their **Account** tab in Staff, where HR shows the
Temporary Access Pass once. Come back to the page when Staff's dashboard says a secret
is about to expire or the connection is failing, and look at the **Status** card and
the recent events.
