---
title: Google Workspace
description: Connecting the company's Google Workspace organisation on the Google Workspace page under Integrations in Workspace admin — its four tabs, Status, Connection, Accounts and Calendar and Gmail, where the page is, the six steps on Connection and what Done means on each, the key shown never again, the client ID and the scopes to copy, the admin to impersonate, Test connection, the Internal web client and its redirect URIs, the Status tab with its scopes and events, the warning for scopes Vantigo does not use, the Accounts tab with the account rules, the licences, the accounts and the groups Vantigo keeps and Confirm for those it did not create — and who sees what.
sidebar:
  order: 48
sources:
  - apps/google/frontend
  - apps/host/frontend/src/routes/workspace/google
  - apps/host/frontend/src/routes/workspace/google.tsx
---

The **Google Workspace** page, under **Integrations** in Workspace admin, connects
Vantigo to your company's Google Workspace organisation through a service account with
domain-wide delegation. Through it Staff keeps your people's Google accounts and its
groups in the organisation, by the account rules on the **Accounts** tab; each person's
own Google connection, calendar sync and Gmail come later, on the same connection. The operator's procedure, with what to do in the Google consoles, is in
[the administration guide](/en/admin/google-workspace/); the rules are in [the
reference](/en/reference/google/).

## Who sees what

| What | Who |
| --- | --- |
| **Google Workspace** under **Workspace admin** › **Integrations**, the **Status** tab with its scopes and **Recent events**, and every step read-only | `google:manage` |
| The **Accounts** tab: saving the account rules, reading the licences, and **Confirm** for an account or a group Vantigo did not create | `google:manage` |
| **Upload key**, **Revoke**, saving the admin to impersonate, **Test connection** and saving the web client | an **Owner**, signed in with a second factor when Owners must use one; to anyone else with `google:manage` the steps are shown with *An Owner does this step. You can see what is set, but not change it.* |

