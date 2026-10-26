# Staff — the photo follows the account

A follow-up to [the person's photo and the at-a-glance cards](2026-10-25-staff-person-glance-design.md)
("G"), decided with the owner on 2026-10-25 before G's pull request (#175) landed; built
on G's branch and opened against `main` after it. Code references are against that
branch at `2cdb6d65`; `srv/` abbreviates `apps/server/internal/`, `st/` `srv/staff/`, `id/`
`srv/identity/`, `en/` `srv/entra/`, "the reference" `docs/src/content/docs/en/reference/`.

## 1. Purpose

One picture of a person, everywhere it is shown: HR sets a photo in Staff and it becomes
the user's picture in Vantigo and in Microsoft Entra ID; while HR's photo stands the user
cannot pick another; when HR has set none, the user's own Vantigo avatar is the picture,
shown on the Staff page too and pushed to Entra the same way. Nothing flows back from
Entra: Vantigo is the source, and the directory is expected to catch up.

## 2. Decisions (the owner's, 2026-10-25)

**S1 — Two pictures, one rule.** HR's photo lives in Staff (G S1). The person's own
picture is their Vantigo avatar in identity. The **effective picture** is HR's when set,
else the person's own. While HR's photo stands, choosing an avatar in Vantigo is
**disabled** (identity refuses it, the host's profile page says why); the user's own
avatar is kept aside and comes back when HR removes the photo.

**S2 — HR's upload syncs; the person's own syncs too.** Whichever picture is effective is
pushed to Entra. The person's own "Change photo" on My page sets their identity avatar
(not Staff's slot, which becomes HR's alone — G's self-write is removed), and is disabled
while HR's photo stands.

**S3 — One direction.** Vantigo writes the directory's photo and never reads it.

**S4 — The seams are the existing ones.** Staff writes identity through
`contracts.AccountProvisioning` (the staff → identity write port) and reads the effective
avatar through `contracts.UserDirectory` (the read port); Staff's accounts worker pushes
the effective picture to the directory through `contracts.IdentityProvisioner`, as it
pushes the directory profile, and records what it pushed on the provider link. Entra
owns Graph's rule (JPEG, under 4 MB) and converts the image itself.

## 3. Data model

### 3.1 `identity.managed_avatars` (migration `00070_identity_managed_avatars.sql`)

| Column | Type | Notes |
|---|---|---|
| `user_id` | uuid PK → `identity.users` on delete cascade | one managed picture per user |
| `data` | bytea not null | the bytes as Staff stored them (metadata already stripped) |
| `content_type` | text not null, `image/jpeg` or `image/png` | the same CHECK as `profile_avatars` |
| `source` | text not null | `staff` |
| `source_stamp` | text not null | Staff's `photo_updated_at` in RFC 3339, the reconciliation key |
| `version` | integer not null | bumped on every write |
| `updated_at` | timestamptz not null | |

`identity.profile_avatars` (the user's own) is untouched. The **effective avatar** is the
managed row when one exists, else the own row: `GET /account/avatar`, the owner's
`GET /owner/users/{id}/avatar`, `has_avatar` and the contract's read all answer it.

### 3.2 `staff.identity_links.photo_pushed_stamp` (migration `00071_staff_photo_push.sql`)

`varchar(80)`, nullable, on the **provider** link (`provider = 'entra'`): the effective
picture's stamp last pushed to the directory — `managed:<source_stamp>`, `own:<version>`
or `none` — no class (it is about the account, not the person; the link is erased with
the person as today).

### 3.3 Contracts

```go
// contracts.AccountProvisioning gains the managed avatar: Staff's photo as the
// user's picture. nil clears it and the user's own avatar shows again.
SetManagedAvatar(ctx context.Context, actor Actor, userID uuid.UUID, avatar *ManagedAvatar) error
type ManagedAvatar struct{ ContentType string; Data []byte; Source, Stamp string }
// Refusals: ErrInvalidActor, ErrUserGone, ErrAccountPrivileged (an Owner's or a
// SystemAdmin's picture is theirs), ErrAvatarInvalid (not JPEG/PNG, over 5 MiB
// or 4096 px — identity judges as it judges an upload).

// contracts.UserDirectory gains the effective avatar, a read.
Avatar(ctx context.Context, userID uuid.UUID) (*AvatarEntry, error) // nil when none
type AvatarEntry struct{ ContentType string; Data []byte; Stamp string; ManagedBy string; UpdatedAt time.Time }
// Stamp is "managed:<source_stamp>" or "own:<version>"; ManagedBy "" or "staff".

// contracts.IdentityProvisioner gains the directory's photo.
SetPhoto(ctx context.Context, actor Actor, externalID string, photo Photo) error
ClearPhoto(ctx context.Context, actor Actor, externalID string) error
type Photo struct{ ContentType string; Data []byte }
// SetPhoto converts to what the directory takes (Entra: JPEG under 4 MB, the
// longer side at most 1024 px) and reports ReportedPhotoNotWritten when the
// directory refuses it (400, 403, 413); ClearPhoto is idempotent (404 is done).
const ReportedPhotoNotWritten = "photo_not_written"
```

## 4. Rules

**Who sets what.** Staff's `PUT`/`DELETE /people/{id}/photo` are `staff:manage`'s and
`staff:personal-data`'s alone (`canSetPhoto`); the person's own picture is set through
identity's `POST`/`PUT`/`DELETE /account/avatar`, which answer **409 `avatar_managed`**
while a managed avatar exists. `GET /account` carries `avatarManagedBy` (`"staff"` or
null) so the host disables the controls and says who to ask.

**Staff → identity, at once and reconciled.** After a photo `PUT` or `DELETE` commits, when
the person has a linked user, Staff calls `SetManagedAvatar` (the stripped bytes, source
`staff`, stamp `photo_updated_at`) or clears it, outside the transaction with the
request's actor; a failure is logged and does not fail the request — the **accounts
worker reconciles**: on every claim of a person's identity link it reads
`Avatar(user)` and compares: a photo on the person whose stamp is not identity's managed
stamp → `SetManagedAvatar`; no photo but a managed avatar → clear. So a person linked
after HR set the photo, or an identity write that failed, is right by the next claim.

**The Staff page shows the effective picture.** `GET /people/{id}/photo` streams HR's
photo when set, else the linked user's effective avatar read through the contract, else
404. The person response's `photoUpdatedAt` is the effective picture's time and a new
`photoSource` says `record` (HR's) or `account` (the user's own); the directory's list
row keeps Staff's own `photoUpdatedAt` only (no avatars in the list yet, G S4). The
person's own picture is in the Work group like HR's.

**Staff → Entra.** On a provider link, after the chain's main step, when the account is
enabled and the provider connected, the worker compares the effective picture's stamp
(`Avatar(user).Stamp`, `none` when nil) with `photo_pushed_stamp`: different → `SetPhoto`
or `ClearPhoto`, then the stamp is written with the link's result; a refusal is the
reported code `photo_not_written` (the item `photoNotWritten`, "The directory refused the
photo"), kept until a push succeeds; a failure retries as every provider call does.
Entra's `SetPhoto` decodes the image (bounded: Staff and identity admit at most 4096 px a
side), scales the longer side to 1024 px, encodes JPEG at quality 85 (lower until under
4 MB), and `PUT`s `/users/{id}/photo/$value` as `image/jpeg` — the Graph client gains a
raw body; `ClearPhoto` `DELETE`s it, a 404 counting as done. Both are recorded as
`entra.user.photo_set` / `entra.user.photo_cleared`. The permission is the already
granted `User.ReadWrite.All`.

**Events.** Staff: `photo_set` / `photo_removed` unchanged; the worker's push is a code
`photo_pushed` / `photo_cleared` on `account_external_updated`'s codes. Identity: audit
actions `user.avatar-managed` and `user.avatar-unmanaged` with the Staff actor.

**Erase and delete.** The managed avatar cascades with the user; `SetManagedAvatar(nil)` is
also called when Staff erases a person's photo through the D erase? — no: the erase runs
after the person left; the worker's reconciliation clears the managed avatar when the
photo is gone (the identity link outlives the photo until the account is deactivated),
and the user's deactivation/erasure takes the rest. Stated as a reading.

## 5. API

Identity (`openapi/identity.yaml`): `AccountResponse.avatarManagedBy` (required, nullable
`"staff"`); `POST`/`PUT`/`DELETE /account/avatar` gain 409 `avatar_managed`. Staff
(`openapi/staff.yaml`): `StaffPerson.photoSource?` (`record` | `account`, `x-enum-varnames`
`PhotoFromRecord`, `PhotoFromAccount`), `canSetPhoto`'s description (HR alone), the
photo operations' descriptions, `StaffStatsAttentionItem.type` gains `photoNotWritten`.
Entra: nothing public.

## 6. Frontend

- **Staff, My page**: the avatar's menu for the person themselves — **Change photo** /
  **Remove photo** call identity's account avatar endpoints (a cross-module HTTP read as
  Time calls Projects); when `photoSource === "record"` the menu shows **Set by HR**
  disabled. The person page: HR's menu as today; `photoSource: account` shows the user's
  own picture with no menu change.
- **Host, Settings › Profile**: when `avatarManagedBy === "staff"`, the file input and
  buttons are disabled with the hint *Your photo is kept by Staff. Ask HR to change it.*

## 7. Documentation

The Staff reference (`### The photo` rewritten: who sets what, the account sync and
reconciliation, the effective picture, `photoSource`; `### The provider link` the push
and the stamp; the items table `photoNotWritten`; the worker's events' codes; the
schema's 00071; the exports unchanged); the identity reference (the managed avatar,
`avatar_managed`, `avatarManagedBy`, the audit actions, 00070); the Entra reference
(`SetPhoto`/`ClearPhoto`, the conversion, the reported code, the Graph client's raw
body, the events); the boundaries page (the two contract methods; `AccountProvisioning`
stays the one write direction); the user guides en+nb (Staff: My page's photo wording,
the person page; the host's profile settings page); `admin/entra.md` "what can go wrong"
row; `admin/installation.md` en+nb (00070, 00071); `ROADMAP.md`; G's spec gains a
pointer here.

## 8. Readings on the record

1. **The managed avatar is a second row, not an overwrite**, so HR's removal restores the
   user's own picture without anyone re-uploading it.
2. **An Owner's or SystemAdmin's picture is never managed** (`ErrAccountPrivileged`): HR's
   record does not reach the people who run the installation; Staff logs it and raises no
   item.
3. **The managed avatar applies to a linked user whether or not Staff manages the
   account**: the link says whose picture it is; the account's management is a separate
   question.
4. **Entra owns the conversion** (JPEG, 1024 px, under 4 MB) because the limit is Graph's;
   `golang.org/x/image/draw` becomes a direct dependency.
5. **The push is reconciled by stamp, not by event**, so a failed write, a late link or a
   changed own avatar is right by the next claim, with no identity → Staff callback.
6. **The erase does not call identity for the avatar**: the worker's reconciliation
   clears the managed avatar once the photo is gone, and the account's own end takes the
   rest.
7. **No new permission anywhere**, and no new Graph consent: `User.ReadWrite.All` covers
   the photo.

The plan's readings (2026-10-25):

8. **Identity judges the managed avatar as it judges an upload** (JPEG/PNG, 5 MiB, 4096 px)
   and answers `ErrAvatarInvalid`; Staff's bytes already pass, so the check guards the
   contract, not the flow.
9. **`photoSource` is `record` or `account`; the list row carries no source**, since the
   list has no avatars, and its `photoUpdatedAt` is Staff's own photo's alone.
10. **The worker pushes the photo after the chain's main step, within the same claim**,
    and a photo failure is the link's failure (`last_error`, `attempts`, retried at the
    next poll; three raise `accountFailing`) — not a link state of its own.
11. **The My page menu calls identity directly** from the Staff frontend (same origin; the
    pattern Time uses for Projects), so Staff's API carries no proxy for the user's own
    avatar.

The lanes' decisions and the review's fixes (2026-10-26), the code as it landed:

12. **The managed avatar's set is idempotent**: the same source, stamp, type and bytes
    again writes nothing and audits nothing, and a clear with none standing likewise.
13. **The managed source must be `staff`**, refused `ErrAvatarInvalid` otherwise, and the
    audit's `user.avatar-managed` / `user.avatar-unmanaged` record the source, the stamp,
    the content type and the byte count, never the bytes.
14. **Identity's 409 `avatar_managed` is judged before the upload's body is read**, and a
    new query `HasManagedAvatar` answers it.
15. **Only a set is refused for a privileged user**: a clear is always allowed, so an
    Owner or SystemAdmin who gains the role after HR set a photo is freed of it, and the
    worker's reconciliation clears it (revising reading 2 for the clear).
16. **The own avatar's stamp is `own:<version>@<epoch of updated_at>`**, so an avatar
    deleted and uploaded again — its version starting over — never repeats a stamp.
17. **`UserDirectory` gains `AvatarInfo`** (the stamp and the time, no bytes) beside
    `Avatar`: the person response and the push's comparison read it, and the bytes are
    read only when streaming or pushing.
18. **Staff's stamp is `photo_updated_at` in UTC as RFC 3339 with nanoseconds**, formatted
    by one function for the inline write and the worker alike.
19. **The inline identity write runs right after the commit with the HR user as the
    actor and never fails the request**; privileged and accounts-unavailable are logged
    at Info, anything else at Warn, and left to the worker.
20. **Changing or removing a person's self-service link moves the managed avatar with
    it**: the old user's is cleared and, when HR has a photo, the new user's set, at once
    and outside the transaction, logged on failure.
21. **The D erase clears the linked user's managed avatar through identity**
    (`system:staff-retention`, once the cycle's connection is returned, a failure
    logged), and an erased person's page and photo `GET` never fall back to the
    account's avatar — replacing reading 6.
22. **The worker's reconciliation runs before the provider column on every claim**, and
    is skipped when identity reported no user, for a privileged user (but for the clear,
    reading 15) and for an erased person.
23. **The push runs only when the provider chain ended with nothing to do** on an enabled
    account of a connected provider, the flag on, the person not erased, a user linked,
    within the chain's three calls and with the lease renewed before it.
24. **A photo write or a link change moves the identity link's next attempt to now**, so
    the push reaches the directory at the next poll (within the hour by default), not at
    the next civil midnight.
25. **The first push never removes a photo the tenant already had**: with no Vantigo
    picture and no stamp yet, the worker records `none` without a call — Vantigo removes
    only what it wrote.
26. **A refused push still writes the stamp**, so the same picture is not pushed again
    until HR or the user changes it, and the account is re-read at once so
    `photo_not_written` is on `reported_codes` in the same claim.
27. **The push's event is one `account_external_updated` with `step: "photo"`** and the
    code `photo_pushed`, `photo_refused` or `photo_cleared`.
28. **`identity_links.photo_pushed_stamp` is class C** with the rest of the link, not
    classless as §3.2 said: the retention table classes every column of the tables an
    erase reaches.
29. **Entra re-encodes every photo** — the format sniffed whatever the content type says,
    the longer side scaled to 1024 px with Catmull-Rom over white, JPEG at quality 85,
    70, then 55 until under 4,000,000 bytes — so metadata is stripped again, and a source
    over 4096 px a side is refused as undecodable without being decoded.
30. **Entra's refusals are reported, not failed**: a 400, 403, 413 or 404 (a user without
    a mailbox) on the photo's `PUT`, a picture that does not decode or stays over the
    limit, and a refusal of the photo's `DELETE` all answer nil and report
    `photo_not_written` until a push is taken or a clear runs; a 404 on the `DELETE` is
    done.
31. **A user synchronised from on-premises is not refused up front for the photo**:
    Graph's answer decides.
32. **The events are `entra.user.photo_set`** (codes `photo_written` or
    `refused:photo_not_written`) **and `entra.user.photo_cleared`**, and the Graph client
    gained `putRaw` for a raw body; `golang.org/x/image` is a direct dependency.
33. **My page's menu has three states for the person**: **Set by HR**, disabled, with
    *Ask HR to change it* while HR's photo stands; else **Change photo**, and **Remove
    photo** only when the picture is their own; HR's **Remove photo** on the person page
    is hidden when the picture shown is the user's own, and the host's Settings › Profile
    disables its controls with *Your photo is kept by Staff. Ask HR to change it.*
