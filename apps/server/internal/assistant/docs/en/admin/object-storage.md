---
title: "Object storage"
description: "The filesystem and S3 providers, moving between them, module scopes, key validation and streamed downloads."
sidebar:
  order: 41
sources:
  - apps/server/internal/storage
  - apps/server/cmd/vantigo/storage.go
  - apps/server/internal/invoices/bankimport.go
  - apps/server/internal/invoices/bank_feed_sync.go
  - apps/server/internal/invoices/reminderpdf.go
  - apps/server/internal/invoices/print_batches.go
  - apps/server/internal/staff/objects.go
  - apps/server/internal/customers/objects.go
  - apps/server/internal/manufacturing/quality_photos.go
  - apps/server/internal/products/objects.go
  - apps/server/internal/identity/brand_logos.go
---
Object storage is the application port Communications stages and serves attachments
through. Modules never talk to a provider SDK: they receive an `ObjectStore` scoped
to their own namespace and pass relative keys.

## Providers

Two drivers implement the port, and `STORAGE_PROVIDER` picks one:

- **`fs`** stores objects under a local directory, a mounted volume in a container.
- **`s3`** stores them in a bucket on any S3-compatible service: UpCloud Managed
  Object Storage, AWS S3, MinIO, SeaweedFS and the like.

