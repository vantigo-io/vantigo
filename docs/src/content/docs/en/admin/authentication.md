---
title: "Identity, authentication and deployment"
description: "Local accounts, users Staff manages, the accounts it announces and the roles it may give, the workforce OIDC provider and SCIM, email, proxy trust, APP_SECRET-derived key material and migrations."
sidebar:
  order: 20
sources:
  - apps/server/internal/identity
  - apps/server/internal/config
  - apps/server/cmd/vantigo
  - apps/server/internal/db/migrations/00070_identity_managed_avatars.sql
---
Vantigo authenticates the browser SPA and the same-origin API with an application
cookie issued by the identity module. The API answers JSON `401`/`403` problems
rather than login redirects.

**There is no antiforgery token endpoint and no `X-XSRF-TOKEN` header.** Cross-site
request forgery is refused by origin, not by a token: the server wraps every `/api`
request in Go's `http.CrossOriginProtection`, which rejects unsafe cross-site browser
requests using `Sec-Fetch-Site` (falling back to `Origin` compared against `Host`).
`APP_URL` is registered as a trusted origin so a proxy that rewrites `Host` does not
turn same-origin requests into rejections, a rejection answers `403` as an RFC 7807
problem, and a request carrying neither header — a non-browser client — passes.
Session cookies are `SameSite=Strict` as the second layer.

Run the SPA and API under one public origin. A reverse proxy may serve both paths,
but it must preserve the `Host` header: `X-Forwarded-Host` is never honoured, and
host filtering is derived from `APP_URL`. Cross-origin SPA/API hosting is not a
supported deployment shape.

The workforce OIDC provider and SCIM 2.0 are set by an Owner on the **Single sign-on**
card, not in the environment; for provider setup and the operator recovery runbook,
see [SSO and SCIM operations](/en/admin/sso-scim/).

