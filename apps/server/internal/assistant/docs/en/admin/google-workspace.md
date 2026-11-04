---
title: Google Workspace
description: Connecting Vantigo to a Google Workspace organisation through a service account with domain-wide delegation — the Google Cloud project and the Admin SDK API, the service account and its JSON key, lifting iam.disableServiceAccountKeyCreation for the project, uploading the key, delegating the scopes in the Admin console and waiting for them, the admin to impersonate, the Internal web client and its two redirect URIs, verifying with Test connection — then the accounts Staff keeps in Google, the account rules (the address, the organisational unit, the first password and its lifetime), licences, Staff's groups and their three settings, photos at 96 px, what a leaver's steps do, delete after N days, confirming an account or a group Vantigo did not create, rotating a key, changing the service account, and what can go wrong.
sidebar:
  order: 47
sources:
  - apps/server/internal/google/module.go
  - apps/server/internal/google/client.go
  - apps/server/internal/google/token.go
  - apps/server/internal/google/credentials.go
  - apps/server/internal/google/connection.go
  - apps/server/internal/google/test.go
  - apps/server/internal/google/status.go
  - apps/server/internal/google/provisioner.go
  - apps/server/internal/google/directory.go
  - apps/server/internal/google/lifecycle_prepare.go
  - apps/server/internal/google/lifecycle_activate.go
  - apps/server/internal/google/lifecycle_deactivate.go
  - apps/server/internal/google/licences.go
  - apps/server/internal/google/groups.go
  - apps/server/internal/google/groups_traits.go
  - apps/server/internal/google/photo.go
  - apps/server/internal/google/accounts.go
---