The Azure Blob provider the .NET implementation offered does not exist in this port.
Both drivers lay objects out the same way (see
[Module scopes and the physical key](#module-scopes-and-the-physical-key)), so
`vantigo storage copy` moves an installation from one to the other without renaming
anything.

## Configuration

| Variable | Purpose | Default |
| --- | --- | --- |
| `STORAGE_PROVIDER` | `fs`, `s3`, or unset | unset |
| `STORAGE_FS_ROOT` | Absolute root directory; required when the provider is `fs` | unset |
| `STORAGE_FS_ALLOW_INSECURE_ROOT` | Relax the group/world-writable root check | `0` |
| `STORAGE_S3_ENDPOINT` | The service's URL, `https://host[:port]` with no path; `http` only with `APP_ENV=development`. Required for `s3` | unset |
| `STORAGE_S3_REGION` | The provider's region name, for example `europe-1`. Required for `s3` | unset |
| `STORAGE_S3_BUCKET` | The installation's bucket: 3–63 lowercase letters, digits and hyphens. Never created by the app. Required for `s3` | unset |
| `STORAGE_S3_PREFIX` | A key prefix inside the bucket: lowercase letters, digits and hyphens, in segments joined by `/` | none |
| `STORAGE_S3_ACCESS_KEY_ID` | The access key; or `STORAGE_S3_ACCESS_KEY_ID_FILE`, the path of a file holding it. Required for `s3` | unset |
| `STORAGE_S3_SECRET_ACCESS_KEY` | The secret key; or `STORAGE_S3_SECRET_ACCESS_KEY_FILE`. Required for `s3` | unset |
| `STORAGE_S3_FORCE_PATH_STYLE` | `1` addresses the bucket in the path (`https://host/bucket/key`), `0` as a subdomain | `1` |

```text
STORAGE_PROVIDER=fs
STORAGE_FS_ROOT=/var/lib/vantigo/objects
```

```text
STORAGE_PROVIDER=s3
STORAGE_S3_ENDPOINT=https://abc123.upcloudobjects.com
STORAGE_S3_REGION=europe-1
STORAGE_S3_BUCKET=acme
STORAGE_S3_ACCESS_KEY_ID_FILE=/run/secrets/s3/access-key-id
STORAGE_S3_SECRET_ACCESS_KEY_FILE=/run/secrets/s3/secret-access-key
```

Each driver's variables are valid only with its provider: setting `STORAGE_FS_ROOT`
without `STORAGE_PROVIDER=fs`, or any `STORAGE_S3_*` without `STORAGE_PROVIDER=s3`, is a
configuration error rather than a silent no-op. A credential and its `_FILE` variant
must not both be set; a file's trailing newline is dropped, and the value must hold no
whitespace. The secret key is redacted wherever the configuration is printed.

**Unset storage fails closed at each operation, not at startup.** With
`STORAGE_PROVIDER` unset the process still starts and serves everything else; every
storage operation then reports `storage: not configured`. That is deliberate: an
installation that never uploads an attachment does not need storage provisioned to
boot.

`STORAGE_FS_ALLOW_INSECURE_ROOT` is accepted **only when `APP_ENV=development`**;
outside development it is a startup error regardless of what it would have done —
the same shape as `MAIL_DRIVER=log` and `OWNERS_ALLOW_INSECURE_NO_MFA`.

## The readiness check

When storage is configured, `GET /health/ready` runs a check named `storage` next to
`postgres`: a `HeadBucket` on the bucket for `s3`, a stat of the root for `fs`. A
missing bucket, refused credentials, an unreachable endpoint or a root that has gone
away answer 503 with `storage` in `failing`; the process keeps running, so it recovers
by itself once the bucket answers again. With storage unset there is no check.

A store that cannot even be built is a startup error, as before: an `fs` root that
cannot be created or written, for example. The `s3` driver contacts nothing at
startup, so its problems always show in readiness instead.

## The S3 driver

- **Uploads stream.** An object of up to 16 MiB goes as one `PUT` and is held in
  memory while it is sent; anything larger goes as a multipart upload in 16 MiB parts,
  holding about two parts (32 MiB) in memory, whatever the file's size. Nothing is
  written to local disk. Count it in the pod's memory limit: a pod with many large
  uploads in flight at once needs 32 MiB for each.
- **Content type** is stored with the object. A `Put` over an existing key replaces it.
- **No integrity trailers.** The driver (built on `minio-go`) never sends the
  trailing CRC checksums recent AWS SDKs add by default, which UpCloud and several
  other S3-compatible services reject.
- **A missing key** is "does not exist" to the modules, as on `fs`; deleting a missing
  key succeeds.
- **The bucket is never public and never presigned**: downloads still stream through
  the application's authorised endpoints, as described under [Downloads](#downloads).
- **The bucket is never created or configured by the app.** Create it, its
  credentials and its policy beforehand.

The credentials need these actions on the bucket and nothing else:
`s3:ListBucket` on `arn:aws:s3:::<bucket>` (for the readiness check and
`vantigo storage copy`), and `s3:GetObject`, `s3:PutObject`, `s3:DeleteObject` and
`s3:AbortMultipartUpload` on `arn:aws:s3:::<bucket>/*`.

### UpCloud Managed Object Storage

- **Endpoint:** `https://<service-id>.upcloudobjects.com`, or
  `https://<service-id>-private.upcloudobjects.com` from a private network.
- **Region:** the region name in lowercase, `europe-1` for Helsinki, not the zone
  code (`fi-hel2`).
- **Addressing:** keep `STORAGE_S3_FORCE_PATH_STYLE=1`, the default.
- **One bucket per installation, with its own user.** Create a user for the
  installation, attach a policy that grants the actions above on that one bucket, and
  give the app that user's access key. A user with no policy can do nothing, so keys
  of one installation cannot reach another's bucket.
- **Encryption at rest** is always on (AES-256, keys managed by UpCloud); there is
  nothing to configure.

## Moving between fs and s3

`vantigo storage copy` copies every object between an `fs` root and the bucket the
environment configures. Run it with the `s3` settings (`STORAGE_PROVIDER=s3` and the
`STORAGE_S3_*` variables) and the old root mounted:

```sh
vantigo storage copy --fs-root /var/lib/vantigo/objects
```

The copy run's environment is the installation's with the storage settings swapped:
`STORAGE_PROVIDER=s3` and the `STORAGE_S3_*` variables, and no `STORAGE_FS_ROOT`, which
is refused without `STORAGE_PROVIDER=fs` — the root is given with `--fs-root` instead.

`--to fs` copies the other way, from the bucket into the root. The command skips an
object the destination already holds with the same size, never copies the `fs`
driver's unfinished writes (`.<name>.<random>.tmp` files), prints how many objects
it copied and skipped, and exits 1 on the first error. It is safe to run again: a
second run copies the objects that are new or have changed size. An object rewritten
in place at the same size is skipped as well; `--all` copies every object whatever is
already there.

To move an installation from `fs` to `s3`:

1. Create the bucket, its user and policy.
2. Run `vantigo storage copy` while the installation serves. This moves the bulk.
3. Stop writes: stop the `api`, `server` and `worker` processes, or scale them to
   zero.
4. Run `vantigo storage copy --all` again. With writes stopped, this pass copies
   everything, so nothing rewritten in the meantime is missed. Without `--all` it is
   quicker and copies what is new or has changed size.
5. Switch `STORAGE_PROVIDER` to `s3`, set the `STORAGE_S3_*` variables, remove
   `STORAGE_FS_ROOT`, and start.
6. Check that `/health/ready` passes and that an existing attachment downloads. Keep
   the old volume until you have.

An object deleted between the two copies stays in the bucket as an orphan: no module
refers to it any more, so it is never served.

## Temporary files

Objects never pass through local disk on either driver, but one feature writes a
temporary file: **Accounting's SAF-T import** keeps the uploaded file (up to
`ACCOUNTING_SAFT_IMPORT_MAX_BYTES`, 512 MiB by default) in the temporary directory
while it reads the preview from it and then stores it. The temporary directory is `$TMPDIR`, or `/tmp` when it is
unset, and it must be writable by the application user and large enough for the
largest import. With a read-only root filesystem, mount a writable volume there.

## Brand logos

Identity now stores **brand logos** (the marks, icon and wordmark of
[Branding](/en/admin/branding/)) in its own scope, `identity`, as
`brands/logo-{sha256}.{png|jpg}`: named by the hash of the normalised file, so uploading the same
logo twice is one object, and **never deleted**, since a document may carry one. Without an object
store brands work in full, without logos; the **Brand** screen says why the upload controls are
off. The one-time import of `APP_LOGO_URL` needs a store too, and raises a notice when there is
none. Published logos are served to everyone without a session from
`/api/v1/identity/brand-assets/{sha256}.{png|jpg}`, which answers only for a file a published
brand names.

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

It is the path under `STORAGE_FS_ROOT` for `fs` and the object key in the bucket for
`s3`, behind `STORAGE_S3_PREFIX` and a `/` when a prefix is set.

**There is no tenant segment.** The .NET implementation's physical key was
`tenants/{tenant-id}/{scope}/{relative-key}`; this is a single-tenant application and
the tenant segment is gone along with the rest of tenancy. Nothing resolves a tenant,
and no storage operation fails for want of one.

Scope names are canonical lowercase ASCII `[a-z0-9-]`, at most 64 characters, with no
slashes or dots. Communications uses the scope `communications`, so one of its
attachments lands at `communications/<relative-key>`. Expenses uses the scope
`expenses`: a receipt's relative key is `receipts/<entryId>/<uuid>`, so its
physical key is `expenses/receipts/<entryId>/<uuid>`. Invoices uses the scope
`invoices`: an issued document's PDF has the relative key
`documents/<id>/<number>-<sha256>.pdf`, so its physical key is
`invoices/documents/<id>/<number>-<sha256>.pdf` — stored once after the issue
commits and never deleted or overwritten, since an issued document is bookkeeping
material ([Invoices](/en/reference/invoices/#the-pdf)). Sending a document as EHF adds
three more beside it, kept the same way: the EHF as submitted,
`documents/<id>/<number>-<sha256>.xml` (`application/xml`), stored once by its hash when
the document is sent; and, once the access point confirms delivery, its receipt
`documents/<id>/<number>-<transmission>-receipt.json` and the copy it delivered
`documents/<id>/<number>-<transmission>-delivered.xml`
([E-invoicing](/en/admin/e-invoicing/#the-objects-written)). An imported bank file is
kept as it was uploaded, under `bank-files/<sha256>.ocr` (an OCR giro file) or
`bank-files/<sha256>.xml` (a camt.054 file) — keyed by the hash of its bytes, stored once
before the import's transaction and never deleted or overwritten, since it is the
documentation of the payments booked from it (bokføringsloven § 10); an import that
fails after storing it leaves an object the next import of the same bytes reuses
([Payments from the bank](/en/admin/payments/)). A batch the bank feed fetched is kept the
same way, as the bank sent its incoming payments, under `bank-files/<sha256>.json` — a JSON
array of the transactions kept, never the payments out, card purchases or internal
transfers. The bank feed needs the object store: without one, or with one that fails,
every read of an account fails and is tried again, and nothing is registered
([The bank feed](/en/admin/payments/#the-bank-feed)). A reminder letter's PDF is stored
under `reminders/<invoiceId>/<reminderId>-<sentOn>-<sha256>.pdf` — keyed by the hash of
its bytes, put only when nothing is there yet, before the letter is mailed or when it is
printed, and never deleted or overwritten: a letter rendered again with other figures
(a retry the same day after a payment) gets a key of its own, and the letter's row names
the object it went with ([the worker](/en/reference/invoices/#the-worker)). A paper letter's
PDF is stored the same way when its print batch is printed, under its posting day; a
reprint prints it again under a new key, and the batch's combined PDF is rendered from
the letters' rows each time it is downloaded, never stored; a letter whose PDF could not
be stored when it was printed is stored when the batch is next posted or downloaded, and
each of those stops at the first letter the store fails, so an outage holds a request for
one store timeout
([paper and posting](/en/reference/invoices/#paper-and-posting)). Staff uses the scope
`staff`: a document's relative key is `people/<personId>/<uuid>`, so its physical key is
`staff/people/<personId>/<uuid>` ([Staff](/en/reference/staff/#documents)). Customers uses the
scope `customers`: an attachment's relative key is `attachments/<uuid>`, so its physical key
is `customers/attachments/<uuid>` — a key that names no customer, since a merge moves the
attachment's row to the surviving customer and the store has no rename
([Customers](/en/reference/customers/#attachments)). Products uses the scope
`products-images`: a product image's relative key is `<productId>/<uuid>` and its
thumbnail's `<productId>/<uuid>-thumb`, so the physical keys are
`products-images/<productId>/<uuid>` and `products-images/<productId>/<uuid>-thumb`
([Products](/en/reference/products/#images)). Inventory uses the scope `inventory-lots`: a
lot's file has the relative key `lots/<uuid>`, so its physical key is
`inventory-lots/lots/<uuid>` — a key that names no lot and is never made from the file's name
([Inventory](/en/reference/inventory/#a-lots-files)). Manufacturing uses the scope `manufacturing-quality`: a check's photo has the relative
key `checks/<uuid>`, so its physical key is `manufacturing-quality/checks/<uuid>` — a key
that names no work order ([Manufacturing](/en/reference/manufacturing/#photos)). A scoped store refuses a
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
request that caused it. See [Expenses](/en/reference/expenses/#receipts) for the upload rules
(types, size, count, sniffing) and the rate limit.

**Staff's documents, specifically.** A person's archive: contracts, attests,
certificates, correspondence, health documents and the company's own categories, each a
PDF, JPEG, PNG or HEIC of at most 10 MiB. A document is read by the person, by
`staff:manage` (health documents excepted) and by `staff:personal-data`, always through
Staff's own download endpoint. The object is written before its row and removed again
if the row is refused. A delete — by hand, or by the retention erase — removes the row
and records the object in Staff's ledger of objects to remove (`staff.object_deletions`)
in one transaction; the object is then removed, and a removal the store refuses stays in
the ledger for Staff's retention worker to finish, so a document that has to be erased
is never left to chance.
The ledger rows a store outage leaves are what to watch: they are the objects Staff
still owes a removal. Switching the module off leaves its objects and its ledger rows
where they are, untouched, until it is switched on again. See
[Staff](/en/reference/staff/#documents) for the upload rules and the rate limit.

**Customers' attachments, specifically.** A customer's documents — a contract, an NDA,
the correspondence behind a timeline entry — each a PDF, JPEG, PNG or HEIC of at most 10
MiB, at most 200 a customer. An attachment is read with `customers:timeline-view` and
written with `customers:timeline-manage`, always through Customers' own download endpoint.
The object is written before its row and removed again if the row is refused. A delete —
by hand, or by a private person's anonymisation — removes the row and records the object
in Customers' ledger of objects to remove (`customers.object_deletions`) in one
transaction; the object is then removed, and a removal the store refuses stays in the
ledger for the `customers-anonymisation` worker, which drains it after each cycle (passes
of a hundred rows, for at most ten minutes), retried until the store confirms, so a file that has to be erased is never left to chance. A merge
moves the rows and leaves the objects where they are. The ledger rows a store outage
leaves are what to watch: they are the objects Customers still owes a removal, named in
the worker's log; with `CUSTOMERS_ANONYMISATION_ENABLED=0` they wait, as scheduled
anonymisations do. See [Customers](/en/reference/customers/#attachments) for the upload
rules and the rate limit.

**Inventory's lot files, specifically.** A lot's certificates, analyses and photos — each a
PDF, JPEG or PNG of at most 20 MiB, at most 20 a lot. A file is read with both of
Inventory's views and written with `inventory:lots-manage`, always through Inventory's own
download endpoint. The object is written before its row and removed again if the row is
refused. A delete removes the row and records the object in Inventory's ledger of objects to
remove (`inventory.object_deletions`) in one transaction; the object is then removed, and a
removal the store refuses stays in the ledger for the `inventory.object-deletions` worker,
which runs every five minutes in worker mode and retries each object — two seconds after the
first failure, doubling, at most an hour apart — until the store confirms. The ledger rows a
store outage leaves are what to watch: they are the objects Inventory still owes a removal,
named in the worker's log. Without object storage configured, an upload or a download
answers 503 `storage_not_configured` and nothing is recorded.

## Downloads

Downloads always stream through an authorized application endpoint.

**The storage contract has no presigned-URL operation**, and no provider ever
redirects a caller to a direct storage URL. Communications' download endpoint
authorizes the attachment, loads it through the scoped store, and returns the bytes
with the stored content type and a sanitized filename; the `downloadPath` field in
its DTOs is that same-origin application endpoint, never a storage location. See
[Communications](/en/reference/communications/) for the attachment lifecycle.

Expenses' `GET /attachments/{id}` follows the same shape: it authorizes the
receipt through the expense it belongs to, loads it through the module's own
`expenses`-scoped store, and streams the bytes back with the content type they
were sniffed as on upload and the file name as it was given — never a storage
location of any kind. See [Expenses](/en/reference/expenses/#receipts).

Staff's `GET /documents/{id}` does the same through its `staff`-scoped store, with
the document's own visibility; a document whose object cannot be read answers 503
`storage_unavailable`, not 404. See [Staff](/en/reference/staff/#reading-changing-and-removing).

Customers' `GET /customers/{id}/attachments/{attachmentId}` does the same through its
`customers`-scoped store, under the timeline's read permission, with the same headers and
the same 503 for an object that cannot be read. See
[Customers](/en/reference/customers/#attachments).

Inventory's `GET /lots/{id}/attachments/{attachmentId}` does the same through its
`inventory-lots`-scoped store, under both of Inventory's views, with the same headers. See
[Inventory](/en/reference/inventory/#a-lots-files).

Manufacturing's `GET /work-orders/{id}/photos/{photoId}` does the same through its
`manufacturing-quality`-scoped store, under Use Manufacturing, with the same headers; it
is shown in place, as a picture. An upload no check's result names after a day is removed
by the `manufacturing.object-deletions` worker. See
[Manufacturing](/en/reference/manufacturing/#photos).
