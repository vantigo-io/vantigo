# Object storage

Object storage is the application port Communications stages and serves attachments
through. Modules never talk to a provider SDK: they receive an `ObjectStore` scoped
to their own namespace and pass relative keys.

## The provider set is a regression from the .NET implementation

**The local filesystem is the only supported provider.** The implementation
(`apps/server/internal/storage`) contains one driver, `fs.go`, and configuration
accepts only `fs`:

```text
STORAGE_PROVIDER: must be "fs" if set
```

The S3/MinIO and Azure Blob providers the .NET implementation offered **do not
exist in this port**. There is no `STORAGE_S3_*`, no `STORAGE_AZURE_BLOB_*`, no
access-key, SAS, connection-string or managed-identity authentication, and no
container provisioning. A deployment that needs remote object storage today must
mount durable, exclusively-owned storage into the container and point the `fs`
driver at it. Adding a driver is additive — the port is provider-neutral — but
nothing in this repository implements one.

## Configuration

| Variable | Purpose | Default |
| --- | --- | --- |
| `STORAGE_PROVIDER` | `fs`, or unset | unset |
| `STORAGE_FS_ROOT` | Absolute root directory; required when the provider is `fs` | unset |
| `STORAGE_FS_ALLOW_INSECURE_ROOT` | Relax the group/world-writable root check | `0` |

```text
STORAGE_PROVIDER=fs
STORAGE_FS_ROOT=/var/lib/vantigo/objects
```

`STORAGE_FS_ROOT` must be an absolute path, and setting it without
`STORAGE_PROVIDER=fs` is a configuration error rather than a silent no-op.

**Unset storage fails closed at each operation, not at startup.** With
`STORAGE_PROVIDER` unset the process still starts and serves everything else; every
storage operation then reports `storage: not configured`. That is deliberate: an
installation that never uploads an attachment does not need storage provisioned to
boot.

`STORAGE_FS_ALLOW_INSECURE_ROOT` is accepted **only when `APP_ENV=development`**;
outside development it is a startup error regardless of what it would have done —
the same shape as `MAIL_DRIVER=log` and `OWNERS_ALLOW_INSECURE_NO_MFA`.

## The filesystem driver

The root must exist or be creatable at initialisation, must not itself be a symbolic
link, and — outside development — must not be group- or world-writable. Directories
the driver creates are mode `0700`: the application identity alone. Give it a volume
the application user exclusively owns; do not point it at a shared writable
directory.

Containment is enforced by the operating system, not by a check-then-open pair. The
driver opens the root once as an `os.Root` and performs every operation through it,
so each name is resolved with `openat`-style containment at the moment of use: a
symlink planted inside the root afterwards — even one timed to land between a check
and the syscall — still cannot make an operation land outside the root. Key
validation runs in front of that anyway, rejecting traversal, URL-encoded, absolute,
backslash, control-character and duplicate-prefix forms, so nonsense input gets a
clear typed rejection before any syscall. The two are complementary, not redundant.

Writes go to a temporary file under the root and are atomically renamed into place,
so a crash mid-write never leaves a partially written object readable. Keys are
bounded at 1024 UTF-8 bytes. **No API ever returns a physical filesystem path.**

## Module scopes and the physical key

Every module gets a scoped store, and the scope prefixes every key before it reaches
the driver. The physical key is:

```text
{scope}/{relative-key}
```

**There is no tenant segment.** The .NET implementation's physical key was
`tenants/{tenant-id}/{scope}/{relative-key}`; this is a single-tenant application and
the tenant segment is gone along with the rest of tenancy. Nothing resolves a tenant,
and no storage operation fails for want of one.

Scope names are canonical lowercase ASCII `[a-z0-9-]`, at most 64 characters, with no
slashes or dots. Communications uses the scope `communications`, so one of its
attachments lands at `communications/<relative-key>`. Expenses uses the scope
`expenses`: a receipt's relative key is `receipts/<entryId>/<uuid>`, so its
physical key is `expenses/receipts/<entryId>/<uuid>`. A scoped store refuses a
relative key that equals its scope or already begins with `{scope}/`: callers pass
relative keys only and must never construct the prefix themselves.

**Expenses' receipts, specifically.** They are the only thing this module stores.
A receipt is read by whoever may see the expense it is on — the same rule that
governs the expense itself, not a rule of its own — and always through the
application's own download endpoint (below), never a direct storage read. An
object is removed with its expense: on an ordinary delete the object goes once
the database row's removal has committed, and on a failed or refused write the
object that was staged for it is removed again, so a failure never leaves an
object nothing points at. The one window nothing closes is the process dying
between writing the object and that compensating removal or commit completing —
there is no background sweeper today, so a receipt orphaned that way outlives the
request that caused it. See [Expenses](expenses.md#receipts) for the upload rules
(types, size, count, sniffing) and the rate limit.

## Downloads

Downloads always stream through an authorized application endpoint.

**The storage contract has no presigned-URL operation**, and no provider ever
redirects a caller to a direct storage URL. Communications' download endpoint
authorizes the attachment, loads it through the scoped store, and returns the bytes
with the stored content type and a sanitized filename; the `downloadPath` field in
its DTOs is that same-origin application endpoint, never a storage location. See
[Communications](communications.md) for the attachment lifecycle.

Expenses' `GET /attachments/{id}` follows the same shape: it authorizes the
receipt through the expense it belongs to, loads it through the module's own
`expenses`-scoped store, and streams the bytes back with the content type they
were sniffed as on upload and the file name as it was given — never a storage
location of any kind. See [Expenses](expenses.md#receipts).
