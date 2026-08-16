# Object storage

Application modules reference only `Vantigo.Storage.Abstractions`, define a typed
marker implementing `IStorageScope`, and inject `IObjectStore<TheirScope>`. The
`Vantigo.Storage` implementation package is registered by the host with
`AddVantigoObjectStorage`; it resolves the configured provider and prefixes every
key with the current tenant and typed scope before reaching S3, Azure Blob, or
local storage.

Vantigo uses one centrally configured object-storage provider. Configure it with
`Storage__Provider` as `s3`, `azure-blob`, or `local`, and set the matching
`Storage__Authentication` value. If `Storage__Provider` is omitted, the host starts
with storage fail-closed; an operation reports that storage is not configured.

## S3 and MinIO

S3 uses explicit access-key authentication. The service URL is used for MinIO and
other S3-compatible services; it can be omitted when normal AWS endpoint resolution
is desired.

```text
STORAGE__PROVIDER=s3
STORAGE__AUTHENTICATION=access-key
STORAGE__S3__BUCKET_NAME=vantigo-objects
STORAGE__S3__SERVICE_URL=http://minio:9000
STORAGE__S3__ACCESS_KEY=<secret>
STORAGE__S3__SECRET_KEY=<secret>
STORAGE__S3__REGION=us-east-1
STORAGE__S3__FORCE_PATH_STYLE=true
```

## Azure Blob

Required settings are `STORAGE__AZURE_BLOB__ACCOUNT_NAME` and
`STORAGE__AZURE_BLOB__CONTAINER_NAME`. `Azure_Blob` is the canonical environment
hierarchy; the legacy compact `AzureBlob` hierarchy is accepted only as a fallback.

| Authentication | Required setting | Credential behavior |
| --- | --- | --- |
| `azure-identity` | `AZURE__IDENTITY__ENABLED` is omitted or `true` | Uses the host's global `DefaultAzureCredential`; assign **Storage Blob Data Contributor** |
| `connection-string` | `CONNECTION_STRING` | Uses the supplied connection string |
| `sas` | `SAS_TOKEN` | Uses the account/container SAS; a leading `?` is accepted |

Do not provide connection strings, SAS tokens, or account keys to azure-identity
configuration. Container provisioning is disabled by default. Set
`STORAGE__AZURE_BLOB__CREATE_CONTAINER_IF_MISSING=true` only when the application
identity is intentionally allowed to create the container. Otherwise the configured
container is verified and must already exist. Secrets must be supplied through a
secret manager or environment injection, never committed to configuration files.

## Local filesystem

```text
STORAGE__PROVIDER=local
STORAGE__AUTHENTICATION=none
STORAGE__LOCAL__ROOT_PATH=/var/lib/vantigo/objects
```

The root must be an absolute, writable path that exists or can be created during
initialization. It must not be a symlink/reparse point and, on Unix, must not be
group/world writable by default. The app identity must exclusively own the root;
do not use a shared writable volume. Set
`STORAGE__LOCAL__ALLOW_INSECURE_ROOT_FOR_DEVELOPMENT=true` only for intentional
local development. The setting is accepted only when the actual host environment
is Development; if it is true in any other environment, startup/storage
initialization fails with a configuration error. It never relaxes Unix root or
directory permission checks outside Development, and defaults to false. Keys are
checked for containment, traversal, symlink/reparse components, and unsafe
characters before every operation. Writes use a temporary file under the root
and an atomic replacement; physical paths are never returned by the API.
This is defense-in-depth, not a claim of cross-platform TOCTOU-proof `openat`
semantics: protect the root with deployment ownership and filesystem policy.

## Tenant isolation, module scopes, and downloads

All objects are tenant-prefixed automatically in both single-tenant and
multi-tenant installations. The physical provider key is always:

```text
tenants/{tenant-id}/{scope-name}/{relative-key}
```

`{tenant-id}` is the tenant UUID and `{scope-name}` is the validated marker-type
scope. Single-tenant installations use an automatically provisioned `Default`
tenant; multi-tenant installations use the tenant resolved for the current
request or background-job iteration. If no tenant is resolved, storage fails
closed before calling the provider. This is a logical object key, not a provider
URL, and the storage contract never exposes provider URLs.

Modules define a marker type implementing `IStorageScope` from
`Vantigo.Storage.Abstractions`, for example `CommunicationsStorageScope : IStorageScope`
with `Name => "communications"`,
and inject `IObjectStore<CommunicationsStorageScope>`. They pass only relative
keys. The provider key is constructed as
`tenants/<tenant-id>/communications/<relative-key>`, with
traversal, URL-encoding, absolute-path, backslash, control-character, and
duplicate-prefix forms rejected. Scope names are canonical lowercase ASCII
`[a-z0-9-]` names without slashes or dots. Never construct tenant or scope prefixes
manually; the unscoped provider and string scope factory are not application
contracts.

Downloads always stream through an application endpoint after authorization. The
storage contract intentionally has no presigned URL operation, and providers never
redirect callers to direct S3, Azure, or local filesystem URLs.
