---
title: Microsoft Entra ID
description: Connecting Vantigo to a Microsoft Entra ID tenant so Staff's accounts are made there — the app registration, the client secret, the eight application permissions and the User Administrator role, admin consent, the Temporary Access Pass policy, the account rules, sign-in and provisioning; the Outlook connection — the redirect URI on APP_URL, the delegated permissions and their admin consent, the switch, verifying by connecting yourself, turning it off, what happens to a person's connection and entra-outlook-sweep, and what can go wrong; the warning for permissions Vantigo does not use; calendar sync on each person's own Outlook connection — the preconditions, verifying with a connected person, which mailboxes, removing an earlier setup's application Calendars.ReadWrite and its RBAC for Applications assignment, the switch off, a disconnect and a leaver's copies left in their mailbox, and change notifications; mail channels — the switch, the RBAC for Applications commands the page fills in, removing a tenant-wide Mail permission, verifying with Test-ServicePrincipalAuthorization and a channel, the cache, turning it off and what can go wrong; what a leaver's steps do and what deleting loses; Staff's groups in the tenant, the application as their owner, confirming a group Vantigo did not create and what an archived group becomes; rotating secrets; the tagged test against a real tenant; and what each reason code means.
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
  - apps/server/internal/entra/mailbox_writeback.go
  - apps/server/internal/entra/mailbox_notifications.go
  - apps/server/internal/entra/mailbox_notifications_endpoint.go
  - apps/server/internal/entra/outlook.go
  - apps/server/internal/entra/outlook_sweep.go
  - apps/server/internal/entra/channel_mailbox.go
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