Every setting this document names is read by
[`apps/server/internal/config/config.go`](https://github.com/vantigo-io/vantigo/blob/main/apps/server/internal/config/config.go),
whose field comments are the authoritative reference. Configuration is parsed once
at startup and **every** problem is reported at once, so a misconfigured container
fails its first boot with the complete list instead of one restart per mistake.

## Cookies

| Cookie | Purpose | Attributes |
| --- | --- | --- |
| `vantigo.session` | The authenticated session token | `HttpOnly`, `SameSite=Strict`, scoped to the base path |
| `vantigo.2fa` | A pending two-factor sign-in's ticket, 5 minutes | `HttpOnly`, `SameSite=Strict` |
| `vantigo.oidc` | OIDC state, nonce and PKCE verifier, sealed | `HttpOnly`, `SameSite=Lax` |
| `vantigo.identity.external` | The validated external identity between callback and completion, sealed | `HttpOnly`, `SameSite=Lax` |

All four are `Secure` exactly when `APP_URL` is an https origin (a `Secure` cookie
never reaches an http one). The two OIDC cookies are `SameSite=Lax` on
purpose: the provider's redirect back is a cross-site top-level navigation, which
`Lax` admits and `Strict` would not. Both are sealed with AES-256-GCM under
distinct purposes, so a state cookie cannot be replayed as an external identity.

A persistent session cookie (`rememberMe` on the 2FA step) carries the standard
absolute lifetime as its `Max-Age`; the server still enforces the tighter
privileged bounds per request, whatever the cookie says.

## First Owner and local accounts

There is one deliberate, one-time bootstrap path, and it needs no secret: on a fresh
installation, visit `/setup` and provide the first Owner's email, display name and
password. That account becomes the installation's full administrator, holding both
`Owner` (every permission in every module) and `SystemAdmin` (`/admin` and
maintenance mode). No configuration names it, and no other path grants `SystemAdmin`.

**Complete setup before the installation is reachable by anyone else.** Whoever
submits `/setup` first owns the installation. While no Owner exists the process logs
a `WARN` at startup saying so; once the Owner exists, setup and bootstrap refuse every
further request.

`GET /api/v1/identity/bootstrap-status` is anonymous and returns
`{"available": true|false}`. `POST /api/v1/identity/bootstrap` remains available for
an orchestrated flow:

```bash
curl -X POST https://vantigo.example.com/api/v1/identity/bootstrap \
  -H 'Content-Type: application/json' \
  -d '{"email":"owner@example.com","displayName":"Owner","password":"a strong password"}'
```

Every change to who holds Owner — bootstrap, Owner creation, Owner invitation
acceptance and Owner demotion — serializes on a transaction advisory lock, so
concurrent bootstrap requests cannot both win. Setup and bootstrap become
unavailable once the first Owner exists. The bootstrap account is an ordinary local
password account, and a local Owner remains the break-glass path even when workforce
OIDC is enabled.

**`BOOTSTRAP_OWNER_EMAIL` replaces `/setup` with an emailed invitation**, for a
deployment that is reachable before an operator can sign in. While it is set and no
Owner exists, `/setup` and `POST /api/v1/identity/bootstrap` answer the same 409
`bootstrap_unavailable` a consumed bootstrap gets, and every start mails that
address an Owner invitation — once: a pending invitation is not re-sent before it
expires, and an expired one is re-issued on the next start. The invitation follows
the configured address, not a fixed one: changing `BOOTSTRAP_OWNER_EMAIL` while no
Owner exists revokes the invitation already issued to the previous address and
mails the new one on the next start, so a mistyped address does not keep the
installation's only live Owner token. An address some account already holds is
never invited — a warning is logged instead. A failed send revokes the invitation
and logs the failure without stopping startup; the next start retries. Whichever
account accepts the invitation becomes the installation's Owner and SystemAdmin,
exactly as `/setup` would create it.

The installation's first Owner holds `Owner` and `SystemAdmin`, and the bootstrap
marker is written, **however that account is created** — `/setup`'s anonymous form
and an accepted Owner invitation reach the same outcome.

Owners invite `User` or `Owner` accounts from `/settings`. Invitation tokens are
opaque and stored only as hashes. `INVITATION_LIFETIME` defaults to `168h` (seven
days) and is accepted from `24h` to `720h`; a value outside that range fails
startup.

Password recovery answers generically so it never discloses whether an address
exists, and password-policy detail is reported only after the token validates.

## Staff-managed users

A user is **Staff-managed** when identity marks them managed by Staff, with the
employee number that ties them to a person in the [Staff](/en/admin/staff/) register.
Staff's account provisioning may then enable, disable and update that user; it never
acts on a user who is not Staff-managed. The marking, the employee number and who
disabled a user are identity's own columns (`identity.users`): Staff reaches them only
through identity, never through its own tables.

`GET /api/v1/identity/owner/users` shows the state on each user: `pending` (always
sent: a user Staff created who has not chosen a credential yet, listed `disabled` and
not `active` until they do), and, only when set, `managedBy` (`staff`),
`employeeNumber` and `disabledBy` (`owner` or `staff`).

What an Owner may still do with a Staff-managed user:

- **Edit, reset the password or MFA, and enable** the user as any other — except that
  a **pending** user can neither be enabled nor given a password: both answer 409
  `account_pending`, since the account becomes usable only by accepting its invitation.
  Should a pending user hold a credential all the same, password, passkey and OIDC
  sign-in refuse them as they refuse a disabled account, and a session they already
  hold ends on its next request. No password is ever written to a pending user, by an
  Owner, a reset or the user: the write itself checks, so a user made pending while a
  password set waits still keeps none (409 `account_pending`; the user's own change
  answers 401 and ends no session). Password recovery sends a pending user no reset
  link, and answers as it does for any address.
- **Disable** the user. An Owner's disable is recorded as the Owner's
  (`disabledBy: owner`), also over a user Staff had disabled, and Staff never
  re-enables a user an Owner disabled. Enabling clears it. Overriding Staff's disable —
  disabling over it, or enabling a user Staff disabled — is also audited
  `user.disabled-by-changed`, with who had disabled the user before and after.
- **Promote** the user to Owner. Staff never manages an Owner, so the promotion stops
  Staff managing them in the same change, audited `user.staff-unmanaged` with the
  reason `promoted`. A pending user cannot be promoted (409 `account_pending`), and a
  pending user never counts as an active Owner, so disabling or demoting the last
  Owner who can sign in is refused `last_active_owner` whatever pending users hold.
- **Not delete** the user: the delete answers 409 `account_managed`. Stop Staff managing
  them first (below), then delete.

Users disabled before this version are recorded as disabled by an Owner, since only an
Owner could disable one.

A pending user's **invitation spell** begins when Staff creates them
(`identity.users.invitations_since`): the invitations Staff's provisioning delivers to
them since then count toward its three, and a fresh invitation (Staff's *Run now*) or a
new address before their first invitation was delivered starts another spell.

A pending user's **address follows the person's record in Staff only until the first
invitation naming the user has been delivered** (one whose delivery failed does not
count). From then on the address is the one the person was invited at: Staff's
provisioning no longer writes it, sends nothing to a new one, and reports the
difference (`accountEmailDiffers` in Staff) for an Owner, who changes the address on
the **Users** page — as for an active user.

### A pending user accepts their invitation

The invitation Staff's provisioning sends a pending user names that user
(`identity.invitations.user_id`). Accepting it completes the existing account instead
of creating one: under the owner lock, then the invitation's row lock, then the user's,
the user must still be the invitation's address, managed by Staff, pending, not
disabled (by an Owner or by Staff), neither an Owner nor a SystemAdmin, and hold no
password, passkey or OIDC link. The password must meet the policy; the user then has
it, a confirmed email, no pending mark and no lockout, keeps their display name (the one typed on the page is ignored) and the roles they
hold — an invitation naming a user grants no role and never makes the first Owner. The
audit records `invitation.accepted-completing-user`, and the user is signed in. Anything
else answers the same 400 `invitation_invalid` as an unknown or expired token, and
nothing is written.

The same conditions decide the rest of a named invitation's life. Validating its link
(`GET /api/v1/identity/invitations/validate`) answers `valid: false` once the user no
longer meets them, and names no role while they do, since the account keeps its own. A
resend keeps the user the invitation names, and is refused 409 `invitation_not_active`,
with nothing sent, once that user no longer meets them or is gone.

An Owner cannot invite a pending user's address by hand: the account exists, so the
invite answers 409 `account_exists`. A pending user's invitation is Staff's to send; an
Owner may resend one already sent.

### The audit's system actor

A change Staff's provisioning makes in identity is audited like an Owner's, in
`identity.authorization_audit_events`, but its actor is a system, not a user:
`actor_user_id` is empty, the `details` read
`{"action":"…","actor":"system:staff-accounts"}` (or `"system:staff-retention"` when the
retention worker erases the user), and there is no correlation id, since
no request made the change. Every other row's `details` are `{"action":"…"}` as before.

**After Staff erases a person.** Once Staff's retention worker has erased a former
employee's personnel administration (`erased_at`), it asks identity (`Erase`, actor
`system:staff-retention`, or the caller for a run by hand) to erase the disabled user
Staff managed for them: the user is deleted together with their sessions, credentials,
invitations, SCIM mapping, any unconsumed announcement (`identity.expected_accounts`) for
their employee number, and the delegations they granted (`created_by_user_id`), while
delegations granted to them stay with `delegate_user_id` cleared; the `actor_snapshot`
and `target_snapshot` of every row in `identity.authorization_audit_events` naming them
are pseudonymised in place (`displayName: "Erased user"`, `email` and `employeeNumber`
empty); and `user.staff-erased` is recorded without a target snapshot. A user HR linked
by hand (`managed = false`) is not Staff's and is left untouched.

### The authority mode

Staff's provisioning acts in one of two modes. **Local**: identity holds the
credentials, so it creates the users Staff asks for, enables them and invites them to
choose a password. **Federated**: an identity provider owns the accounts — workforce
OIDC or SCIM is on in the [single sign-on settings](/en/admin/sso-scim/#the-single-sign-on-card)
— so it only finds a user by
employee number, re-enables a user Staff disabled and disables; it never creates or
invites (see [SSO and SCIM operations](/en/admin/sso-scim/#staff-in-a-federated-installation)).

**Staff now decides per person.** Each person in Staff has an **account kind** —
`local`, `external` or `entra` — and every call Staff's provisioning makes carries it,
so the kind, not the installation's mode alone, decides what identity creates, gives
and invites ([below](#what-staffs-provisioning-does-in-identity)): a `local` person in
an installation whose single sign-on is on is still created and invited. The mode stays
for two things: a call that names no kind — from a Staff worker of the release before —
is judged by it (`local` from local, `external` from federated), and Staff seeds every
person's kind and its default from it once, when it first starts on this release.

The mode is derived from `identity.sso_settings` by every process — the API and the
workers alike — so none can disagree, and a worker needs no OIDC or SCIM settings of
its own. Turning SSO or SCIM on or off on the card changes the mode everywhere within
20 seconds, with no restart. The mode is also recorded, as the trail of its changes,
in `identity.system_settings.authority_mode` with `authority_mode_at`: by an Owner's
save on the card that changes it, in the save's own transaction, and at every API
start, in the transaction that holds the row for the import — the first after the
upgrade records the mode the
[imported settings](/en/admin/sso-scim/#before-the-card-the-environment-variables)
derive. A change is audited `authority-mode.changed` with the modes before and after,
no actor and no correlation id. A save or a start with the same mode changes
nothing.

The same row holds the **workspace's business time zone**
(`identity.system_settings.business_time_zone`, `Europe/Oslo` by default): the one
calendar every module judges a bookkeeping day in, read through the platform's
`BusinessCalendar` contract and set by an Owner with
`PUT /api/v1/identity/owner/workspace` or the **Workspace** card under
[Workspace administration](/en/user/workspace-administration/#workspace-settings). A
name the server cannot load, and a name the database reads differently from the
server (`CET`), are refused. A change is audited
`workspace.business_time_zone_changed` with the zone before and after, by the Owner
who made it. Before this setting Staff and Expenses each held a zone of their own;
an installation upgraded from there runs on `Europe/Oslo` until an Owner sets the
workspace's, and the migration log warns when either module's value was another.

Beside it, each user has a **personal time zone** (`identity.users.time_zone`): the
zone instants are shown to that user in, and a time they type is read in. It is
empty — "follow the workspace" — until the user picks one under **Personal settings**
([Getting started](/en/user/getting-started/#choose-your-time-zone)), or a client sends
`timeZone` to `PUT` or `PATCH /api/v1/identity/account/profile`. The name is held to the
same two checks as the workspace's, refused on `timeZone` with `400 invalid_request`
otherwise; `null` or a blank name empties it again, and a request that leaves
`timeZone` out keeps it. `GET /api/v1/identity/account` answers the stored choice
(`null` when the user follows the workspace), and `GET /api/v1/identity/session`
answers the zone in effect as `timeZone`, where it came from as `timeZoneSource`
(`personal` or `business`), and the workspace's as `businessTimeZone`. A personal zone
never moves a bookkeeping day; an empty one follows a later change of the workspace's
zone at once, since it is resolved when the session is read and not copied when the
user is created. It is a preference, so changing it is not audited, and no Owner or
directory sets it for the user: neither SCIM nor Staff's provisioning writes it.

### What Staff's provisioning does in identity

Staff reaches identity's users only through identity's provisioning, which runs each
change in a transaction of its own — under the owner lock, then the user's
invitations, then the user — audits it with its actor (the person who pressed a button
in Staff, or `system:staff-accounts`), and sends mail only once the change is
committed. It refuses anything with no valid actor, and acts by the **account kind
the call carries** — the person's in Staff — or, for a call that names none, by the
[authority mode](#the-authority-mode) the single sign-on settings derive, read in the
change's own transaction (`local` from local mode, `external` from federated). The
`external` kind is the federated mode's rules for one person; `entra` is the same
link-only path for a person whose account Staff has an external provider create and
[announces](#the-announced-account), with the employee roles given once.

| Step | `local` | `external` | `entra` | Audited |
| --- | --- | --- | --- | --- |
| Prepare | Finds the Staff-managed user with the employee number, else creates one: Staff-managed, pending, disabled by Staff, with the built-in `User` role, no credential and an unconfirmed address. An Owner's open invitation to that address is revoked `replaced` and named in the audit — but an open invitation to become an Owner keeps the address for its invitee. The answer says whether this call created the user (`Created`), so Staff never undoes a user it only found. | Finds the Staff-managed user with the number; creates nothing (`Not found` until an Owner adopts one) | Finds the Staff-managed user with the number — the one the announced account's arrival made; creates nothing (`Not found` until it has arrived) | `user.staff-prepared` |
| Activate | Enables a user Staff disabled — a prepared user among them — giving them the [employee roles](#the-employee-roles) on that call only, so a role an Owner removes later stays removed, and recording when (`provisioning_roles_at`); invites a pending user without a credential by name | Re-enables a user Staff disabled; gives no role and sends no invitation | Re-enables a user Staff disabled and, while `provisioning_roles_at` is empty, gives the employee roles and records it — once, enabled or not, never again; sends no invitation | `user.staff-activated` |
| Deactivate | Disables the user as Staff's (`disabledBy: staff`) — also one the directory already deactivated — ends their sessions and reset links, and revokes their open invitations `deactivated` | The same | The same | `user.staff-deactivated` |
| Update | Writes the display name, the language and the employee number; the address only while the user is pending and no invitation naming them has been delivered yet, which revokes their invitations `email_changed` and an Owner's open invitations to the new address `replaced` (named in the audit) | Writes the language and the employee number only | The same as `external` | `user.staff-updated` |
| Erase | Deletes the disabled Staff-managed user (with their sessions, credentials, invitations, SCIM mapping, any unconsumed announcement for the employee number and the delegations they granted, while delegations to them keep their row with `delegate_user_id` cleared) and pseudonymises the `actor_snapshot` and `target_snapshot` of the audit rows naming them (`displayName: "Erased user"`, `email` and `employeeNumber` empty) | The same | The same | `user.staff-erased` |
| Managed avatar (`SetManagedAvatar`) | Sets or clears the linked user's [managed avatar](#the-managed-avatar), HR's photo in front of the user's own — whether or not Staff manages the user | The same | The same | `user.avatar-managed`, `user.avatar-unmanaged` |
| Expect | — | — | Records or replaces the [announcement](#the-announced-account) for the employee number | `account.expected` |
| Unexpect | — | — | Withdraws an announcement nobody fulfilled | `account.unexpected` |

An activated pending user is enabled but still cannot sign in: password, passkey and
OIDC sign-in refuse them — OIDC's first sign-in by their address answers
`account_locked` and links nothing — until they accept their invitation. A user who has
accepted keeps the address identity holds; when Staff's record holds another, Staff
reports it for an Owner to change here.

Invitations follow the pending user's [invitation spell](#staff-managed-users): a
lapsed invitation is replaced at the next activation until three have been delivered;
then Staff reports it unanswered. An Owner's revoke of the invitation stands — Staff
does not send another — until someone runs the person's account in Staff by hand
(*Run now*), which starts a new spell. *Run now* never re-sends an invitation that can
still be accepted. No invitation is sent within ten minutes of the last one naming the
user — whether by *Run now*, after an address change or on the next scheduled run — so
pressing *Run now* again and again, or changing an address back and forth, sends at
most one invitation every ten minutes. A mail that cannot be sent revokes its
invitation `delivery_failed` and does not count; the next activation tries again.

Staff's provisioning never touches an Owner or a SystemAdmin, never re-enables a user
an Owner disabled and never deactivates the last active Owner. A leaver loses access
whatever the directory says: Staff disables them in identity even when the directory
already reports them inactive (SCIM never disables a user in identity, and its
deactivation counts only while SCIM is configured), so a directory that reactivates
them later, or SCIM turned off, does not let them back in. A Staff deactivation removes no role, so
a rehire keeps the roles an Owner gave the user earlier as well as the employee roles;
an Owner removes roles by hand when that is not wanted. A rehire re-enables what
Staff disabled; while the directory still reports the person inactive, it keeps them
out and Staff reports their account disabled by the directory. What it refuses:

| Refusal | Meaning | What to do |
| --- | --- | --- |
| Not managed | The user is not Staff-managed | Adopt them, or leave them to an Owner |
| Privileged | The user is an Owner or a SystemAdmin (refused for every step, an update included). Identity reports this when Staff reads the account, so Staff reports it without trying, also for a managed user since promoted to Owner | Handle the account by hand |
| Blocked by an Owner | An Owner disabled the user | Enable them here if Staff should manage them again |
| Account enabled | `Erase`: the user is still enabled | Disable the user first |
| Employee role missing | No employee role is named | Name the [employee roles](#the-employee-roles) |
| Employee role unsafe | A role in the set carries a permission that grants administration — possible only by a change outside identity's checks | Take the permission off the role, or the role out of the set |
| Address taken | Another user holds the person's address, or an Owner has invited someone to become an Owner at it and the invitation is still open | Give the person another address in Staff, or resolve the other account or the invitation |
| Employee number taken | A user Staff does not manage holds the number | Adopt that user, or correct the number in Staff |
| Not found | `external` or `entra`: no Staff-managed user holds the number yet | `external`: let the identity provider create the account, then adopt it; `entra`: the account arrives by SCIM or the person's first sign-in and is adopted by its announcement |
| Avatar invalid | `SetManagedAvatar`: the picture is not a PNG or a JPEG, is over 5 MiB or 4096 px a side, or its source is not `staff` | Staff's photos always pass; a refusal is a fault to report |
| Mode mismatch | Reported only by a worker still on the release before the Single sign-on card, during a rolling upgrade (refused for every step but deactivation) | Finish the upgrade |

### The announced account

For a person of the `entra` kind, Staff has the external provider create the account
and then **announces** it to identity (`Expect`), so the user that arrives — by the
provider's SCIM push, or at the person's first OIDC sign-in — is adopted for Staff on
arrival, managed, numbered and not pending, with no Owner in between. An announcement
is kept in `identity.expected_accounts`, one per employee number (a new one replaces
it): the employee number, the display name, the language, the issuer (identity's own
from the single sign-on settings — Staff leaves it empty), the provider's object id of
the account, its user principal name (stored normalised as addresses are) and, at
most, a user it replaces.

**The match, three keys in order.** A SCIM create of a User is matched first on the
[enterprise extension's](#scim) `employeeNumber`, by the number's key; then on
`userName` against the announced principal name; then on `externalId` against the
announced object id. An OIDC first sign-in is matched on the `oid` claim against the
object id, under the announced issuer. The number comes first because the provider
writes it as the account's `employeeId`, which Entra's default SCIM mapping sends as
`employeeNumber`; the principal name second, since Entra sends `userName` from it by
default; and the object id last for SCIM, because Entra's default mapping sends the
**mail nickname**, not the object id, as `externalId` — it matches only when the Owner
maps `objectId` there — while OIDC always carries `oid`, which identity already
requires.

**What an arrival does.** Under the owner lock, in the arrival's own transaction:

- **A match** that nobody else's user holds the number for creates the user as SCIM or
  OIDC would, then marks them managed by Staff with the announced employee number, not
  pending (the credential is the provider's), in the announced language. A SCIM user
  takes the announced display name only when the resource carries no display name and
  no name part; an OIDC user is named by the claims, as before. The announcement is
  consumed, audited `account.adopted-from-provider` with `matchedBy`
  (`employeeNumber`, `userName`, `externalId` or `oid`) and the employee number — never
  an address — beside SCIM's own `scim.user.created`. An adopted OIDC user is then
  signed in by the linked account's rules, so a user Staff keeps disabled is
  `account_locked` until Staff enables them.
- **A match naming a user to replace** (`ReplaceUserID`: the person was `local` with a
  pending, credential-less user when HR changed their kind to `entra`) completes that
  user instead of inserting a second — as long as that user still holds the number and
  is still managed, unprivileged, pending and without a credential; otherwise the
  arrival is no adoption. The user is locked, their open invitations revoked
  `replaced`, `pending_since` and the invitation spell cleared, and the SCIM mapping or
  the OIDC link attached to them, their id, display name and disabled state kept;
  `account.adopted-from-provider` names `replacedUserId`. Their address becomes the
  arrival's: for SCIM the resource's **work email** (none is `InvalidEmail`, another
  user's `DuplicateEmail`, and a `userName` another mapping has is `uniqueness`); for
  OIDC the email claim when there is one, else the address is kept. For OIDC, the email
  claim colliding with that very user is the completion, not `oidc_email_conflict`;
  with anyone else it is refused as before.
- **No match** is what it was: a plain SCIM or OIDC user, whom an Owner adopts by hand
  if they are Staff's. The enterprise extension's `employeeNumber` is kept in the SCIM
  profile and **never** becomes the user's employee number: only Staff's provisioning
  and an Owner's adoption make that claim.

**What `Expect` refuses.** It reads no mode, and it judges the announcement first: an
employee number in Staff's alphabet, a display name of 1 to 200 characters, the
language `en`, `nb` or none, an issuer and an object id of at most 200 characters, a
principal name that is an address of at most 254 — and at least one of the object id
and the principal name, the keys an arrival can carry; anything else is an invalid
announcement. Then the user holding the number decides: an unmanaged user is
`ErrEmployeeNumberTaken`; a managed Owner or SystemAdmin `ErrAccountPrivileged`; a
managed user who is not pending has arrived already, or an Owner adopted them: nothing
is recorded and the call succeeds, so Staff's next `Prepare` finds them. A managed,
pending user without a credential is recorded only when the announcement names that
very user to replace. A user to replace named when nobody holds the number, or who is
not that pending, credential-less holder, is an invalid announcement. The same
announcement again changes nothing; any change replaces it whole, audited
`account.expected` with the number, the names of its keys and the user it replaces. An
announcement that records a user to replace revokes that user's open invitations
`replaced` in the same transaction — the person cannot accept a local invitation while
the provider's account is on its way — and the audit's after counts them
(`replacedInvitations`).
Staff records `ErrEmployeeNumberTaken` and `ErrAccountPrivileged` on the person's
provider link and retries an invalid announcement as a failure. `Unexpect` deletes an
announcement and is audited `account.unexpected` when one went; with none — never
announced, or fulfilled already, which consumed it — it succeeds and writes nothing.

**The roles, once.** An adopted user arrives enabled, so no `Activate` ever enables
them; `identity.users.provisioning_roles_at` records when Staff's `Activate` gave a
user the employee roles. For the `entra` kind the first `Activate` that finds it empty
gives them and sets it — once: an Owner's later removal of a role is not undone. The
`local` path sets it too when it gives them. Migration 00059 sets it, on the upgrade,
for every Staff-managed user who is not pending: the previous release's `Activate` or
an Owner's adoption gave them their roles already, so the once-only grant is spent for
them and a later `entra` activation gives nothing back over what an Owner has removed.
A pending user keeps it empty.

An announcement nobody fulfils stays until Staff withdraws it; after ninety days Staff
reports the person's account as not arrived (`accountNotArrived`). The Owner's manual
[adoption](#adopt-an-existing-user-for-staff) stays for the `external` kind and for any
user the provider made without an announcement. Migration
`00059_identity_expected_accounts.sql` adds the table and `provisioning_roles_at`
(filled for the managed users who are not pending, as above); its
Down drops both, and an unfulfilled announcement is made again after the provider's
next `Prepare`.

### Adopt an existing user for Staff

Adoption brings a user who already exists — a colleague with an account before Staff's
accounts were turned on — under Staff's management.

Preconditions: you are an Owner, signed in with MFA when `OWNERS_REQUIRE_MFA` is on;
the person exists in Staff's register with an employee number; the user is neither an
Owner nor a SystemAdmin.

1. Find the user's id in `GET /api/v1/identity/owner/users`.
2. Adopt them with the person's employee number:

   ```bash
   curl -X POST https://vantigo.example.com/api/v1/identity/owner/users/<id>/manage \
     -H 'Content-Type: application/json' \
     -d '{"module":"staff","employeeNumber":"1042"}'
   ```

3. Verify: the answer, and the user in the list, show `managedBy: staff` and the
   employee number. The audit records `user.staff-managed`.

An employee number is Staff's own alphabet: once trimmed, 1 to 16 characters, each a
letter `a`–`z` in either case, a digit or a hyphen — what the a-melding, a payroll file
and Entra's `employeeId` carry without escaping, and what Staff's register itself
holds. Numbers are compared by a key identity computes and stores beside the number
(`identity.users.employee_number_key`), the number upper-cased, so `a-17` and `A-17`
are one number and `E-1` and `E-01` are two. The database's collation plays no part.
Adopting a managed user again with another number
changes the number. A user SCIM
provisions may be adopted: in a federated installation the managed user is the
identity provider's.

What can go wrong:

| Answer | Meaning |
| --- | --- |
| 409 `account_privileged` | The user is an Owner or a SystemAdmin; Staff never manages them, and an Owner handles Owners by hand |
| 409 `employee_number_taken` | Another user already holds the number, however it is cased |
| 400 `invalid_request` | The module is not `staff`, or the number is blank, longer than 16 characters or holds anything but `a`–`z`, `A`–`Z`, digits and hyphens — a space inside it, an underscore, a letter such as `Å` or `ı`, a full-width digit, an invisible or control character — or the user is you |

### Stop Staff managing a user

`POST /api/v1/identity/owner/users/<id>/unmanage` clears the marking and the employee
number, audited `user.staff-unmanaged`. A user Staff does not manage is left as it is.
A pending user stays pending: finishing or deleting that user is then the Owner's.
The person Staff linked the user to **loses their My page** (Staff's Self view) until HR
links the user to them again by hand on the person's page in Staff, which makes it an
ordinary self-service link Staff never acts on.

### Connect an existing user to the provider

When single sign-on is turned on in an installation that already has users, an
address never links a provider sign-in to an existing account, so their first attempt is
refused `oidc_email_conflict`. An Owner **announces** that the user holds a directory
account; the user's next OIDC sign-in (matched on `oid` under the configured issuer, else on the token's principal name, Entra's `preferred_username`, or the verified `email` claim for a provider without one)
or SCIM push (matched on `userName` against the principal name, then `externalId`
against the object id) then links or maps to that user. How the arrival is matched is
described with [the announced link](/en/admin/sso-scim/#the-announced-link).

Preconditions: you are an Owner, signed in with MFA when `OWNERS_REQUIRE_MFA` is on;
the workforce OIDC provider is on; the user is not you, is not pending and has no link
under the configured issuer.

1. Find the user's id in `GET /api/v1/identity/owner/users`. The user's `ssoLink` says
   `state: none`.
2. Announce the link, with the user principal name, the object id, or both:

   ```bash
   curl -X POST https://vantigo.example.com/api/v1/identity/owner/users/<id>/sso-link \
     -H 'Content-Type: application/json' \
     -d '{"userPrincipalName":"kari@example.com","objectId":"3f2c1a40-8b1e-4c52-9d3a-6e7f5a1b2c3d"}'
   ```

3. Verify: the answer is the user, with `ssoLink` of `state: expected`, the keys and
   `expectedAt`. After the person has signed in, or the provider has pushed the
   account, it reads `state: linked`, and the audit records
   `account.linked-from-provider`.

The user principal name is stored normalised as addresses are (at most 254 characters);
the object id as SCIM's `externalId` is, a GUID in lower-case dashed form (at most 200
characters, no control character). A SCIM arrival replaces the user's address with the resource's work email and leaves
`email_confirmed` as it was; a user who was an Owner keeps their role. The same
announcement again changes nothing; any
change replaces it whole. A directory account is announced for at most one user. The
user's email address plays no part in the match, and the arrival's email claim is not
consulted. `ssoLink` is absent from the answers while OIDC is off.

What can go wrong, in the order the endpoint judges it:

| Answer | Meaning |
| --- | --- |
| 400 `invalid_request` | Neither key is given; the principal name is not an address or is longer than 254; the object id is longer than 200 or holds a control character; or the user is you (connect your own account by signing in) |
| 404 | No such user |
| 409 `sso_not_configured` | OIDC is off, so there is no issuer to announce under |
| 409 `account_pending` | The user is pending: their way in is the invitation, or the announced account that replaces them |
| 409 `account_linked` | The user already has a link under the configured issuer; disconnect them first |
| 409 `sso_link_taken` | The object id under the issuer, or the principal name, is announced for another user, or named by Staff's [announced account](#the-announced-account) |

A disabled user may be announced: the arrival signs them in by a linked account's
rules, `account_locked` until they are enabled. An Owner or a SystemAdmin may be
announced, and the local MFA rule stands. An announced link outranks an announced
account for the same arrival, and an arrival never fulfils both.

### Disconnect a user

`DELETE /api/v1/identity/owner/users/<id>/sso-link` withdraws the user's announced link
and removes every OIDC link of the user under the configured issuer, in one
transaction, and answers the user.

```bash
curl -X DELETE https://vantigo.example.com/api/v1/identity/owner/users/<id>/sso-link
```

Nothing to remove is `200` with the user unchanged. The user keeps their account; a
user left without a credential signs in again only after a password reset or a new
announcement. The SCIM mapping is **not** touched: it is the provider's record and ends
with the provider's delete. The refusals are `400` for your own account, `404` for no such user
and `409 sso_not_configured` while OIDC is off.

The audit, in `identity.authorization_audit_events`, records the four events of the
announced link, none with an address:

| Event | When | Detail |
| --- | --- | --- |
| `account.link-expected` | An announcement is recorded or changed | `keys`: the names of the keys given (`objectId`, `userPrincipalName`) |
| `account.link-unexpected` | The `DELETE` withdrew an announcement | — |
| `account.unlinked` | The `DELETE` removed OIDC links | `links`: how many |
| `account.linked-from-provider` | An arrival fulfilled the announcement | `matchedBy`: `oid`, `userName` or `externalId` |

Migration `00064_identity_expected_links.sql` adds `identity.expected_links`; its Down
drops the table, and an unfulfilled announcement is made again by an Owner.

### The employee roles

The built-in `User` role carries no permissions, so the users Staff provisions get the
**roles Staff may give**: custom roles an Owner names, typically one "Employee" role
with `staff:access`, `time:access` and `expenses:access`. Identity holds the set, and
only an Owner (with MFA when required) changes it, so `staff:manage` can never give an
account a role it chooses.

1. Create the role under the workspace's roles, if it does not exist.
2. Name the set, whole:

   ```bash
   curl -X PUT https://vantigo.example.com/api/v1/identity/owner/provisioning-roles \
     -H 'Content-Type: application/json' \
     -d '{"roleIds":["<role id>"]}'
   ```

3. Verify: `GET /api/v1/identity/owner/provisioning-roles` lists the roles by name, each
   with its permission keys. The audit records `provisioning-roles.replaced` with the
   set before and after.

The request replaces the set; an empty `roleIds` empties it. Deleting a role removes it
from the set. 400 `role_not_assignable` refuses, and nothing changes, for:

- a built-in, system or unknown role;
- a role carrying a permission that grants administration — `identity:manage` and any
  other permission that cannot be delegated, `staff:manage`, `staff:personal-data` or
  `staff:approve`. The message names the role and the key.

**Caution:** a role in the set is given to every user Staff provisions, and editing
that role's permissions later changes what all of them hold at once. Give the role only
what every employee needs, and review the set before widening one of its roles. An edit
that would give a role in the set one of the permissions above — by an Owner or by a
delegate stewarding the role, through `PUT /api/v1/identity/access/roles/{id}` or its
`/permissions` — is refused 403 `role_provisioning_protected`, and nothing changes;
take the role out of the set first; a caller with no authority over the role is
refused for that first (`role_edit_denied`). An edit of a role waits while the set is
being named with that role, and naming the set while one of its roles is being edited
waits for the edit and then judges the role as the edit left it — so a role widened
with one of those permissions meanwhile is refused `role_not_assignable`.

### Why an invitation was revoked

Every revoked invitation records why (`identity.invitations.revoked_reason`):

| Reason | Revoked by |
| --- | --- |
| `owner` | An Owner's revoke; also every invitation revoked before this version |
| `replaced` | A new invitation to the same address (an invite or a resend), an account created for the address, or a corrected `BOOTSTRAP_OWNER_EMAIL` |
| `delivery_failed` | The invitation's mail could not be sent |
| `email_changed` | An Owner changed a user's email to, or from, the invitation's address; or Staff's provisioning gave the pending user it named another address |
| `deactivated` | Staff's provisioning deactivated the user it named |

The Owner's invitation list (`GET /api/v1/identity/owner/invitations`, and the revoke
and resend answers) shows the reason as `revokedReason` on each revoked invitation, and
leaves it out on one that is not revoked.

### Who holds a permission

Identity answers other modules who holds a permission (for instance Staff, to know who
can decide an absence). A user holds it through a role assigned to them directly,
through a role mapped to an active access group they are a member of (forced in, or
present in the directory and not overridden out — the rule every permission check
applies), or by being an Owner, who holds every permission. An authorization
delegation lets its grantee manage roles; it grants no permission of its own. Only
active users count: a disabled user, a pending user who has not yet accepted their
invitation, and — while SCIM is configured — a user the directory deactivated, unless
an Owner, hold nothing. A lockout is temporary and does not change who holds a
permission. To make someone a holder, give them a role carrying the permission, or map
one to a group they belong to.

### The managed avatar

When HR sets a person's photo in [Staff](/en/reference/staff/#the-photo), Staff's
provisioning makes it the linked user's **managed avatar** (`SetManagedAvatar`): a second
picture, kept in `identity.managed_avatars` beside the user's own avatar
(`identity.profile_avatars`), never over it. The **effective avatar** is the managed one
while it stands, else the user's own. It is what `GET /api/v1/identity/account/avatar`,
an Owner's `GET /api/v1/identity/owner/users/{id}/avatar`, the user list's avatar and
the reads other modules make (`UserDirectory.Avatar`, and `AvatarInfo` — the stamp and
the time without the bytes) all answer. When HR removes the photo, Staff clears the
managed avatar and the user's own picture shows again, with nobody uploading it anew.

- **Who sets it.** Only Staff, through the contract — there is no endpoint for it — with
  the HR user who saved the photo, or `system:staff-accounts` when the accounts worker
  puts it right. It applies to any user a person's self-service link names, whether or
  not Staff manages the account. The source must be `staff`, and the image is judged as
  an upload is — a PNG or a JPEG whose bytes are what it declares, at most 5 MiB and
  4096 px a side — or the call is refused (*Avatar invalid*). The same picture again
  (the same stamp, type and bytes), or a clear with none standing, writes nothing and
  audits nothing.
- **Never set for an Owner or a SystemAdmin.** A set is refused *Privileged*: the people
  who run the installation keep their own picture, and Staff logs it and raises nothing.
  A clear is always allowed, so a user promoted after HR set a photo is freed of it — the
  accounts worker clears it at the person's next run.
- **The user's own avatar while it stands.** `POST`, `PUT` and `DELETE
  /api/v1/identity/account/avatar` answer **409** `avatar_managed`, *Your photo is kept by
  Staff and cannot be changed here.*, judged before an upload's body is read; the user's
  own avatar is left as it is. `GET /api/v1/identity/account`, and the profile writes'
  answers, carry **`avatarManagedBy`** — `staff` while the managed avatar stands, null
  otherwise — and `avatarUrl` points to the effective avatar. **Settings › Profile**
  then disables the photo controls with *Your photo is kept by Staff. Ask HR to change
  it.*
- **The stamp.** The contract's read answers which picture stands: `managed:<stamp>`,
  Staff's own stamp for its photo, or `own:<version>@<epoch>` for the user's own — the
  avatar's version and when it was stored, so an avatar removed and uploaded again never
  repeats a stamp. Staff compares it to know when to write the directory's photo.
- **The audit.** A set is `user.avatar-managed`, a clear `user.avatar-unmanaged`, with
  the actor and the target user; before and after record the source, the stamp, the
  content type and the size in bytes — never the bytes.
- **Gone with the user.** The managed avatar is deleted with the user
  (`ON DELETE CASCADE`), by an Owner's delete or Staff's erase.

Migration `00070_identity_managed_avatars.sql` adds `identity.managed_avatars` — one row
per user: the bytes, the type (`image/jpeg` or `image/png`, the CHECK `profile_avatars`
has), `source`, `source_stamp`, a `version` bumped on every write, and `updated_at`. Its
Down drops the table: every user's own avatar is their picture again, and once the
release is back Staff's accounts worker sets HR's photos anew at each person's next run.

## MFA

MFA is a six-digit TOTP authenticator plus one-use recovery codes, and passkeys
(WebAuthn) are a first-class second factor: a sign-in that verified a TOTP code, a
recovery code **or** a passkey is recorded as MFA-verified for that session.

`OWNERS_REQUIRE_MFA` requires MFA for the Owner and SystemAdmin policies. It
defaults **on outside development and off in development**. Outside development the
process refuses to start unless it is on or `OWNERS_ALLOW_INSECURE_NO_MFA=1`
deliberately accepts running without it — configuration reports:

```text
OWNERS_REQUIRE_MFA: must not be 0 outside development unless OWNERS_ALLOW_INSECURE_NO_MFA=1
(accepts that a compromised Owner or SystemAdmin password alone reaches every identity
and tenant control-plane endpoint)
```

The opt-out exists for demo deployments; set it deliberately, not by default. It takes
both keys: `OWNERS_REQUIRE_MFA=0` turns the requirement off, and
`OWNERS_ALLOW_INSECURE_NO_MFA=1` lets the process start with it off. The acknowledgement
alone changes nothing — the requirement stays on, and an administrator without an
enrolled authenticator is refused by every module and control-plane endpoint.
`MFA_ISSUER` (default `Vantigo`) is the label authenticator apps show.

MFA enrollment and status stay reachable to a session that has not enrolled yet, so
turning the requirement on never locks out the account that must enable it. Such a
session carries `mfaEnrollmentRequired: true` (GET `/session`, the sign-in and
invitation responses, GET `/account/mfa`) whenever the account holds Owner or
SystemAdmin, the requirement is on, and no authenticator is enrolled. The frontend
holds that session at `/settings/security`: every other signed-in page redirects
there, the page explains what to do, and enabling the authenticator marks the
session MFA-verified so access returns without a new sign-in.
Recovery codes are shown once. An MFA-authenticated Owner can reset another Owner's
MFA, which ends that account's sessions and requires re-enrollment.

## Session lifetime and revocation

Four bounds are enforced on every request. A privileged bound may not exceed its
standard counterpart, or startup fails.

| Key | Default | Meaning |
| --- | --- | --- |
| `SESSION_IDLE_TIMEOUT` | `8h` | Idle window for a standard session |
| `SESSION_PRIVILEGED_IDLE_TIMEOUT` | `2h` | Idle window for an Owner or SystemAdmin session |
| `SESSION_ABSOLUTE_LIFETIME` | `24h` | Hard cap on a standard session, from sign-in |
| `SESSION_PRIVILEGED_ABSOLUTE_LIFETIME` | `8h` | Hard cap on an Owner or SystemAdmin session |

The idle window slides, but the write is throttled: `last_seen_at` is rewritten only
once it is older than `min(idle/4, 5 minutes)`, not on every request. Presenting
credentials again is the only thing that starts a new absolute lifetime.

**Revocation takes effect on the very next request.** Every request resolves the
session cookie to a row in `identity.sessions` and evaluates the operation's access
rule against that row and the user's current roles — there is no cached revocation
state and no security-stamp convergence window, so a revocation, a disable or a
role change applies immediately across every replica.

- `POST /api/v1/identity/account/sessions/revoke` signs the calling account out
  everywhere, including the browser that made the request.
- `POST /api/v1/identity/system/users/{userId}/sessions/revoke` does the same for
  any account and requires SystemAdmin.

Each sign-in also purges that user's dead sessions (revoked, or past the standard
absolute lifetime), so the table stays bounded by sessions that could still be valid.

The same holds for a read made on a user's behalf with no session behind it — today a
person's calendar address, which a phone reads with a token instead of a cookie.
Identity answers it through `UserHolds`, whose person checks are one query,
`UserMayAct`, applying the session query's own: not disabled, not pending, and while SCIM
is configured not deactivated by the directory unless an Owner — and not locked out, as
every permission check requires. The two queries are kept in step, so a disable, a
lockout, a deactivation in the directory or a role change stops such a read at its very
next request, as it stops a session ([Calendar → the feed](/en/admin/calendar/#the-feed-on-the-public-host)).

## Rate limits

Authentication endpoints are rate limited by a fixed-window limiter whose counters
live in PostgreSQL (`platform.rate_limit`), so a limit holds **across replicas and
restarts** rather than per process. The limit is applied before the access check.

| Policy | Limit | Applies to |
| --- | --- | --- |
| `Login` | 100 / minute | `POST /login` |
| `login-attempts` | 10 / minute | Failed passwords for one email from one address, keyed `EMAIL\|ip`; answers without `Retry-After` |
| `Bootstrap` | 20 / minute | `POST /bootstrap` |
| `Mfa` | 20 / 5 minutes | The 2FA step, MFA management and passkey registration |
| `PasskeyLogin` | 30 / 5 minutes | Passkey sign-in |
| `PasswordRecovery` | 10 / 15 minutes | Recovery request, reset and account password change |
| `Invitations` | 30 / minute | Owner invitation management |
| `InvitationAcceptance` | 20 / minute | Invitation validate and accept |
| `UserManagement` | 30 / minute | Owner user management, adoption for Staff and the employee roles |
| `OwnerAvatarRead` | 300 / minute | Owner avatar reads |

Every policy except `login-attempts` keys on the client address, which is why
`TRUSTED_PROXY_HOPS` and `TRUSTED_PROXY_CIDRS` matter behind a proxy: get them
wrong and every request shares one bucket, or a client picks its own.

## Invitation and recovery URLs

The frontend routes are `/invitations/accept?token=…`, `/forgot-password` and
`/password-reset?email=…&token=…`.

Mailed links are derived from `APP_URL` plus `APP_BASE_PATH` by default, so setting
the public origin is usually all that is needed:

```text
APP_URL=https://vantigo.example.com
# yields https://vantigo.example.com/invitations/accept?token=...
# and    https://vantigo.example.com/password-reset?email=...&token=...
```

When the mailed links must differ, set the templates explicitly. Each must contain
its placeholders literally or startup fails:

```text
INVITATION_ACCEPT_URL=https://vantigo.example.com/invitations/accept?token={token}
PASSWORD_RESET_URL=https://vantigo.example.com/password-reset?email={email}&token={token}
```

`APP_URL` must be an origin only — scheme, host and optional port, no path, query or
credentials; a path prefix belongs in `APP_BASE_PATH`. `{email}` and `{token}` are
URL-encoded by the server. Do not put tokens in source control.

## Email delivery

`MAIL_DRIVER=log` writes mail to the application log instead of sending it. It
defaults to `log` in development and `smtp` everywhere else, and is **rejected
outside development**: invitation and password-reset mails carry bearer links, so
logging them anywhere else is a leak, not a feature.

For production:

```text
MAIL_DRIVER=smtp
SMTP_HOST=smtp.example.com
SMTP_PORT=587
SMTP_FROM=no-reply@vantigo.example.com
SMTP_USERNAME=<from-secret-store>
SMTP_PASSWORD=<from-secret-store>
SMTP_TLS=starttls
```

`SMTP_HOST` and a valid `SMTP_FROM` address are required for the SMTP driver.
`SMTP_TLS` is `starttls` (the default), `implicit`, or `none` — the operator's
choice; see [transport security](/en/admin/transport-security/) for what `none` costs.
STARTTLS is mandatory rather than opportunistic — a server that offers no TLS
produces an error, not a plaintext delivery. `SMTP_USERNAME` is optional for servers
that need no authentication.

SMTP destinations are resolved and checked before the socket opens: private,
loopback, link-local, carrier-grade-NAT and cloud-metadata addresses are refused,
and the connection is made to the address that was checked rather than a fresh
lookup. See [transport security](/en/admin/transport-security/) for the rule and its escape
hatch. A destination reached only through DNS64 on the local-use prefix
`64:ff9b:1::/48` is refused outright (there is no single fixed offset to decode
an embedded address from); a resolver on the well-known `64:ff9b::/96` prefix
works as normal.

Invoices sends through the same configuration: a document e-mailed to a customer goes
out from `SMTP_FROM`, under the seller's name and with the seller's e-mail as Reply-To,
through this SMTP server and its guard ([Invoices](/en/reference/invoices/#sending-a-document)).
Under `MAIL_DRIVER=log` nothing can be sent that way: the send answers 503
`mail_unavailable`, and `GET /invoices/meta` answers `mailAvailable: false`.

Calendar sends through it too. An invitation to someone outside the company goes out
from `SMTP_FROM` under the organiser's name, with `SMTP_FROM` as the invitation's
organiser, and a colleague on an appointment is mailed a notice by identity at the
address identity holds; under any driver but `smtp` no invitation is sent and
`GET /calendar/meta` answers `invitationsAvailable: false`. An invitation that Gmail is
to put on the invitee's calendar needs `SMTP_FROM`'s domain to pass SPF, DKIM and DMARC
([Calendar](/en/admin/calendar/#invitations-to-people-outside-the-company)).

This is identity's application mail, and Invoices' and Calendar's. Communications' per-channel
mailbox credentials are a different thing entirely — configured through the
Communications API and sealed at rest, never environment variables. See
[Communications](/en/reference/communications/).

## Key material

`APP_SECRET` is the process-wide key material: at least 32 bytes, from which every
key the process uses is derived with HKDF-SHA256 — one derived key per purpose —
and used as AES-256-GCM (CSRF and cookie sealing, OIDC state, TOTP secret
encryption, the OIDC client secret, Communications channel passwords). The purpose
string is bound in as additional authenticated data, so a value sealed for one purpose
cannot be opened under another, and a key-id byte leaves room for a future rotation
scheme.

**There is no persisted key ring, no key-wrapping service and no external key
vault.** Nothing needs provisioning beyond the variable itself. Two consequences
matter operationally:

- **All replicas must share the same `APP_SECRET`** (and the same database), or
  sessions issued by one replica are unreadable by another.
- **Losing or rotating it is equivalent to losing a signing key**: every open
  session, every OIDC flow in progress, every stored TOTP secret, the sealed OIDC
  client secret and every sealed channel credential becomes unrecoverable. Rotate
  only with a migration plan, and save the client secret on the Single sign-on card
  again afterwards.

## Configuration reference

`config.go`'s field comments remain authoritative; this table is the operator-facing
summary. Booleans are strict `0`/`1` switches — anything else fails startup.

### Application

| Variable | Purpose | Default |
| --- | --- | --- |
| `APP_ENV` | `production` or `development`; only development relaxes anything | `production` |
| `APP_URL` | Public origin (scheme + host [+ port]); no path | **required** |
| `APP_BASE_PATH` | Path prefix on a shared domain, e.g. `/vantigo` | empty (domain root) |
| `PUBLIC_BASE_URL` | The `https` URL the Invoices pay page and the Calendar answer page are reachable at from the internet: an origin and `APP_BASE_PATH`, nothing else, its host name ASCII (punycode for an internationalised one) with no trailing dot, and never `APP_URL`'s host name on another port; on a host of its own only the public host's allowlist is served there. Unset, there are no pay links, and an invitation's answer link and a person's calendar address (the feed) are on `APP_URL` ([The public host](/en/admin/payments/#the-public-host), [Calendar](/en/admin/calendar/#preconditions), [the feed](/en/admin/calendar/#the-feed-on-the-public-host)) | unset |
| `PORT` | Listen port | `8080` |
| `DATABASE_URL` | Runtime connection, the least-privilege role | **required** |
| `MIGRATIONS_DATABASE_URL` | Connection `migrate` uses (the owner role) | `DATABASE_URL` |
| `SHUTDOWN_TIMEOUT` | Drain budget for in-flight requests and workers | `30s` |
| `LOG_LEVEL` | `debug`, `info`, `warn`, `error` | `info` |
| `MODULES` | Comma list of business modules to serve; `planning` requires `projects` and `staff`, and `entra` requires `staff` ([Microsoft Entra ID](/en/admin/entra/), which has no variable of its own: its settings are in the database) | `customers,products,energy,communications,projects,time,expenses,invoices,staff,planning,calendar,entra` |
| `WORKERS_IN_PROCESS` | Whether `api` also runs background workers in-process | `1` |

### Secrets and identity

| Variable | Purpose | Default |
| --- | --- | --- |
| `APP_SECRET` | Process-wide key material, ≥ 32 bytes | **required** |
| `OWNERS_REQUIRE_MFA` | Require MFA for Owner and SystemAdmin | on outside development |
| `OWNERS_ALLOW_INSECURE_NO_MFA` | Lets the process start with the above set to `0`; does not turn it off by itself | `0` |
| `MFA_ISSUER` | Authenticator app label | `Vantigo` |
| `SESSION_IDLE_TIMEOUT` | Standard idle window | `8h` |
| `SESSION_PRIVILEGED_IDLE_TIMEOUT` | Privileged idle window | `2h` |
| `SESSION_ABSOLUTE_LIFETIME` | Standard absolute cap | `24h` |
| `SESSION_PRIVILEGED_ABSOLUTE_LIFETIME` | Privileged absolute cap | `8h` |
| `INVITATION_LIFETIME` | Invitation validity, `24h`–`720h` | `168h` |
| `INVITATION_ACCEPT_URL` | Template containing `{token}` | derived from `APP_URL` + `APP_BASE_PATH` |
| `PASSWORD_RESET_URL` | Template containing `{email}` and `{token}` | derived from `APP_URL` + `APP_BASE_PATH` |
| `BOOTSTRAP_OWNER_EMAIL` | Closes `/setup`; seats the first Owner by emailed invitation instead | unset (`/setup` open) |
| `OIDC_WORKLOAD_IDENTITY_TOKEN_DIR` | The directory a workload identity token file saved on the **Single sign-on** card must lie in, an absolute path: the deployment, not the Owner, bounds what the server stats and reads on the card's word ([Workload identity](/en/admin/sso-scim/#microsoft-entra-id-workload-identity)) | the directory of `OIDC_WORKLOAD_IDENTITY_TOKEN_FILE`, else of `AZURE_FEDERATED_TOKEN_FILE`, else `/var/run/secrets/azure/tokens` |

### Mail

| Variable | Purpose | Default |
| --- | --- | --- |
| `MAIL_DRIVER` | `smtp`, or `log` (development only) | `log` in development, else `smtp` |
| `SMTP_HOST` | SMTP server | required for `smtp` |
| `SMTP_PORT` | SMTP port | `587` |
| `SMTP_FROM` | Sender mailbox | required for `smtp` |
| `SMTP_USERNAME` / `SMTP_PASSWORD` | Optional credentials | unset |
| `SMTP_TLS` | `starttls`, `implicit` or `none` | `starttls` |

### Workforce OIDC and SCIM

**These variables are read for one import, and no longer configure anything.** The
provider and the SCIM credential are kept in `identity.sso_settings` and set on the
[Single sign-on card](/en/admin/sso-scim/#the-single-sign-on-card). The first process
serving the API that finds that row never written, while `OIDC_PROVIDER` or
`SCIM_TOKEN` is set, imports these into it, audited `sso-settings.imported` with their names; once the row is written
they are ignored, and each one still set is named in a warning at every start. This
release still validates them at startup, so remove them after the import; the next
release refuses them at startup. See
[Before the card](/en/admin/sso-scim/#before-the-card-the-environment-variables).

| Variable | Imported as | Default |
| --- | --- | --- |
| `OIDC_PROVIDER` | `entra` or `google`; unset, OIDC stays off | unset |
| `OIDC_AUTHORITY` | Exact issuer for the provider; over 200 characters, the provider is imported off | unset |
| `OIDC_CLIENT_ID` | Entra GUID, or a `.apps.googleusercontent.com` client ID; over 200 characters, the provider is imported off | unset |
| `OIDC_CLIENT_SECRET` | Client secret, sealed | unset |
| `OIDC_WORKLOAD_IDENTITY_TOKEN_FILE` | Absolute, readable assertion file (Entra only), in `OIDC_WORKLOAD_IDENTITY_TOKEN_DIR` when that is set; over 500 characters, the provider is imported off | unset |
| `AZURE_FEDERATED_TOKEN_FILE` | Platform-supplied fallback for the above | unset |
| `OIDC_ALLOWED_DOMAINS` | Comma list of bare DNS names (Google only, required there) | unset |
| `OIDC_DISPLAY_NAME` | Sign-in button label, cut to 100 characters | `Workforce SSO` |
| `SCIM_TOKEN` | The SCIM bearer token, kept as its digest; unset, SCIM stays off | unset |
| `SCIM_PREVIOUS_TOKEN` | Prior token during a rotation overlap, kept as its digest | unset |
| `SCIM_PREVIOUS_TOKEN_EXPIRES_AT` | The overlap's end; RFC 3339, in the future, ≤ 24h ahead | unset |

### Transport, storage and modules

| Variable | Purpose | Default |
| --- | --- | --- |
| `CSP_REPORT_ONLY` | Emit the CSP as report-only | `0` |
| `TRUSTED_PROXY_HOPS` | Number of proxies in front (0–10) | `0` |
| `TRUSTED_PROXY_CIDRS` | The proxies' own addresses, as CIDR prefixes | unset |
| `STORAGE_PROVIDER` | `fs`, or unset for fail-closed storage | unset |
| `STORAGE_FS_ROOT` | Absolute root for the `fs` driver | unset |
| `STORAGE_FS_ALLOW_INSECURE_ROOT` | Relax the root permission check (development only) | `0` |
| `BRREG_BASE_URL` | Brønnøysundregisteret origin | `https://data.brreg.no` |
| `BRREG_TIMEOUT` | Budget for one lookup, retries included | `15s` |
| `PEPPOL_LOOKUP_ENABLED` | On/off switch for the Peppol EHF-capability lookup | `1` |
| `PEPPOL_SML_ZONE` | SML zone a participant identifier is hashed into; must be a plain hostname — no scheme, path or whitespace | `participant.sml.prod.tech.peppol.org` |
| `PEPPOL_DNS_SERVER` | `host:port` of a resolver to use instead of `/etc/resolv.conf`; the port is required and must be numeric | unset |
| `PEPPOL_TIMEOUT` | Budget for one lookup end to end (DNS and SMP together) | `10s` |
| `INVOICES_EHF_ENABLED` | On/off switch for sending invoices as EHF over Peppol; also needs `PEPPOL_LOOKUP_ENABLED=1`, access-point credentials and the seller's Peppol id ([E-invoicing](/en/admin/e-invoicing/)) | `1` |
| `INVOICES_STORECOVE_BASE_URL` | Base URL of the Storecove access point's API — an absolute http or https URL, any trailing slash trimmed; point it at a mock host for a test (Storecove's sandbox is the same host with a sandbox key) | `https://api.storecove.com/api/v2/` |
| `INVOICES_VIPPS_ENABLED` | On/off switch for Vipps MobilePay payments of invoices; also needs the sales unit's credentials, and pay links `PUBLIC_BASE_URL` ([Payments](/en/admin/payments/)) | `1` |
| `INVOICES_VIPPS_BASE_URL` | Base URL of the Vipps MobilePay API — an absolute http or https URL, any trailing slash trimmed; `https://apitest.vipps.no` is Vipps' test environment | `https://api.vipps.no` |
| `STAFF_IMPORT_MAX_ROWS` | People one Staff import takes and one Staff export writes, from `5000` to `20000` — one number for both, so an export always imports back (above 20000 people an export would outgrow the import's 5 MiB file) ([Staff](/en/admin/staff/#import-and-export-the-register)) | `5000` |
| `STAFF_RETENTION_ENABLED` | On/off switch for the Staff retention worker, `staff-retention`, which erases what the retention rules no longer let the register keep and finishes deleting documents' files; off, nothing is erased on a schedule and `POST /retention/run` still runs it by hand ([Staff](/en/admin/staff/#retention)) | `1` |
| `STAFF_ACCOUNTS_ENABLED` | On/off switch for the Staff accounts worker, `staff-accounts`, which prepares, enables, updates and disables the users the account flag asks for — with a flag on it creates the person's pending, disabled user even before an Owner names the employee roles, and enables nobody until they are named; off, nothing is done on a schedule — leavers keep their access until it is on again or an Owner disables them ([Staff](/en/admin/staff/#the-accounts-worker)) | `1` |
| `STAFF_COMPETENCE_ENABLED` | On/off switch for the Staff competence worker, `staff-competence`, which mails a person and their managers once when a certificate comes within its reminder of its end and once when it has expired; off, nobody is mailed, and the dashboard still shows expiring and expired certificates ([Staff](/en/admin/staff/#the-competence-worker)) | `1` |
| `STAFF_WORKERS_POLL` | How often the Staff workers look: an erase cycle of at most fifty people and a pass over the deletion ledger, at most fifty due account links, a failed one retried at the next poll, and the competence rows come due; a duration of at least `1m` | `1h` |
| `CALENDAR_CANCELLATIONS_ENABLED` | On/off switch for the Calendar cancellations worker, `calendar-cancellations`, which erases a cancelled appointment — one deleted after someone outside the company had been sent an invitation — once its last day has passed; off, cancelled appointments are kept until it is on again ([Calendar](/en/admin/calendar/#the-cancellations-worker)) | `1` |
| `CALENDAR_WORKERS_POLL` | How often the Calendar workers look: an erase cycle of at most 200 cancelled appointments; a duration of at least `1m` | `1h` |
| `CALENDAR_SUBSCRIPTIONS_ENABLED` | On/off switch for the Calendar subscriptions worker, `calendar-subscriptions`, which fetches the calendars people subscribe to from elsewhere through the outbound address guard — no private address, no redirect, at most 1 MiB; off, every subscription shows its last read, and a save or **Refresh now** still fetches it once ([Calendar](/en/admin/calendar/#the-subscriptions-worker)) | `1` |
| `CALENDAR_SUBSCRIPTIONS_POLL` | How often each subscription is fetched — later when its calendar asks to be read less often, at most daily — by the worker, which wakes every five minutes; a duration of at least `5m` | `30m` |
| `CUSTOMERS_IMPORT_ENABLED` | Run the customers import worker, which checks and imports uploaded files; `0` leaves imports waiting | `1` |
| `CUSTOMERS_IMPORT_POLL` | How often the customers import worker looks for a queued import | `5s` |
| `CUSTOMERS_IMPORT_RETENTION` | How long a finished or abandoned customer import and its rows are kept | `720h` |
| `ENERGY_IMPORT_ENABLED` | Run the energy import worker, which checks, imports and attaches uploaded metering-point files; `0` leaves imports waiting | `1` |
| `ENERGY_IMPORT_POLL` | How often the energy import worker looks for a queued import | `5s` |
| `ENERGY_IMPORT_RETENTION` | How long a finished or abandoned metering-point import and its rows are kept | `720h` |
| `ENERGY_SPOT_PRICE_FEED_ENABLED` | On/off switch for the energy spot price worker, `energy-spot-prices`, which fetches the day-ahead prices of the price areas in use, from seven days back to tomorrow, through the outbound address guard — no private address, no proxy, no redirect, at most 1 MiB; off, prices are imported from a file ([Spot prices](/en/reference/energy/#spot-prices)) | `0` |
| `ENERGY_SPOT_PRICE_FEED_URL` | The feed as a template: an absolute `https` URL carrying `{year}`, `{month}`, `{day}` and `{area}`, the month and the day zero-padded; anything else stops the start-up, even with the worker off | `https://www.hvakosterstrommen.no/api/v1/prices/{year}/{month}-{day}_{area}.json` |
| `ENERGY_SPOT_PRICE_FEED_POLL` | How often the spot price worker runs a cycle of at most twenty requests, **with either source** — the public feed or the spot-price service; while the service's history is still being copied the next cycle comes after a minute instead. A duration of at least `1m`, a shorter one stopping the start-up even with the worker off | `15m` |
| `ENERGY_SPOT_PRICE_SERVICE_URL` | The shared spot-price service's base URL — `http://vantigo-spotprices:8080` on our hosts. Set, it turns the spot price worker on with the service as its source: it copies the prices of every area in use or followed by the service, from fourteen days back to tomorrow, and their history. An absolute `http` or `https` URL with no user, path, query or fragment; an `http` URL is dialled only to a private address ([Transport security](/en/admin/transport-security/#the-private-network-exception)). **One source per installation**: set together with `ENERGY_SPOT_PRICE_FEED_ENABLED=1`, it stops the start-up ("set one spot price source") ([Spot prices](/en/reference/energy/#the-sources)) | unset |
| `ENERGY_SPOT_PRICE_SERVICE_KEY` | The installation's bearer key for the spot-price service. Required with `ENERGY_SPOT_PRICE_SERVICE_URL` and refused without it; at most 200 bytes of printable characters with no space. A secret: redacted wherever the configuration is printed, never logged | unset |
| `ENERGY_SPOT_PRICE_HISTORY_FROM` | The oldest day the spot-price service's history is copied from, `YYYY-MM-DD`; anything else stops the start-up. Unset copies everything the service holds for each area | unset |
| `APP_TITLE`, `APP_LOGO_URL`, `APP_SUPPORT_EMAIL`, `APP_SUPPORT_PHONE`, `APP_SUPPORT_URL` | SPA branding | unset |

The Peppol lookup treats NXDOMAIN and NOERROR-with-no-NAPTR-records as the same
definitive "not registered" answer ([Peppol lookup](/en/reference/customers/#peppol-lookup)), since
the SML publishes a name only for a registered participant. That makes the
resolver named by `PEPPOL_DNS_SERVER` matter: one that answers NODATA instead
of forwarding the authoritative NXDOMAIN — or that silently drops an
unfamiliar record type such as NAPTR rather than passing it through — would
turn a genuinely registered participant into a silent false negative, with
nothing in the answer to tell the two apart. Point `PEPPOL_DNS_SERVER` at a
plain recursive resolver, not one that synthesizes or filters answers.

### Management listener

| Variable | Purpose | Default |
| --- | --- | --- |
| `MANAGEMENT_PORT` | Port of the private control-plane listener (must differ from `PORT`) | unset (disabled) |
| `MANAGEMENT_TOKEN` | Bearer token for it, ≥ 32 characters, no whitespace | unset (disabled) |

The two are one switch: set both or neither. See
[the management listener](/en/admin/management-listener/).

Communications' own settings (`COMMUNICATIONS_*`) are documented in
[Communications](/en/reference/communications/). OpenTelemetry is configured with the standard
`OTEL_EXPORTER_OTLP_*` variables, read by the OTel SDK rather than by `config.go`.

## Workforce OIDC

At most one workforce OpenID Connect provider is configured. It is a block of the
row `identity.sso_settings` — the provider, the authority, the client ID, the client
secret sealed under `APP_SECRET` or a token file, the allowed domains and the display
name — which an Owner sets on the **Single sign-on** card (`PUT
/api/v1/identity/owner/sso/oidc`, `policy:Owner+OwnerManagement`: an Owner, with MFA
while `OWNERS_REQUIRE_MFA` is on) and every process reads
through a 20-second cache, so a save is in effect within 20 seconds with no restart.
The rules are the ones the `OIDC_*` variables were held to, checked at the save; a
refusal is a `400` with field errors. The relying party is rebuilt when the row's
version moves.

**The client-authentication mode is inferred, not configured.** There is no
`ClientAuthentication` setting: the card's **Client secret** means client-secret
authentication, its **Token file** (an absolute path inside the directory
`OIDC_WORKLOAD_IDENTITY_TOKEN_DIR` names, to a regular file of at most 16 KiB that
the saving process can read; nothing outside the directory is looked at) workload
identity. Saving both at once is
refused, and so is saving neither, unless a client secret is stored for the same
provider and client ID, which is then kept; saving one clears the other. Workload identity is Entra-only; Google is
client-secret-only.

The flow is authorization code plus PKCE with a nonce. Provider metadata, issuer,
audience, signature, state, nonce and correlation are validated, the validated
identity is held in a sealed cookie, and provider tokens are never saved. The
browser starts at `/api/v1/identity/oidc/challenge`, the provider returns to the
fixed `/api/v1/identity/oidc/callback`, and local completion is
`/api/v1/identity/oidc/complete`. Every failure is a redirect to
`/sign-in?error=<code>`; no redirect target ever comes from the request.

Provider-specific checks then apply: Entra requires a `tid` matching the tenant GUID
in the configured authority and an `oid` GUID, and requires `azp` to equal the
client ID when the token carries multiple audiences; Google requires
`email_verified`, a valid email, and a non-empty `hd` that matches the email domain
and appears in the allowed domains.

New identities are provisioned just in time as local `User` accounts, keyed by the
validated issuer and case-sensitive `sub`. Provider email is informational: an email
collision **fails rather than auto-linking**. OIDC claims never grant local roles and
never satisfy the local MFA requirement.

`GET /api/v1/identity/providers` is anonymous and reports the configured sign-in
providers to the SPA, read from the row, so the button appears within 20 seconds of a
save.

### Required callback URI

Register the exact public HTTPS callback with the provider, including the base path
when one is configured:

```text
https://vantigo.example.com/api/v1/identity/oidc/callback
```

The callback path is fixed and is not a deployment setting; the **Single sign-on**
card shows the whole URL as **Callback URL**. The public origin, the forwarded scheme
and the provider registration must agree.

## SCIM

SCIM 2.0 is served at `/api/v1/identity/scim/v2` and authenticated by a bearer token
an Owner issues with **New token** on the **Single sign-on** card (`POST
/api/v1/identity/owner/sso/scim/token`): generated by the server, answered once, and
kept in `identity.sso_settings` as its SHA-256 alone. SCIM is on while a token exists,
and **Turn off** clears it. A rotation keeps the token it replaces for an overlap of at
most 24 hours, which **End overlap** ends early; both digests are compared, the
previous one only until its expiry. Every process reads the row through a 20-second
cache. The procedures are in
[SSO and SCIM operations](/en/admin/sso-scim/#scim).

The User resource accepts the **enterprise extension**,
`urn:ietf:params:scim:schemas:extension:enterprise:2.0:User` (RFC 7643 §4.3):
`employeeNumber`, `costCenter`, `organization`, `division`, `department` and `manager`
(`value`, `displayName`, and `$ref` answered as this server's path for that id), each
a string, trimmed, of at most 256 characters, or `invalidValue`. The block is named by
its URN exactly; its attributes are read in any ASCII case, as the core ones are. A
create carrying the block, a replace — which sets it whole, and clears it when it is
left out — and a PATCH are accepted and kept in the user's SCIM profile under
`enterprise`. A PATCH may name one of its paths (`urn:…:User:department`,
`urn:…:User:manager`, `urn:…:User:manager.value`, …), or carry no path and an object
whose member under the URN sets each attribute it names; `manager` takes an object
with a string `value`, or a bare string as its value, as Microsoft's provisioning
service sends it. An attribute the extension has no path for, and a path outside the
two schemas, is still `invalidPath`. `GET` answers the block, the URN among its
`schemas`, when it holds any value; `Schemas` and `ResourceTypes` advertise it
(`schemaExtensions`, not required); and `GET /Users` filters on
`urn:…:User:employeeNumber eq "…"` — the URN spelled exactly, the value compared as the
profile keeps it. Nothing in `identity.users` changes for it: its `employeeNumber` is
what an [announced account](#the-announced-account) is matched on, never the user's
employee number.

## Forwarded headers and HTTPS

`TRUSTED_PROXY_HOPS` **counts the proxies in front of the server** rather than
listing them: with `N > 0`, each of the N trusted proxies appended one entry to
`X-Forwarded-For`, so the client is the Nth entry from the right and everything to
its left is client-written and untrusted. `X-Forwarded-Proto`'s last entry becomes
the scheme. `X-Forwarded-Host` is never honoured — proxies must preserve `Host`.

Outside development, hops without `TRUSTED_PROXY_CIDRS` fails startup:

```text
TRUSTED_PROXY_HOPS: requires TRUSTED_PROXY_CIDRS outside development:
forwarded headers are honoured only from a peer inside that list
```

With the list set, forwarded headers are honoured only when the direct peer falls
inside one of its prefixes; any other peer is treated as the client itself. Without
it, every peer could choose the client address the rate limits key on.

```text
TRUSTED_PROXY_HOPS=1
TRUSTED_PROXY_CIDRS=10.0.0.0/24
```

Terminate TLS at the proxy, forward the original scheme, and expose the SPA, API and
OIDC callback on that HTTPS origin. Outside development cookies are `Secure` and
`APP_URL` must be `https`.

## Database migrations

One PostgreSQL database holds one schema per module — `identity`, `customers`,
`products`, `energy`, `communications`, `projects`, `time`, `expenses`, `invoices`,
`staff`, `planning`, `calendar` and `entra` — migrated by plain SQL files embedded in the binary. Every schema is migrated regardless of which modules `MODULES` enables,
so enabling a module later needs no migration. See the
[contributor migration guide](https://github.com/vantigo-io/vantigo/blob/main/CONTRIBUTING.md#database-migrations) for how
migrations are written and applied.

**Both the `migrate` command and the `api` command apply migrations.** `api`
applies pending migrations under a PostgreSQL advisory lock and only serves once
they succeed; `server` mode never migrates. Migrators serialize on that lock, so an
old and a new binary starting at once wait for each other instead of racing.

Running a terminating `migrate` job first and waiting for it is still the right
deployment shape: the point is to see a migration failure **before** any serving
replica starts, not that `api` would otherwise leave the schema behind. Use
`MIGRATIONS_DATABASE_URL` for the owner role where a deployment separates it from
the runtime role.

Do not run `seed` in production; it is development-only and exits 2 outside it.