The Google Workspace module connects Vantigo to your company's Google Workspace
organisation. It signs in as a **service account** with **domain-wide delegation**:
Google lets the service account act as an admin of the organisation, for the scopes a
super admin delegates to it, and nothing else. On that connection Staff keeps your
people's Google accounts and its groups in the organisation: it creates a person's
account before their first day, opens it on the day with a first password HR hands
over, keeps their profile and manager in step, assigns the licences of their groups,
writes their photo, and suspends the account when they leave
([The accounts](#the-accounts)). Each person's own Google connection, calendar sync and
Gmail come in later releases, on the same connection.

This page is the operator's side: what Google needs, the steps in the Google Cloud
console and the Admin console, and what to do when something fails. The page in Vantigo
is **Google Workspace** under **Integrations** in Workspace admin; its screens are in
[the user guide](/en/user/google-workspace/), and the rules the module checks are in
[the reference](/en/reference/google/).

## Before you start

- **A Google Workspace organisation**, and a **super admin** in it: domain-wide
  delegation is granted in the Admin console, and only a super admin can grant it.
- **A Google Cloud project under the organisation**, or the right to create one, and
  someone who may create service accounts and keys in it. If the organisation forbids
  service account keys, someone with **Organization Policy Administrator** on the
  organisation ([below](#create-the-service-account-and-its-key)).
- **An Owner** in Vantigo, signed in with a second factor while `OWNERS_REQUIRE_MFA` is
  on. The key, the admin to impersonate, the web client and the test are an Owner's
  alone; a person with `google:manage` sees them read-only.
- **Staff on.** `MODULES` refuses `google` without `staff` at startup
  (`google requires staff`): the accounts Vantigo will keep in Google are Staff's
  decision, as they are for Microsoft Entra ID.
- **Outbound https** from every process that runs the API or the workers to Google's
  hosts — `oauth2.googleapis.com`, `admin.googleapis.com`, `licensing.googleapis.com`,
  `www.googleapis.com`, `gmail.googleapis.com`, `groupssettings.googleapis.com` and
  `accounts.google.com` — without a proxy: the module uses none and follows no
  redirect. This release calls the first four.
- The migrations create the `google` schema whether or not the module is enabled; a
  least-privilege application role needs it granted like every other
  ([Installation](/en/admin/installation/#database-roles)).

Nothing about the module is in `vantigo.env`: there is no `GOOGLE_*` variable. The key,
the admin and the web client are in the database, set on the module's page — a workspace
setting, not an instance one ([Configuration](/en/admin/configuration/)).

## Enable the module

1. Leave `MODULES` unset, or add `google` beside `staff`.
2. Restart Vantigo (`docker compose up -d`).
3. Verify: **Google Workspace** is in the sidebar of **Workspace admin**, under
   **Integrations**, for an Owner (the module has no tile in the app switcher), and
   `GET /api/v1/google/connection` answers 401 without a session rather than the 404 a
   disabled module's path answers.

Open the avatar menu, choose **Workspace admin**, and under **Integrations** choose
**Google Workspace** (`/workspace/google`). The **Connection** tab holds six steps, each
a card in the order below; until a key is uploaded the page opens on it. Keep the Google
consoles open in another tab.

## Create the project

1. In the **Google Cloud console**, create a project under your organisation (for
   example `vantigo`), or choose one you keep for integrations. It must be under the
   organisation for its consent screen to be *Internal*.
2. In the project, open **APIs & Services** › **Library**, find **Admin SDK API** and
   choose **Enable**. Without it Google refuses the test's reads of the organisation.

## Create the service account and its key

1. In the project, open **IAM & Admin** › **Service accounts** › **Create service
   account**. Name it (for example `vantigo-workspace`) and choose **Done**. It needs no
   role in the project: what it may do in Workspace is the delegation, not IAM.
2. Open the service account, then **Keys** › **Add key** › **Create new key**, choose
   **JSON** and **Create**. The browser downloads the key file once; Google keeps no
   copy you can download again. Keep it until it is uploaded to Vantigo, then delete it.

**If Google will not create the key.** Organisations created since 2024 may enforce the
policy `iam.disableServiceAccountKeyCreation`, which forbids every service account key,
and the console then says key creation is disabled. Lift it for this project only:

1. Someone with **Organization Policy Administrator** on the organisation opens the
   project, then **IAM & Admin** › **Organization policies**, and finds **Disable service
   account key creation** (`iam.disableServiceAccountKeyCreation`).
2. **Manage policy** › **Override parent's policy**, add a rule with **Enforcement**
   *Off* (*Not enforced*), and **Set policy**. The rest of the organisation keeps the
   policy.
3. Create the key as above. The policy can be set back afterwards: it stops new keys,
   not the ones already made — but a [rotation](#rotate-a-key) needs it lifted again.

Whether every new organisation enforces the policy is not yet confirmed; the step is
needed only when the console refuses.

## Upload the key

1. In step **2 Upload the key**, paste the key file's contents into **The key file's
   contents**, or choose **Choose the key file**. The file is read in your browser.
2. Choose **Upload key**.

Vantigo checks the file — at most 8 KiB, of type `service_account`, its `token_uri`
`https://oauth2.googleapis.com/token`, its private key an RSA key in PKCS #8 PEM — seals
the private key at once and never shows it again. It keeps the service account's
address, its numeric **Client ID**, the **Key ID** and the project, and lists the key
under **Service account keys** as **In use**. Delete the downloaded file now.

## Delegate the scopes

1. Step **3 Delegate the scopes** shows the service account's numeric **Client ID** and
   the **OAuth scopes (comma-separated)**; copy both. This release needs six:
   - `https://www.googleapis.com/auth/admin.directory.customer.readonly` — the
     organisation;
   - `https://www.googleapis.com/auth/admin.directory.domain.readonly` — its domains;
   - `https://www.googleapis.com/auth/admin.directory.user` — the accounts, their
     photos, and signing a leaver out;
   - `https://www.googleapis.com/auth/admin.directory.group` — Staff's groups and their
     members;
   - `https://www.googleapis.com/auth/apps.groups.settings` — who sees a group;
   - `https://www.googleapis.com/auth/apps.licensing` — the licences.
2. As a super admin, in the **Google Admin console**, open **Security** › **Access and
   data control** › **API controls** › **Manage domain-wide delegation**, and choose
   **Add new**.
3. Paste the **Client ID** and the scopes, and choose **Authorise**.

Delegate only the scopes the page lists. Delegation reaches every account in the
organisation; a later release lists more scopes on the page when it needs them, and you
then edit the same client's entry.

**It can take up to 24 hours** before Google applies a new delegation; it is usually
minutes. If the organisation uses **multi-party approval** for sensitive admin actions,
the change waits until a second super admin approves it.

## Name the admin to impersonate

Domain-wide delegation acts *as* someone: every call the module makes to the Admin SDK
is made as this admin, through the delegation.

1. In step **4 Name the admin to impersonate**, enter the **Admin's address**: a super
   admin, or an admin whose role may read the organisation's users and domains (and,
   for the accounts to come, manage users and groups).
2. Choose **Save**.

Saving another address sends the connection back to *Not set up* and forgets the
scopes the last test found, until it is tested again.

## Verify

1. In step **5 Test the connection**, choose **Test connection**. Vantigo asks Google
   for a fresh token for each scope as the admin, then reads the organisation and its
   verified domains.
2. Expect, under **Test result**: **Connected**, the **Organisation**, the **Customer
   ID** (`C0…`), the **Primary domain** and the **Verified domains**. The **Status** tab
   then shows the same, *Every scope Vantigo needs is granted.*, and the step carries
   *Done*.

**Waiting for delegation** means Google refused every scope: the delegation is not
entered, or not applied yet. Wait, and test again. **Connected** with *Scopes Vantigo
needs that Google refused* lists the ones to add to the delegation. Staff keeps no
account in Google until the last test was granted the four scopes of the accounts, the
groups, the group settings and the licences: until then Staff's **Accounts** card reads
Google Workspace as failing (`scopes_missing`). An installation upgraded from the release
before needs the four added to the delegation and a new test.

**Scopes Vantigo does not use.** The test also asks for scopes Vantigo never uses but a
delegation copied from another product often carries — `https://mail.google.com/`,
`gmail.readonly`, `gmail.modify`, `gmail.send`, `calendar`, `calendar.events`,
`admin.directory.user.security`, `drive` and `cloud-platform` (each under
`https://www.googleapis.com/auth/` but the first). Any Google grants is listed in an
orange notice on the **Status** tab. The connection stays **Connected**; remove those
scopes from the client's delegation in the Admin console unless another product uses
the same service account.

## Set up the Internal web client

Each person's own Google connection and the sign-in with Google, in later releases, use
an OAuth web client in the same project. Enter it now, so the project is set up in one
sitting; nothing uses it yet.

1. In the project, open **Google Auth Platform** › **Branding** (the OAuth consent
   screen) and set it up with the **Audience** *Internal*, so only accounts in your
   organisation can use it.
2. Open **Clients** › **Create client**, choose **Web application**, and add both
   **Authorised redirect URIs** that step **6 Set up the Internal web client** lists:
   - `APP_URL` + `APP_BASE_PATH` + `/api/v1/google/connect/callback`
   - `APP_URL` + `APP_BASE_PATH` + `/api/v1/identity/oidc/callback`
3. Choose **Create**, and copy the **Client ID** and the **Client secret** at once:
   Google shows a client secret only now. If it is lost, add a new secret to the client.
4. In step 6, enter the **Client ID** (it ends with `.apps.googleusercontent.com`) and
   the **Client secret**, and choose **Save**. The secret is sealed and never shown
   again.

To rotate the secret, add a new one to the client in the console, enter it in step 6
with the same client id, and **Save**: the older one is revoked in Vantigo. Then delete
the old one in the console. A new client id always needs its secret.

## The accounts

People reach Google through Staff. HR gives a person the **Google Workspace** account
kind on their Account tab in Staff, or makes it the default for new people
([Staff](/en/admin/staff/#the-account-kind)); Staff's accounts worker then drives the
person's account in Google, through this module, as it drives Microsoft Entra ID's:

1. **Before the first day** (Staff's *create before* days), the account is found or
   created **suspended**: Google holds it, and nobody can sign in. Vantigo finds a
   person's account by their **employee number**, kept as the account's external ID of
   the type *Organization* — the same field to fill in for existing accounts, under
   **User information** › **Employee information** › **Employee ID** in the Admin console.
2. **On the first day** the suspension is lifted, Google gets a first password, and the
   licences of the person's groups are assigned.
3. **While they work**, a change to their name, job title, department, cost centre,
   phones, office location, manager or language reaches Google at the next run.
4. **The day after they leave**, the account is suspended and the leaver's steps run.
5. **After Staff's delete days**, the account is deleted.

The module runs nothing of its own: Staff's worker reads each account from Google at
every run, which is how a suspension or a delete made in the Admin console is seen.

### Set the account rules

On the **Accounts** tab of the Google Workspace page, a holder of `google:manage` sets
the **Account rules**, then **Save the account rules**:

- **Address domain** — one of the organisation's verified domains, which the test read;
  empty for the primary domain.
- **Address pattern** — for a person whose work email cannot be their address:
  `given.family`, `g.family` or `given`. A work email on a verified domain is used as it
  is. Letters are folded (`ø` → `o`, `æ` → `ae`, `å` → `a`, accents dropped); a taken
  address gets 2, 3, … 99 after it. A work email another account already holds is not
  suffixed: the person's account is held up, *address taken*. The address is the
  account's **primary email** in Google, and Vantigo never renames it.
- **Organisational unit** — where new accounts are made, `/` for the top, such as
  `/Ansatte`. The unit must exist in the Admin console; an account the module found
  stays where it is.
- **First password** — its lifetime in days (1 to 30, seven unless changed) and who
  hands it over. HR shows it once on the person's Account tab in Staff; the hand-over
  setting is kept for a later release and does not yet send the manager anything.
- **The leaver's steps** — remove the licences, which groups to leave (*all*, *the groups
  Vantigo made* or *none*), and sign out of every session; each is on unless turned off
  ([what they do](#what-a-leavers-steps-do)).

The **Accounts** tab also lists the accounts and the groups Vantigo keeps, and the
organisation's **Licences**.

### The first password

Google has no Temporary Access Pass. When the suspension is lifted on the first day,
Vantigo sets a fresh password of 16 characters — without the letters and digits easily
mistaken for each other — which the person **must change** at their first sign-in. It is
kept sealed until HR shows it, **once**, on the person's Account tab in Staff, and is
forgotten when its lifetime ends. HR's **Run now** sets a new one, but only while nobody
has signed in to the account yet: Vantigo never sets a password over an account someone
uses. If the organisation's password policy refuses it, the account is opened without
one and the person's Account tab says so (`pass_not_allowed`): reset the password in
the Admin console, or relax the policy for the organisational unit.

### Licences

A licence is a Google Workspace SKU, written `productId/skuId` (such as
`Google-Apps/1010020028`). On the **Accounts** tab, **Read the licences** asks Google
which licences the organisation holds — for Google Workspace, Cloud Identity, Google
Voice and archived-user licences, those at least one user holds — and keeps them: they
are the choices of a Staff group's **Licences**. A person is assigned every licence their
groups name on their first day, and later when their groups change; one no group names
any more is removed — but only one Vantigo assigned itself. A licence an administrator
assigned in the Admin console is left. When none is left to assign, the person's
Account tab says so (`licence_unavailable`, with the SKU), and Staff tries again at the
next run.

### Staff's groups in Google Workspace

HR turns a Staff group on for Google Workspace on the group's page, with three settings:

| Setting | Rule |
| --- | --- |
| **Group address** | The group's email address, on one of the verified domains; empty for the group's key at the address domain. It cannot change once the group exists. |
| **Who sees the group** | *Everyone in the organisation* — anyone in the organisation sees the group and its members, and finds it in the directory — or *Members only*. Written through Groups Settings. |
| **Licences** | The licences every member with a Google account gets, as long as they are in the group. |

The group is made at the next run, with Staff's name and description; its owners are
Staff's owners of the group, as members with the role *Owner*; its members are the
group's people whose Google account is active. A member somebody adds in the Admin
console is removed at the next daily sync. When Staff lets the group go, a group Vantigo
made is deleted in Google; a group it found is left to the organisation.

### Photos

Staff's photo of a person — HR's, or the person's own — is written to the account,
cropped to its centre square and scaled to **96 × 96 pixels**, the size Google keeps. A
photo Google refuses, or one that cannot be read, is not an error: the person's Account
tab says *The directory refused the photo.* and the dashboard tells HR, until another
photo is written.

## What a leaver's steps do

The day after a person's last employment ends, Staff's worker has Vantigo **suspend**
the account first — always, whatever the account rules say — and forget a first password
nobody has shown. Then, in order, each step the account rules turn on:

1. **Remove the licences** — every licence Vantigo assigned, and every licence of the
   organisation's (as last read) the person holds. Google bills a suspended user's
   licence, so this is on unless turned off.
2. **Leave the groups** — *the groups Vantigo made* (the default), *all* the person's
   groups, or *none*.
3. **Sign out of every session** — Google's *sign out*. If Google refuses it, the person's
   Account tab says the leaver was not signed out; the suspension already keeps them out.

A step that fails leaves the rest to the next run, and the person's Account tab names
it. A rehire later opens the same account again.

## Delete after N days

Vantigo deletes a leaver's Google account only when Staff's **Delete an external account
(days after the end)** says so — the same setting as for Microsoft Entra ID, under
`staff:manage` in Staff's settings › **Accounts**, empty for never
([Staff](/en/admin/staff/#the-accounts-worker)). The account must be suspended. Google
keeps a deleted user restorable from the Admin console for 20 days. The module has no
delete setting of its own.

## Accounts and groups Vantigo did not create

An account Google already holds under a person's employee number that Vantigo did not
create — a person who had Google before Vantigo — is linked to the person but **not
written to**: no profile, no password, no suspension, no delete. Its row on the
**Accounts** tab says *Found — confirm*, and Staff's dashboard says *The directory
holds an account under …'s employee number that Vantigo did not create*.

1. On the **Accounts** tab, check the address and the employee number are the person's.
2. Choose **Confirm** beside the account, and confirm.

From then on Vantigo manages the account as one it made, and Staff's next run brings it
to the person. An account with an administrator role in Google is never written to,
confirmed or not: give the person an account of their own, or handle theirs by hand.

A group Google already holds at the address a Staff group would get, not made by
Vantigo, waits the same way: **Confirm** it beside the group on the **Accounts** tab. A
confirmed group is written to, but never deleted.

## Rotate a key

Service account keys do not expire. Rotate yearly, and at once if a key may have leaked.

1. On the service account, **Keys** › **Add key** › **Create new key** › **JSON**.
2. Upload it in step 2. The newest live key is used, so **In use** moves to it.
3. Test the connection.
4. Choose **Revoke** on the old key, confirm, and delete that key in the Google Cloud
   console: revoking stops Vantigo using it but changes nothing at Google.

Every key Vantigo has stored stays live until revoked. When Google refuses the newest
(deleted in the console, for instance), Vantigo marks it *Google refused it* and the
event *Google refused a key*, by Vantigo — once, until the key works again — and uses
the older one; when Google refuses them all, the connection is **Failing**. A key that
works again loses the mark. An address Google does not know is not held against the
keys.

## Change the service account

Delegation is granted to one service account's client id, so Vantigo refuses a key of
another service account while one of the current one's is live (*This key is for another
service account*, `client_mismatch`).

1. Create the new service account and its key, and in the Admin console add a
   domain-wide delegation entry for its client id with the same scopes.
2. In step 2, **Revoke** every key of the current service account. The connection is
   *Not set up* until a key is uploaded.
3. Upload the new key, and test the connection.
4. Remove the old client's delegation in the Admin console, and delete the old service
   account or its keys.

## What can go wrong

The **Status** tab names the status and the reason; the events below it say what was
done, and by whom.

| What you see | Means | Do |
| --- | --- | --- |
| **Not set up** | No live key, or no admin to impersonate — or the admin was just changed. | Finish steps 2 and 4, and test. |
| **Waiting for delegation** (`not_delegated`) | Google refused every scope: the delegation is not entered, is entered for another client id, or is not applied yet. | Check the **Client ID** and the scopes in the Admin console; wait up to 24 hours, and for a second super admin's approval under multi-party approval; test again. |
| **Connected**, with scopes listed as refused | Some of the scopes are delegated and some are not. | Add the missing scopes to the client's delegation, and test again. |
| **Failing** (`credential_invalid`) | Google refused every key — deleted or disabled in the Cloud console. A key sealed under another `APP_SECRET` cannot be opened either. | Upload a new key if the old one is gone; test again. |
| **Failing** (`admin_refused`) | Google does not know the admin to impersonate — the address is mistyped, not in the organisation, or the user is suspended — or the token was issued but the Admin SDK answered 403: the admin may not read the organisation, or the Admin SDK API is not enabled on the project. The keys are not marked. | Check the address in step 4; name a super admin, or give the admin's role the right to read users and domains; enable the **Admin SDK API**; test again. |
| **Failing** (`google_unreachable`) | Google did not answer, or answered errors twice. | Check outbound https to the hosts [above](#before-you-start) from every process, then test again. |
| *This key is for another service account* (`client_mismatch`) | The uploaded key belongs to another service account than the live keys. | Upload a key of the current account, or [change the service account](#change-the-service-account). |
| *Scopes Vantigo does not use* | The delegation carries scopes Vantigo never asks for. | Remove them from the delegation unless another product needs them. |
| The console says key creation is disabled | The organisation enforces `iam.disableServiceAccountKeyCreation`. | [Lift it for this project](#create-the-service-account-and-its-key). |
| Staff's **Accounts** card: Google Workspace failing (`scopes_missing`) | The last test was refused one of the scopes of the accounts, the groups, the group settings or the licences, or was made before they were needed. | Add them to the delegation, and test again. |

**The reasons on a person's account.** Staff's Account tab and dashboard say what Google
could not do, by these codes:

| Code | Means | Do |
| --- | --- | --- |
| `external_ambiguous` | Two accounts in Google hold the person's employee number. | Remove the number from the one that is not theirs, in the Admin console. |
| `external_address_taken` | The person's work email is another account's, or a group's. | Change the work email in Staff, or free the address. |
| `external_unconfirmed`, `adoption_unconfirmed` | An account Vantigo did not create holds the number. | [Confirm it](#accounts-and-groups-vantigo-did-not-create). |
| `external_privileged`, `privileged` | The account holds an administrator role in Google. | Handle it by hand, or give the person another account. |
| `external_enabled` | A delete was asked of an account that is not suspended. | Nothing: the leaver's suspension comes first, and the delete follows. |
| `pass_not_allowed` | The organisation's password policy refused the first password. | Reset the password in the Admin console, or relax the policy. |
| `licence_unavailable` | No licence of that SKU is left to assign. | Buy more, or free one; Staff tries again. |
| `manager_not_found` | The manager Staff names has no account in Google. | Give the manager the Google Workspace kind. |
| `photo_not_written` | Google refused the photo, or it could not be read. | Set another photo. |
| `sign_out_refused` | Google refused to sign the leaver out. | Nothing for the suspension; sign them out in the Admin console if needed. |
| `leaver_step_failed` | A leaver's step did not finish (`remove_licences`, `remove_groups`, `sign_out`). | Staff tries it again; check the connection if it keeps failing. |

Vantigo never records Google's own error text, only the status and Google's reason
code; the reason on the **Status** tab is Vantigo's reading of them.