Staff's accounts worker, `staff-accounts`, calls the module
([Staff](/en/admin/staff/#the-accounts-worker)). The module's own one worker,
`entra-outlook-sweep`, tidies the people's [Outlook connections](#outlook-connection)
every hour; it has no setting.

## Connect the tenant

Open **Microsoft Entra ID** from the app switcher. The page is the wizard: six steps,
each a card with the portal path, what to copy, and *Done* once Vantigo has what it
needs (*Optional* on the last two); a **Status** card sits above them once the
connection has been tested. Work through them in order, the portal in another tab.

### 1. Register the application

1. In the **Microsoft Entra admin center**, open **App registrations** › **New
   registration**.
2. Name it (for example `Vantigo`), choose **Accounts in this organizational directory
   only**, and leave **Redirect URI** empty for now — the [Outlook
   connection](#outlook-connection) adds one later. Choose **Register**.
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

   These eight are all: [calendar sync](#calendar-sync) needs no application
   permission, since it runs on each person's own Outlook connection, and [mail
   channels](#mail-channels) get theirs in Exchange Online, scoped to their mailboxes,
   not here.
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

**Permissions Vantigo does not use.** When the token carries application permissions
beyond the eight, the **Status** card shows an orange notice,
*The application holds permissions Vantigo does not use:*, with the list. An application
permission reaches every mailbox or account in the tenant, so one nobody needs is better
gone: unless another product uses this registration, remove it under **API
permissions** and test again. An application `Calendars.ReadWrite` left from an earlier
release, when calendar sync wrote with it, is among them ([calendar
sync](#calendar-sync)). It is a warning only — the connection stays
**Connected** — and the test records each as *a permission Vantigo does not use* in the
recent events. The mail permissions (`Mail.*`, and `MailboxSettings.*`,
`MailboxItem.*` and `MailboxFolder.*`) are listed among them while [mail
channels](#mail-channels) are off; while they are on, the **Mail channels** line shows
them instead, as access to every mailbox in the tenant.

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

## Outlook connection

The **Outlook connection** lets each person connect their own Outlook to Vantigo once,
under **Settings → Connections**, by signing in to Microsoft — usually without a page,
when single sign-on answers ([the user guide](/en/user/connections/)). Vantigo then holds
the person's delegated sign-in to their mailbox, sealed, and shows *Reconnect Outlook*
to that person alone when Microsoft refuses it. Only a person whose Staff record names
an account Vantigo manages can connect, and only as that account. The calendar's sync
runs on it ([calendar sync](#calendar-sync)): each person who connects is synced with
their own sign-in, and nobody else. How it works is in [the reference](/en/reference/entra/#the-outlook-connection).

### What the Outlook connection needs

- The connection is set up and **Connected** ([above](#connect-the-tenant)).
- The people who will connect have the **Microsoft Entra ID** account kind in Staff, and
  their account is one Vantigo made or whose adoption is confirmed
  ([below](#accounts-vantigo-did-not-create)).
- `APP_URL`, with `APP_BASE_PATH` when Vantigo is served under a path, is the address
  people reach Vantigo by: the redirect URI is made from them, never from
  `PUBLIC_BASE_URL` ([Installation](/en/admin/installation/#base-path-and-reverse-proxy)).
  Change either later and the URI changes with it.

### Turn on the Outlook connection

1. On the module page, in step **4 Set the account rules**, under **Outlook
   connection**, copy the **Redirect URI** — `APP_URL`, the base path and
   `/api/v1/entra/outlook/callback`, such as
   `https://vantigo.example.com/api/v1/entra/outlook/callback`.
2. In the registration, open **Authentication**. Under the **Web** platform — the one
   step 5's sign-in added, or **Add a platform** › **Web** if there is none — add the
   URI as a redirect URI, and choose **Save**. It must be a **Web** platform, not a
   single-page application: Vantigo redeems the code on the server with its client
   secret.
3. Open **API permissions** › **Add a permission** › **Microsoft Graph** › **Delegated
   permissions**, and add `openid`, `profile`, `offline_access` and
   `Calendars.ReadWrite`. These are the delegated permissions, beside the application
   permissions of step 3.
4. Choose **Grant admin consent for** your organisation, and confirm, so nobody is asked
   to consent — or refused for want of approval — when they connect.
5. Back on the module page, turn on **Outlook connection: let people connect their
   Outlook to Vantigo** and choose **Save the account rules** (`entra:manage`). Calendar
   sync is then available to everyone who connects ([below](#calendar-sync)); step 3
   asks for nothing more.

### Verify the Outlook connection

Connect yourself: open **Settings → Connections** and choose **Connect Outlook**. You are
back on the page within seconds, reading **Connected as** and your account. On the module
page the line under the switch has moved from **Nobody has connected yet: …** to
**Ready**, *People connected:* counts you, and the **Status** card's recent events show
*A person connected Outlook*.

### Turn the Outlook connection off

Turn the switch off and choose **Save the account rules**. While anyone is connected or
waiting to reconnect the page asks first — *Everyone will have to connect Outlook
again.* — and **Turn off and save** saves it. Every person's connection closes in the
same save, each recorded as *An Outlook connection was closed* (*the Outlook connection
was turned off*); the calendar's sync stops for everyone and its copies are removed,
each person's with their own closing connection. Turned on again, each person connects
again, and nobody's calendar sync resumes until they have.

### What happens to a person's connection

- **Microsoft refuses it.** A password reset, revoked sessions, a Conditional Access
  policy, a new multi-factor requirement or a sign-in that expired moves the connection
  to *Reconnect needed*, with the reason, on that person's page; *Waiting to reconnect:*
  counts them, and the event reads *Microsoft refused a person's Outlook connection*.
  Vantigo declares the client capability `cp1`, so Microsoft tells it at once when an
  account is disabled or its sessions revoked. Their calendar sync pauses, their copies
  left as they were, and picks up once they reconnect; nothing else stops.
- **They leave.** When Staff disables the account, the connection closes and its stored
  token is forgotten in the same step, before the leaver's other steps run. **The copies
  calendar sync put in their mailbox stay there**, in the disabled mailbox: nothing is
  left that could remove them, so Vantigo forgets them.
- **Their account is deleted or disabled** in Vantigo or in the tenant. The connection
  closes, its stored token forgotten, when it is next used, or — for one waiting to reconnect — when the worker
  `entra-outlook-sweep` reads the account in the tenant, at most once a day. Their
  calendar copies stay, as a leaver's.
- **They disconnect.** Vantigo closes the connection; it never signs them out of
  Microsoft. **Calendar sync removes their copies** with the closing connection, then
  waits for them to connect again.

A closed connection is deleted by `entra-outlook-sweep`, the module's worker, an hour at
a time: seven days after it closed, or once a day has passed both since it was last used
and since it closed, when nothing is left for it to clean up. The worker has no setting and runs wherever the module's workers run
([Installation](/en/admin/installation/#background-workers-and-scaling)).

### When connecting fails

| Where | Means | Do |
| --- | --- | --- |
| Microsoft's page shows `AADSTS50011` (the redirect URI does not match) | The registration's **Web** platform lacks the redirect URI exactly as Vantigo shows it — never added, a typo, a single-page application platform, or `APP_URL` or `APP_BASE_PATH` changed since. | Copy the URI from the module page again and add it to the **Web** platform. |
| Microsoft asks the person to consent, or says *Approval required* / *Need admin approval* | Admin consent was not granted for the delegated permissions. | **Grant admin consent** ([above](#turn-on-the-outlook-connection)) and have them connect again. |
| *The last connect lacked a delegated permission: …* under the switch; the person sees *Your organisation has not consented to the permissions the connection needs. …* | Microsoft granted the sign-in without `Calendars.ReadWrite` (delegated). Nothing was stored for the person. | Add the delegated permission, grant admin consent, and connect again; the line clears at the first connect that completes. |
| People in *Reconnect needed* after a Conditional Access or multi-factor change | Expected: Microsoft wants a new sign-in. *blocked* means a policy refused it. | They choose **Reconnect Outlook**. For *blocked*, check the policy's conditions (device, location, risk) for Vantigo's application. |
| The person sees *You signed in to Microsoft with another account than the one Vantigo knows you by. …* | They signed in as another account than the one Staff links to their person (`account_mismatch`). | They connect again as that account. If Staff links the wrong one, HR corrects it on the person's Account tab. |
| *Another person in Vantigo has already connected this Microsoft account.* | Another user holds a live connection of that account (`account_taken`). | Find who in the recent events (*A person connected Outlook*, with the account); that person disconnects, or HR corrects the account link. |
| *Nothing to connect: …* on a person's page | The switch is off, or the person has no account Vantigo manages. | Turn the switch on, or give the person the Microsoft Entra ID kind and confirm their account. |
| *The connect took too long, or was started in another session.* | More than ten minutes at Microsoft, or the browser lost the connect's cookie — another browser, a private window closed, or cookies blocked for Vantigo — or a newer connect was started before this one completed, in another tab say, which cancels it. | Connect again in the same browser. |

## Calendar sync

**Calendar sync** lets people keep a copy of their Vantigo calendar in their Outlook
([the user guide](/en/user/calendar/#sync-with-outlook)): the calendar's
`calendar-mailbox-sync` worker writes each person's appointments, and the items they
choose, to their Exchange Online mailbox, and after each sync reads their **default
calendar** back — the times of the events that make them busy, never the titles — to
show that busy time in Vantigo. Before each push it also reads Vantigo's **own copies** —
and, for a person who chose a separate *Vantigo* calendar, every event in that calendar —
whole, to take back the edits, moves and deletes the person made in Outlook to their own
appointments ([the user guide](/en/user/calendar/#change-your-appointments-in-outlook));
it never reads the person's own events whole.

**It runs on each person's own [Outlook connection](#outlook-connection)**: every write
and read of a mailbox uses that person's delegated sign-in, whose delegated
`Calendars.ReadWrite` covers it all. Nothing is granted to the application for it — no
application permission, no Exchange role — so Vantigo can reach only the mailboxes of the
people who connected. It is off by default, and provisioning works the same with it on or
off. What it writes and reads, and how, is in [the
reference](/en/reference/entra/#the-mailbox-calendar).

### Preconditions

- The connection is set up and **Connected** ([above](#connect-the-tenant)), and the
  Calendar module is enabled ([Calendar](/en/admin/calendar/)).
- The [Outlook connection](#turn-on-the-outlook-connection) is set up and on: the
  redirect URI and the delegated permissions with admin consent.
- The people who will sync have the **Microsoft Entra ID** account kind in Staff, and
  their account is one Vantigo made or whose adoption is confirmed
  ([below](#accounts-vantigo-did-not-create)): Vantigo writes only to a mailbox it knows
  is the person's, never one matched by address.
- Their accounts have an Exchange Online mailbox (a licence that includes Exchange
  Online).
- **Each person connects their own Outlook** — on **Settings → Connections**, or with
  **Connect Outlook** on the calendar's **Outlook** card.

### Turn it on

There is nothing to turn on beyond the [Outlook
connection](#turn-on-the-outlook-connection): with its switch on, calendar sync is
available to everyone who connects. No application permission and no admin step besides
the connection's delegated `Calendars.ReadWrite` and its consent.

Verify with a person who has connected: they open **Calendar → My calendar** and turn on
**Sync to my Outlook calendar**; within a minute their card says **Last synced** and the
appointments are in Outlook. A person who turns it on before connecting sees **Connect
Outlook above to start the sync.**: their sync waits, and starts within a minute or so of
their connecting. The rest of the module works as before.

**Which mailboxes.** Only the people who connect their own Outlook are synced, and only
their own mailbox: the connection signs in as them, and Microsoft refuses it any other
mailbox. There is no scope to configure and no Exchange RBAC to set up. To keep someone
out of calendar sync, leave them unconnected — or block the connection for them with a
Conditional Access policy on Vantigo's application, which their card then shows as
*Reconnect needed*.

**A setup from an earlier release.** Calendar sync used to write with the
`Calendars.ReadWrite` **application** permission, sometimes narrowed with Exchange RBAC
for Applications. Nothing uses either any more:

1. In the registration's **API permissions**, remove `Calendars.ReadWrite` of type
   *Application* (keep the *Delegated* one), and choose **Test connection** in step 3:
   until you do, the **Status** card lists it under *The application holds permissions
   Vantigo does not use:*.
2. If you made an RBAC for Applications assignment, remove it in Exchange Online
   PowerShell (`Connect-ExchangeOnline`): list the assignments of the role, and remove
   Vantigo's by its name.

   ```powershell
   Get-ManagementRoleAssignment -Role "Application Calendars.ReadWrite" | Format-Table Name, RoleAssignee, CustomResourceScope
   Remove-ManagementRoleAssignment "<the assignment's name>"
   ```

   Then, if nothing else uses them, the management scope (`Remove-ManagementScope
   "Vantigo calendar sync"`) and the group.
3. Ask the people who synced before to connect their Outlook: until they do, their card
   asks them to, and their copies stay as they were.

**What a change in Outlook does.** A person's edit or move of their own appointment's
copy changes the appointment in Vantigo at the next sync, through the same rules and
mails as a change made in Vantigo. **Deleting the copy in Outlook — or moving it to
another calendar or folder — deletes the appointment in Vantigo**, or cancels it, with a
cancellation mailed to the people outside the company, once they were invited; for one
occurrence of a repeating appointment, it cancels that occurrence. More than ten such
deletes of one person's appointments in one sync are none of them taken — the copies
are put back — so a calendar emptied by mistake does not empty Vantigo. A change to anything
else — an appointment the person attends, another app's item — is put back, and a copy
the person adds attendees to is left to them. Vantigo takes at most 20 such changes a
person per sync; the rest follow at the next.

**Turning the switch off** stops everyone's sync and closes every person's [Outlook
connection](#turn-the-outlook-connection-off); the worker then removes the copies it
wrote, each person's with their own closing connection, while a client secret still
works. **Turning it on again resumes no one by itself**: every connection was closed, so
each person connects again, and their sync picks up once they have.

**A person who disconnects** has their copies removed the same way, and their sync waits
for them to connect again. **A person waiting to reconnect** is paused, their copies left
as they were, until they reconnect. **A leaver's copies stay in their mailbox**: Vantigo
forgets the leaver's sign-in when it disables their account, before anything else, so
nothing is left that could remove them, and Vantigo forgets them instead; the mailbox is
disabled with the account. The same goes for an account disabled or deleted elsewhere.
Remove them in Exchange yourself if you need to — they carry the marker described in [the
reference](/en/reference/entra/#the-account-check).

### Change notifications

By default Vantigo takes a change made in Outlook at the next sync, every 15 minutes.
With **change notifications**, Microsoft tells Vantigo when something changes in a synced
mailbox, and the person's sync becomes due at once: the change is taken within about a
minute. Vantigo asks for one notification subscription per synced mailbox, on that
mailbox's events, and renews it before it lapses. **A notification is a nudge and nothing
more**: it carries no appointment, and Vantigo still reads the mailbox as before; the
15-minute poll runs whether notifications work or not, so nothing depends on them. What
Vantigo asks and answers is in [the
reference](/en/reference/entra/#change-notifications).

#### Preconditions

- Calendar sync is on and working ([above](#turn-it-on)). Notifications need no extra
  permission: each person's subscription is made with their own connection, and the
  delegated `Calendars.ReadWrite` they consented to covers it.
- **`PUBLIC_BASE_URL` is set**, and Microsoft can reach it from the internet over
  https: a host name with a certificate a public authority signed. Set up the public host
  as for the pay page ([The public host](/en/admin/payments/#the-public-host)); the same
  proxy rules apply. It must pass `POST /api/v1/entra/mailbox-notifications` through to
  Vantigo — the one path Entra puts on the public host's list, anonymous because
  Microsoft sends no session.
- `CALENDAR_MAILBOX_NOTIFICATIONS_ENABLED` is not `0` (the default is on).

#### Turn it on

1. Set `PUBLIC_BASE_URL` in `vantigo.env` (a restart reads it) and leave
   `CALENDAR_MAILBOX_NOTIFICATIONS_ENABLED` unset, or `1`.
2. From outside, check that the path gets through. A notification with nothing in it is
   accepted, and Microsoft's handshake — which Vantigo answers only while it is creating
   a subscription — is not:

   ```sh
   curl -i -X POST https://pay.example.com/api/v1/entra/mailbox-notifications \
     -H 'Content-Type: application/json' -d '{"value":[]}'                    # 202
   curl -i -X POST 'https://pay.example.com/api/v1/entra/mailbox-notifications?validationToken=x'   # 404
   ```

3. Wait for the next sync of a person who has it on: after one that worked, Vantigo
   subscribes their mailbox.

Verify: on the module page, under **Calendar sync**, **Change notifications** reads
**On, watching 1 mailbox. Last notification: none yet.** (the number grows with the
mailboxes). Change an appointment's copy in that person's Outlook: within a minute the
change is in Vantigo and **Last notification** shows the time.

#### Turn it off

Set `CALENDAR_MAILBOX_NOTIFICATIONS_ENABLED=0` and restart. Nothing new is subscribed and
the poll alone runs; the line reads **Turned off by the operator …**. Subscriptions that
exist are not deleted by this — they lapse within seven days, and a notification of one
only makes a sync due — but each is deleted in Microsoft when the person's copies are
cleaned up (one Microsoft refuses to delete is left to lapse the same way). An empty `PUBLIC_BASE_URL` has the same effect, and the line reads **Needs
PUBLIC_BASE_URL …**.

#### What can go wrong

- **Microsoft could not reach the address since …** When Microsoft creates a
  subscription it first posts a validation request to the address, and that failed.
  Vantigo makes no subscription for **an hour**, so one wrong address does not cost an
  attempt per person, and then tries again. Check from outside that
  `PUBLIC_BASE_URL` resolves, its certificate is valid and its chain complete, that the
  proxy passes the path through with `Host` set to the public host, and that no firewall
  or web application filter drops requests from Microsoft. The line clears at the first
  subscription that works. Meanwhile the poll takes the changes.
- **Watching fewer mailboxes than people.** A person who has not connected their
  Outlook, or is waiting to reconnect, has no working sync and so no subscription — one
  Microsoft asks to renew while they wait to reconnect is left to lapse, and made again
  once they reconnect. A mailbox Microsoft refuses is refused for notifications as it
  is for the sync. A person whose sync has not pushed yet is subscribed after the first
  one that does.
- **A notification arrives, nothing changes.** The sync it makes due reads the mailbox;
  if that read is refused or fails, the card says so, as for any sync.
- **Another application's subscription on the same mailbox.** Vantigo deletes only a
  subscription that is on this mailbox's events and posts to this installation's own
  address, and never another's. If Microsoft refuses a new one because of a subscription
  that is **another installation's** — two Vantigo installations sharing one application
  registration — Vantigo logs a warning naming the tenant, does not try that mailbox
  again for **24 hours**, and the poll alone serves it.
- **`PUBLIC_BASE_URL` changed.** A subscription made for the old address is deleted at
  the person's next sync and made again for the new one, so the old address is never
  notified again. If Microsoft refuses the delete, the next sync tries it again.
- **A failing sync is not hurried.** A notification makes a person's sync due at once
  only when it is not backing off a failure; a person whose sync keeps failing waits for
  its own retry times.
- **No notifications from a busy tenant.** The webhook accepts 600 requests a minute per
  client address; Microsoft retries a refused one for hours, and the poll catches
  whatever is missed. Behind a proxy, set `TRUSTED_PROXY_HOPS` and
  `TRUSTED_PROXY_CIDRS` so Microsoft's addresses are told apart from the proxy's
  ([Forwarded headers and
  HTTPS](/en/admin/authentication/#forwarded-headers-and-https)).

## Mail channels

**Mail channels** let Communications send a team's mail from an Exchange Online **shared
mailbox** — `post@`, `support@`, `faktura@` — as a channel ([the user
guide](/en/user/communications/#set-up-a-channel)). Vantigo reaches those mailboxes with
the **application's own access**, not a person's, and that access is granted in
**Exchange Online with RBAC for Applications**, limited to the mailboxes you mark, not
with `Mail.*` permissions in the app registration, which would reach every mailbox in
the tenant. It is off by default.

A channel **sends and receives**: its replies are sent from the shared mailbox and kept
in its **Sent Items**, and the mail arriving in the mailbox's **Inbox** from the chosen
import window (*From now on*, *Last 30 days* or *Last 90 days*) is read into
Communications' Inbox — within about a minute when [change
notifications](#change-notifications) (`PUBLIC_BASE_URL` and
`CALENDAR_MAILBOX_NOTIFICATIONS_ENABLED`) are on, or within about five minutes otherwise.
When notifications are on, Vantigo subscribes each active channel's Inbox on the
application's token and keeps a one-hour safety-net poll. While a conversation is open,
Vantigo leaves the message in the Inbox as it was; when a conversation is closed, it
leaves it as-is, marks it as read, or moves it to a folder in the shared mailbox according
to the channel's setting. **Application Mail Full Access**, below, already
covers reading, subscriptions and on-close changes; there is nothing more to grant. How
the module sends and reads is in
[the reference](/en/reference/entra/#mail-channels).

### Preconditions for mail channels

- The connection is set up and **Connected** ([above](#connect-the-tenant)): its test
  reads the application's **object id** under **Enterprise applications**, which the
  commands need. The Communications module is enabled.
- The mailboxes are **shared mailboxes** in Exchange Online. A person's mailbox is
  refused as a channel.
- You can run **Exchange Online PowerShell** (`Connect-ExchangeOnline`, from the
  `ExchangeOnlineManagement` module) as an **Exchange Administrator** in Entra, or a
  member of **Organization Management** in Exchange.
- `CustomAttribute10` is free on those mailboxes — the commands mark the channel
  mailboxes with it. If something else uses it, pick another of the fifteen and change
  both the `Set-Mailbox` and the scope's filter.

### Turn on mail channels

1. On the module page, in step **4 Set the account rules**, under **Mail channels**, turn
   on **Mail channels: let Communications send and receive through Exchange Online shared
   mailboxes** and choose **Save the account rules**. Anyone with `entra:manage` may.
2. Under the switch, **Commands for Exchange Online PowerShell** shows six commands with
   your application's **client id** and its **object id** filled in. If it says *Test the
   connection first*, choose **Test connection** in step 3 (an Owner) and come back: the
   placeholders `<client id>` and `<object id>` are filled once a test has read them.
   Copy the commands with the copy button beside them (*Copy Commands for Exchange Online
   PowerShell* to a screen reader).
3. In Exchange Online PowerShell, run them in order, replacing `post@firma.no` with a
   shared mailbox's address:

   ```powershell
   New-ServicePrincipal -AppId <client id> -ObjectId <object id> -DisplayName "Vantigo"
   Set-Mailbox post@firma.no -CustomAttribute10 "Vantigo"
   New-ManagementScope -Name "Vantigo mail channels" -RecipientRestrictionFilter "CustomAttribute10 -eq 'Vantigo'"
   New-ManagementRoleAssignment -App <client id> -Role "Application Mail Full Access" -CustomResourceScope "Vantigo mail channels"
   New-ManagementRoleAssignment -App <client id> -Role "Application MailboxSettings.Read" -CustomResourceScope "Vantigo mail channels"
   Test-ServicePrincipalAuthorization -Identity <client id> -Resource post@firma.no
   ```

   - `New-ServicePrincipal` makes the application known to Exchange. Its `-ObjectId` is
     the object id under **Enterprise applications**, not the one under **App
     registrations** — the page fills in the right one.
   - `Set-Mailbox … -CustomAttribute10 "Vantigo"` marks one mailbox. **Run it once for
     each shared mailbox** a channel will use, now or later; the scope follows the mark.
   - `New-ManagementScope` is the set of marked mailboxes.
   - The two `New-ManagementRoleAssignment` grant, inside that scope only, **Application
     Mail Full Access** (read, write and send) and **Application MailboxSettings.Read**
     (to tell a shared mailbox from a person's).
4. **Remove any tenant-wide mail permission.** In the registration's **API permissions**,
   remove every application permission `Mail.Read`, `Mail.ReadBasic`,
   `Mail.ReadBasic.All`, `Mail.ReadWrite`, `Mail.Send`, `MailboxSettings.Read`,
   `MailboxSettings.ReadWrite`, `MailboxItem.Read.All`, `MailboxItem.ImportExport.All`,
   `MailboxFolder.Read.All` or `MailboxFolder.ReadWrite.All` (and `full_access_as_app`
   under *Office 365 Exchange Online*), then choose **Test connection** in step 3. Exchange adds
   the scope's grants to Entra's: a consent in the registration reaches every mailbox
   whatever the scope says.

### Verify mail channels

1. `Test-ServicePrincipalAuthorization` (the last command) answers one line per role:
   **`InScope` is `True`** for both *Application Mail Full Access* and *Application
   MailboxSettings.Read* on the mailbox. It reads Exchange's own assignments, not the
   registration's consents, and is not delayed by the cache below.
2. On the module page, the line under the switch reads **Ready: at the last test the
   application held no Microsoft Graph mail permission for the whole tenant. Access granted
   in Exchange is not visible here — check it with Test-ServicePrincipalAuthorization.**
   It speaks for the registration's permissions only; step 1 is the check of Exchange's.
3. In Communications, add the mailbox as a channel: **Channels** › **Add channel**, the
   provider **Microsoft 365**, its address ([the user
   guide](/en/user/communications/#set-up-a-channel)). Vantigo checks it then — the
   address resolved, the mailbox shared, its Inbox reachable — and the channel is created
   active. **Verify** on its row checks again at any time.
4. Send a mail to the mailbox from outside. Within about five minutes it is a
   conversation in Communications' Inbox, and the channel's **Receive status** on the
   **Channels** page reads **Receiving**, with when it last synced; until the first sync
   it reads **Waiting for first sync**.

**Exchange takes 30 minutes to 2 hours** to apply a new assignment or a newly marked
mailbox to the application's access. Until it does, adding the channel is refused as
outside the scope although the test in step 1 says `InScope`; wait and try again.

### Turn mail channels off

Turn the switch off and save. Nothing is deleted: the channels stay, Microsoft 365 is no
longer offered for new ones, and a Microsoft 365 channel's mail waiting to be sent is
**held**: it spends none of its eight attempts and is tried again at most 5 minutes apart,
until **24 hours after it was queued**, when it is given up as `provider_unavailable` and
counted as failed. Turn it on again within that day and the mail is sent. Receiving
pauses too: no mail is read while the switch is off, and once it is on again the mail
that arrived meanwhile is read — none is lost, as the mailbox keeps it. The same holds
while the connection is failing or the Entra module is off. The
Exchange assignments stay until you remove them (`Remove-ManagementRoleAssignment`,
`Remove-ManagementScope`, and `Set-Mailbox … -CustomAttribute10 $null`).

### What can go wrong with mail channels

- **The line under the switch reads *Mail access is granted to every mailbox in the
  tenant. Limit it with RBAC for Applications and remove the tenant-wide consent. While
  this shows, anyone who manages channels in Communications can send as any shared mailbox
  in the tenant.*** (`tenant_wide`, orange, also on Communications' **Channels** page).
  The last test's token held a mail permission for the whole tenant, granted in the
  registration (step 4's list). The channels still work — it is a warning — but the scope
  limits nothing while the consent stands: a channel can be added for any shared mailbox.
  Remove it (step 4) and test again. The test cannot see `full_access_as_app` or an Exchange
  assignment, so check those yourself.
- **Adding a channel says the mailbox is outside the application's scope**
  (`mailbox_denied`). The mailbox is not marked, the scope's filter does not match its
  mark, one of the two roles is missing, or the change has not applied yet. Run
  `Test-ServicePrincipalAuthorization -Identity <client id> -Resource <address>`: both
  roles `InScope` means wait for the cache; otherwise mark the mailbox or add the missing
  assignment.
- **Adding a channel says it is a personal mailbox** (`personal_mailbox`). Only shared
  mailboxes can be channels. Convert the mailbox to shared in Exchange, or use a shared
  one.
- **Adding a channel says Microsoft 365 has no mailbox with this address**
  (`mailbox_unknown`). No user has it as user principal name or primary mail address —
  an alias may not resolve — or the mailbox is gone. Use the mailbox's primary address.
- **Microsoft 365 is not offered in Communications**, or adding says *Microsoft 365 mail
  channels are not available. An Entra administrator turns on Mail channels in the Entra
  settings.* (`mail_channels_off`). The switch is off, the connection is not
  **Connected** (the line under the switch reads *Waiting for the connection* with the
  reason), or the Entra module is not enabled.
- **A message on a Microsoft 365 channel was given up at once.** The outbox stops after
  one attempt when the mailbox is outside the scope, gone or no longer known to Vantigo,
  the message is over the size Graph or the mailbox takes, or a blind copy (Bcc) would have
  been lost; the code is in the message's failure event and the log (`mailbox_denied`,
  `mailbox_gone`, `mailbox_unknown`, `too_large`, `bcc_unsupported`). Fix the scope or the
  mailbox, and send again.
- **A message was given up as `provider_unavailable`.** Mail channels stayed off, or the
  connection failing, for 24 hours after the message was queued. Fix the switch or the
  connection, and send again.
- **Mail sent to the mailbox does not appear in Communications.** Look at the channel's
  **Receive status** on Communications' **Channels** page:
  - **Waiting for first sync** for more than a few minutes: the worker is not running —
    check that a process runs the background workers
    ([installation](/en/admin/installation/)) — or the channel is inactive.
  - **Failed: The application can no longer read the mailbox…** (`mailbox_denied`): the
    mailbox left the scope, or a role was removed. Run
    `Test-ServicePrincipalAuthorization` as above; mark the mailbox or restore the
    assignment, and allow for the **30 minutes to 2 hours** Exchange's cache takes.
    Vantigo tries again **every hour** while it is refused, and reads the mail that
    arrived meanwhile once it is allowed.
  - **Failed: The mailbox no longer exists in Microsoft 365.** (`mailbox_gone`) or **…is
    not known for this channel** (`mailbox_unknown`): choose **Verify** on the channel's
    row; if the mailbox was replaced, add it anew.
  - **Last sync failed: Microsoft 365 did not answer…** (`sync_failed`): throttling or an
    outage; Vantigo backs off from one minute up to an hour and catches up by itself.
    The metric `communications.inbound.cycles_failed` counts such failures.
  - **Receiving**, yet a message is missing: it arrived before the channel was added, or
    longer ago than `COMMUNICATIONS_RETENTION_DAYS` (it is never imported); it was larger
    than `COMMUNICATIONS_INBOUND_MAX_BYTES` (25 MiB by default; skipped); it was sent from
    the channel's own address (taken as Vantigo's own); its import failed three times, or
    a value of it was refused by the database, and it was given up — a warning *mailbox
    sync gave a message up* with the code `import_failed` in the log, the rest of the mail
    imported; or it was not delivered to the **Inbox** — a mailbox rule moved it to
    another folder first. Vantigo reads the Inbox only.
- **Drafts left in a shared mailbox's Drafts folder.** A message with a blind copy is
  sent through a draft; if Vantigo's process stops between creating the draft and sending
  it, the draft stays, and the retry makes a new one. Delete such drafts in Outlook from
  time to time; nothing else removes them.
- **A channel switched from SMTP sends from an alias.** A switched channel keeps its own
  address, which may be an alias of the shared mailbox rather than its primary address.
  Whether Exchange sends `From:` the alias, rewrites it or refuses it is not verified yet;
  check the first message, or add the channel anew by the primary address.
- **A forged sender links a conversation.** Vantigo takes a received message's `From` as
  it stands: a mail whose `From` is forged to a customer's contact address is linked to
  that customer, and a reply — or a reply-all, which copies the message's Cc list — goes
  to the addresses the message names. Keep Exchange Online's anti-spoofing (SPF, DKIM,
  DMARC) and Junk filtering on for the channel mailboxes: Vantigo reads only the Inbox,
  so what Exchange puts in Junk Email never reaches it.
- **Exclusive management scopes** in Exchange do not restrict an application's access,
  and a scope filtered on group membership (`MemberOfGroup`) counts direct members only;
  the attribute filter above avoids both.
- **Not verified on a real tenant yet**: that Exchange keeps Vantigo's own `Message-ID` on
  a message sent this way — on which it depends whether a customer's first answer to a
  conversation Vantigo began joins it or starts a new one —, that a blind copy (Bcc)
  reaches its recipient, how an alias address is sent from, and whether a message moved
  into the Inbox from another folder is read. They are on the release's checklist; until
  then, check one message's headers and one blind copy after setting up the first
  channel, and that a customer's answer to it joins its conversation.

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

Calendar sync's troubles show on the **Status** card, on the line under the switch, and on each person's
**Outlook** card in Calendar (the Outlook connection's own are [above](#when-connecting-fails)):

| Where | Means | Do |
| --- | --- | --- |
| `Calendars.ReadWrite` under *The application holds permissions Vantigo does not use:* on the **Status** card | The application permission left from an earlier release; calendar sync no longer uses it. | Remove it, and any RBAC for Applications assignment ([a setup from an earlier release](#turn-it-on)), and test again. |
| *Connect Outlook above to start the sync.* on a person's card | The person has the sync on but has not connected Outlook, or their connection closed (`not_connected`). Asked again once a day, and at once when they connect. | They choose **Connect Outlook** on the card or under **Settings → Connections**. |
| *Microsoft wants you to sign in again: choose Reconnect Outlook above.* on a person's card | Microsoft refused the person's connection (`reconnect`): a password change, revoked sessions, a Conditional Access or multi-factor rule, an expired sign-in. | They choose **Reconnect Outlook**; for one that keeps coming back, check the policy, as for [connecting](#when-connecting-fails). |
| *Waiting for the connection* under the switch | The connection is not connected; its reason is under the line. | Fix the connection as the table above says. |
| *Your organisation has not allowed Vantigo to reach your mailbox.* on a person's card | Graph answered 403 for their mailbox (`mailbox_denied`): Exchange refuses it, a mailbox without Exchange Online say. Tried again every six hours. | Check the account's mailbox and licence; they press **Sync now** to try at once. |
| *Your mailbox could not be found.* on a person's card | The user has no Exchange Online mailbox, or no longer exists (`mailbox_gone`). Tried again every six hours. | Give the account a licence with Exchange Online. |
| *A change from Outlook was not taken* on a person's card | The person changed something in Outlook that Vantigo does not take — an appointment changed in Vantigo too, a change its rules refuse, someone else's appointment, or an occurrence's or another app's item's fields — and Vantigo put the copy back. The sentence below the heading says which. | Nothing: the person makes that change in Vantigo, or asks the appointment's organizer. |
| A person's edit in Outlook did not reach Vantigo | It was not to their own appointment; it was a title, description or place under **Busy only**; the copy had become a meeting (they added attendees), which Vantigo leaves alone; or the sync is not **Last synced** — a failed read shows *The last sync failed; Vantigo will try again.*, and that sync's push may write Vantigo's version over an edit to a copy Vantigo also changed. | Check the person's card; they press **Sync now**. An edit lost to a failed read is made again in Vantigo. |
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
