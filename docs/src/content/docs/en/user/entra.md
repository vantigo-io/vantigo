---
title: Microsoft Entra ID
description: Connecting the company's Microsoft Entra ID tenant on the Microsoft Entra ID page — the Status card, the wizard's six steps and what Done means on each, the permissions to copy, the secret shown once, the connection test, the account rules — and who sees what.
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
| The **Microsoft Entra ID** tile, the page, the **Status** card and the recent events | `entra:manage` |
| Steps **1 Register**, **2 A secret** and **3 Permissions and the role** — saving the tenant, adding and revoking secrets, **Test connection** | an **Owner**, signed in with a second factor; to anyone else with `entra:manage` they are shown read-only |
| Step **4 Accounts** — the account rules | `entra:manage` |
| Steps **5 Sign-in** and **6 Provisioning** | an **Owner**: they are the **Single sign-on** card's forms |

`entra:manage` is in the category Administration and cannot be delegated: only an Owner
gives it to a role. The Owner holds it already.

## The page

Open **Microsoft Entra ID** from the app switcher. The page is a wizard of six steps,
one card each, in the order you do them. Each card says where to go in the Microsoft
Entra admin center, what to copy, and carries a *Done* mark once Vantigo has what the
step needs — the mark comes from what Vantigo has stored, so it is right however the
step was done. Keep the portal open in another tab and work down the page.

### The Status card

Once the connection has been tested, a **Status** card sits above the steps:

- whether the connection works — connected, failing, or not connected yet — and, when
  there is one, the reason in a sentence: a secret refused, the tenant not answering,
  no consent granted, the secrets expired, a secret expiring within 30 days, or the
  User Administrator role apparently missing;
- the organisation's name, as the tenant gives it;
- the permissions the connection has, and each one still missing, by name;
- the secrets, with their labels, when each expires and was last used, which one is
  **In use**, and **Revoke** (an Owner);
- when the connection was last tested;
- how many accounts Vantigo keeps in the tenant — enabled, disabled and deleted;
- the recent events: what Vantigo did in the tenant or to the connection, when, by whom
  (a person, or Staff's accounts worker), with the user principal name and what was
  reported. Never a secret, a password or a pass.

### 1 Register

Register an application for Vantigo in the portal (**App registrations** › **New
registration**, *Accounts in this organizational directory only*), then copy its
**Directory (tenant) ID** and **Application (client) ID** into the two fields and choose
**Save**. Both are GUIDs, as the registration's **Overview** shows them.

*Done* once both are saved. Saving another tenant or client later sends the connection
back to not connected until it is tested again; while Vantigo keeps accounts in the
tenant, it asks you to confirm first, since the accounts were made in the old one.
Nothing is deleted in either tenant.

### 2 A secret

Make a client secret in the portal (**Certificates & secrets** › **New client
secret**), copy its **Value** at once, then choose **Add a secret**: a label to tell it
apart, the value, and the expiry date the portal showed — in the future, at most 24
months ahead. Choose **Save**. The value is a password field and is **never shown
again**, here or anywhere: Vantigo seals it as it arrives.

The list below shows each secret with its label and dates. The newest one that has not
expired or been revoked is the one in use. To rotate, add the new secret first, then
choose **Revoke** on the old one; Vantigo refuses to revoke the only working secret
while it keeps accounts, because nothing could then disable a leaver. Revoking only
stops Vantigo using it: delete it in the portal yourself.

*Done* once a secret is in use.

### 3 Permissions and the role

The card lists the eight Microsoft Graph **application** permissions to add under **API
permissions**, each ready to copy, then **Grant admin consent**. It also says the step
no other product asks for: assign the **User Administrator** role to the application
under **Roles & admins** — without it, Microsoft refuses to enable or disable an
account even with every permission.

Choose **Test connection**. Vantigo fetches a token with the secret, reads the
organisation and its verified domains, and compares the token's permissions with the
eight. The card then says whether the connection works, names the organisation, and
names each permission still missing; when it fails, it says why. The role cannot be
checked from here — check it in the portal; if it is missing, the first account
Vantigo tries to enable is refused, and the **Status** card says so.

*Done* once the last test connected with no permission missing.

### 4 Accounts

The rules every account follows, which anyone with `entra:manage` may change:

- the **domain** the accounts' user principal names end in, chosen from the verified
  domains the test read — a person whose work email is on one of them keeps it as
  their user principal name;
- the **pattern** for everyone else: `given.family`, `g.family` or `given`, with
  Norwegian letters folded (`Ørjan Åsmundsen` becomes `orjan.asmundsen`) and a number
  added when the name is taken;
- the **usage location**, the country written when Staff has none for the person —
  Microsoft gives no licence without one;
- whether to write the **hire date**, and the **leaver's steps**: revoke the sessions,
  write the leave date, remove the directly assigned licences, and which groups a
  leaver leaves — all, Vantigo's own, or none;
- the **Temporary Access Pass**: how long it works (10 minutes to 30 days), whether it
  works for one sign-in only, and who hands it over. The card says, with the path, that
  the tenant's authentication methods policy must enable the pass for the users, or
  Microsoft refuses it.

Choose **Save**. A value outside its rule is marked on its field with the reason. If
someone else saved the rules since you opened the page, reload and save again.

*Done* once the rules are saved with a domain.

### 5 Sign-in

Optional: lets people sign in to Vantigo with their Microsoft account. The card is the
**Single sign-on** card's provider form, already filled in with your tenant's address
and the client id from step 1, with the **Callback URL** to paste into the
registration's **Authentication** › **Web** platform. Complete it and choose **Save**.
Without it, Staff still makes the Microsoft accounts, and nobody signs in to Vantigo
with them.

### 6 Provisioning

Optional: has Entra create each person's Vantigo user as soon as their account is
enabled. The card is the **Single sign-on** card's SCIM block: **New token**, the
**Endpoint URL**, and where to paste both in the portal (**Enterprise applications** ›
the app › **Provisioning**). Without it, a person's Vantigo user appears at their
first sign-in instead.

The card also links to Staff's settings, where **Default account kind** can be set to
**Microsoft Entra ID** for the people registered from now on
([Accounts](/en/user/staff/#accounts)). People already registered keep their kind until
HR changes it on their Account tab.

## After the setup

Nothing more happens here day to day: Staff's accounts worker does the work, and each
person's account shows on their **Account** tab in Staff, where HR shows the
Temporary Access Pass once. Come back to the page when Staff's dashboard says a secret
is about to expire or the connection is failing, and look at the **Status** card and
the recent events.