`google:manage` (*Manage Google Workspace*) is in the category Administration and cannot
be delegated: only an Owner gives it to a role. The Owner holds it already. A holder who
is not an Owner sees only the **Integrations** section of Workspace admin
([Workspace administration](/en/user/workspace-administration/#integrations)), and
finds **Google Workspace** in the avatar menu too.

## Where it is, and the four tabs

Open the avatar menu at the top right, choose **Workspace admin**, and in the sidebar
choose **Google Workspace** under **Integrations**. The page is not in the app switcher;
its address is `/workspace/google`. It is there only while the Google Workspace module is
on in this installation.

The page has four tabs, each with an address of its own, so a tab can be linked to:

| Tab | What it holds |
| --- | --- |
| **Status** | The **Status** card — the status, the organisation, the scopes — and **Recent events** |
| **Connection** | The six steps that set up the connection |
| **Accounts** | The account rules, the organisation's licences, and the accounts and groups Vantigo keeps for Staff |
| **Calendar and Gmail** | *Calendar and Gmail are coming*: each person's own Google connection, calendar sync, Gmail mail channels and sending from a person's Gmail address will be set up here |

While nothing is set up and no key is uploaded, the page opens on **Connection**. From
then on it opens on **Status**.

## Connect the organisation

The **Connection** tab is six cards, one per step, in the order you do them. A card says
where to go in Google's consoles and what to copy. Four of them carry a mark — *Done*
once Vantigo has what the step needs, *Not done* until then; steps 1 and 3 happen at
Google and have no mark of their own: the test is their proof.

1. **Create the project, the service account and its key** — in the Google Cloud
   console, under **IAM & Admin** › **Service accounts**. The card warns that a new
   organisation's policy may forbid service account keys, and links to **Read the guide
   to connecting Google Workspace**.
2. **Upload the key** — paste **The key file's contents** or **Choose the key file**,
   then **Upload key**. The file is read in your browser; Vantigo seals the private key
   and never shows it again. The keys are listed under **Service account keys** with the
   **Service account**, the **Client ID**, the **Key ID**, when it was **Added** and its
   **State**: **In use** for the one Vantigo uses, *Google refused it* and the date for
   one Google refused, *Revoked* and the date. **Revoke** stops Vantigo using a key at
   once; it changes nothing at Google, so delete the key in the Google Cloud console as
   well. *Done* once a key is uploaded.
3. **Delegate the scopes** — in the Admin console, under **Security** › **Access and
   data control** › **API controls** › **Domain-wide delegation**. The card shows the
   service account's **Client ID** (once a key is uploaded) and the **OAuth scopes
   (comma-separated)**, each with **Copy**. Delegation can take up to 24 hours to apply,
   and with multi-party approval a second super admin must approve it.
4. **Name the admin to impersonate** — the **Admin's address**: a super admin, or an
   admin whose role may manage users and groups. Vantigo reads the organisation as this
   admin. **Save**; *Done* once an admin is named. Saving another admin sets the status back
   to **Not set up**, and forgets the scopes the last test found, until the next test.
5. **Test the connection** — **Test connection** asks Google for a token for each scope
   as the admin, then reads the organisation and its domains, and shows the **Test
   result**: the status, the **Organisation**, the **Customer ID**, the **Primary
   domain** and the **Verified domains**. It needs a key and an admin first. *Done* once
   the last test found the connection **Connected** with every scope granted.
6. **Set up the Internal web client** — in the Google Cloud console, under **Google Auth
   Platform** › **Clients**: an OAuth client of the type Web application, its consent
   screen's audience *Internal*, and both **Authorised redirect URIs** the card lists,
   each with **Copy**. Enter the **Client ID** and the **Client secret** and **Save**.
   Google shows a client secret only once, when it is created; Vantigo seals it and never
   shows it again. Leave the secret empty to keep the one there; a new client needs its
   secret. *Done* once a client and its secret are saved. Nothing uses the web client
   yet: each person's Google connection and the sign-in with Google will.

If somebody else saved the connection while you had it open, Vantigo says so, reloads
it, and you make your change again.

## The Status tab

The **Status** card shows:

- the status: **Not set up** (no key, or no admin), **Waiting for delegation** (Google
  has not granted the scopes yet), **Connected** or **Failing**, and the reason in a
  sentence when there is one — Google refused every key or does not know the admin, the
  admin may not read the organisation, or Google did not answer;
- **Organisation**, **Customer ID**, **Primary domain**, **Verified domains** and **Last
  tested**, as the last test read them;
- **Scopes**: *Every scope Vantigo needs is granted.*, or *Scopes Vantigo needs that
  Google refused* with the list, and the **Granted scopes**. Before the first test it
  says the scopes are checked when the connection is tested;
- **Scopes Vantigo does not use**, an orange notice when the delegation carries scopes
  Vantigo never asks for. Delegation reaches every account in the organisation, so
  remove the ones nobody needs in the Admin console. It is a warning only: the connection
  stays **Connected**.

While nothing is set up a notice, *Not connected yet*, points to the **Connection** tab.

**Recent events** lists what was done, newest first, and by whom — *by Vantigo* for what
Vantigo did itself: the admin changed, the connection tested, the web client changed, a
key uploaded, a key or a client secret revoked, and Google refusing a key.

## The Accounts tab

Staff decides who has a Google account and when: HR gives a person the **Google
Workspace** account kind in Staff ([Staff](/en/user/staff/#a-google-workspace-account)).
This tab sets how Vantigo makes and ends those accounts, and shows what it keeps. It has
four cards.

**Account rules.** Choose, then **Save the account rules**:

- **Address domain** — the verified domain new addresses are made at; empty for the
  primary domain.
- **Address pattern** — how an address is made from the names when the person's work
  email cannot be used: *kari.nordmann*, *k.nordmann* or *kari*.
- **Organisational unit** — where new accounts are made, such as `/Ansatte`; `/` for the
  top.
- **The first password's lifetime, in days** — 1 to 30: how long the password the person
  gets on their first day can be shown before it is forgotten; and **Who hands over the
  first password** — kept for a later release; HR shows it on the person's Account tab in
  Staff.
- **When someone leaves** — **Remove the licences**, **Take them out of groups**
  (*Every group*, *The groups Vantigo made* or *No group*) and **Sign them out of every
  session**. The account is always suspended first.

If somebody else saved the connection while you had the tab open, the tab says so; reload
it and make your change again.

**Licences.** The licences the organisation holds, each with its name and how many hold
it, as Vantigo last read them — the choices of a Staff group's **Licences**. **Read the
licences** asks Google again. When Google cannot be asked, the card says why: no key
fetched a token for the licences (check the delegation and test the connection), or
Google did not answer.

**Accounts.** Every Google account Vantigo keeps for a person: the address, the employee
number, the state (*Active*, *Suspended* or *Deleted*), the licences Vantigo assigned,
whether a first password waits to be shown, and what Google could not do, in a sentence
each. An account Vantigo found in Google rather than created is marked *Found — confirm* and
has **Confirm**: Vantigo
writes nothing to it until you confirm it is the person's
([why](/en/admin/google-workspace/#accounts-and-groups-vantigo-did-not-create)).

**Groups.** Every Google group Vantigo keeps for a Staff group: its address, the Staff
group, and whether Vantigo made it (*Created by Vantigo*, *Found and adopted*). A group
Vantigo found at the address rather than made is marked *Found — confirm* and has
**Confirm**, for the same reason. Once confirmed, Vantigo reconciles it with Staff's
group: its name, description and visibility are overwritten with Staff's, owners Staff
does not name are made members, and members Staff's group does not list are removed. It
never deletes it. The confirm dialog says so too.
