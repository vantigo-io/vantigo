# Data Protection key wrapping

ASP.NET Core Data Protection keys are persisted as XML in the shared PostgreSQL
database (see [Identity, authentication and deployment](customers-authentication.md#data-protection-keys)
for the PostgreSQL configuration). By default that XML is stored unwrapped: a
PostgreSQL dump then contains both encrypted payloads (mailbox/SMTP
credentials protected by `EmailSender`, antiforgery tokens, protected Identity
tokens) *and* the key material needed to decrypt them, and may enable
auth-cookie/token forgery.

Vantigo supports wrapping the persisted key ring with an Azure Key Vault key
so a database dump alone is no longer sufficient to decrypt protected
payloads. PostgreSQL remains the key **repository** (required so all replicas
share one key ring); Key Vault only wraps the key material stored there.

## Configuration

| Variable | Purpose | Default |
| --- | --- | --- |
| `DataProtection__KeyVaultKeyUri` | Azure Key Vault key identifier used to wrap the key ring, for example `https://vantigo.vault.azure.net/keys/dataprotection/<version>` | unset |
| `DataProtection__AllowUnwrappedKeys` | Escape hatch that allows an unwrapped key ring outside Development; see [Production requirement](#production-requirement) | `false` |

When set, Vantigo calls `ProtectKeysWithAzureKeyVault` using the process-wide
Azure credential (`Vantigo.Azure.Identity`, a `DefaultAzureCredential`); see
[Azure identity](azure-identity.md) for how that credential is resolved
(managed identity in Azure, the standard `AZURE_CLIENT_ID`/`AZURE_TENANT_ID`/
`AZURE_CLIENT_SECRET`/workload-identity federation locally or in CI). The
identity used needs Key Vault permission to wrap and unwrap with the
configured key (the built-in **Key Vault Crypto User** role, or the
`keys/wrapKey`, `keys/unwrapKey`, and `keys/get` data-plane permissions).

Constructing the credential does not contact Azure or acquire a token; the
first failure surfaces when a key is actually wrapped or unwrapped.

### Production requirement

Outside the Development environment, startup fails unless
`DataProtection__KeyVaultKeyUri` is configured **or**
`DataProtection__AllowUnwrappedKeys=true` is set. In Development, leave both
unset; keys are then persisted unwrapped so local setup does not require a
Key Vault.

This is deliberately fail-closed by default, but — unlike
`EmailOptionsValidator` or `BootstrapSecretOptionsValidator` in
`packages/configuration`, which demand a value any operator can supply
locally (an SMTP host, a random string) — a Key Vault key requires an actual
Azure subscription and a provisioned vault. Vantigo does not yet provision
Azure infrastructure ([issue #8](https://github.com/vantigo-io/vantigo/issues/8)),
so today most deployments, including the documented
[Docker Compose stack](../deploy/compose/README.md), have no Key Vault to
point at. A validator with no escape hatch would make those deployments
impossible to start at all.

The precedent this follows instead is `Tenancy:AllowUnsafeMultiTenant` (see
[Tenancy and tenant isolation](tenancy.md#multi-tenant-mode-is-not-production-ready)):
a loudly-named, documented flag that must be set *deliberately*, so an
unwrapped key ring can never happen *by accident* in a real deployment, but a
deployment without Key Vault access can still start knowingly.

```dotenv
DataProtection__AllowUnwrappedKeys=true
```

Setting this accepts that a database dump exposes both encrypted secrets and
the keys that decrypt them, exactly as described at the top of this document.
Unset it — and configure `DataProtection__KeyVaultKeyUri` instead — the moment
a Key Vault key is available for the deployment.

## Rotation

Two independent rotations are involved:

1. **Data Protection's own key rotation.** ASP.NET Core automatically
   generates a new Data Protection key roughly every 90 days (the default key
   lifetime) regardless of wrapping. Each key is individually wrapped with
   whatever Key Vault key version `DataProtection__KeyVaultKeyUri` resolves to
   at the moment that key is created. This rotation needs no operator action.

2. **The Key Vault key itself.** Rotating (creating a new version of) the Key
   Vault key does not retroactively rewrap already-persisted Data Protection
   keys — each stored key remembers the specific Key Vault key **version** it
   was wrapped with, and unwrapping always requests that exact version.

   Because of this:
   - **Never delete or disable an old Key Vault key version** while any
     Data Protection key wrapped with it might still be in use (Data
     Protection keys are retired, not deleted, after they expire, and old
     keys must remain unwrappable to decrypt payloads protected while they
     were active — including mailbox credentials that are not re-protected
     until they are next saved).
   - After rotating the Key Vault key, either point
     `DataProtection__KeyVaultKeyUri` at the new version explicitly, or leave
     it unversioned so Key Vault resolves the latest enabled version
     automatically; newly generated Data Protection keys pick up the new
     version, older keys stay wrapped (and readable) with the version they
     were created under.
   - Prefer Key Vault's own scheduled rotation policy over manual rotation so
     old versions are retained automatically per the vault's retention
     settings.

## Recovery and the cost of losing the key

**Losing the Key Vault key (or all of its versions) permanently loses every
Data Protection payload wrapped with it.** There is no fallback: the XML
stored in PostgreSQL is ciphertext without it. Concretely, on loss:

- Every issued authentication cookie, antiforgery token, and protected
  Identity token becomes invalid; all users are signed out and must
  re-authenticate.
- Every mailbox/SMTP credential `EmailSender` protected with the key ring
  becomes unrecoverable and must be **manually reconfigured** from a
  trusted source (the plaintext is not recoverable from the database).
- Any other payload protected through the shared key ring is lost in the
  same way.

To avoid this:

- Keep Key Vault **soft-delete** enabled (the Azure default for new vaults)
  and enable **purge protection**, so an accidental delete is recoverable
  for the vault's retention period instead of immediate and permanent.
- Restrict who can delete keys or purge the vault via Azure RBAC; wrapping
  only needs `keys/wrapKey`, `keys/unwrapKey`, and `keys/get`, not delete or
  purge permissions.
- Take periodic offline backups of the key (`az keyvault key backup`) and
  store them under equivalent access control, so the key can be restored
  into a new vault if the original vault itself is lost.

If the key is deleted while soft-delete/purge-protection is enabled, recover
it (`az keyvault key recover`) before it is purged; no other recovery path
exists once it is gone.

## Verification note

This was implemented and unit-tested against the dependency-injection wiring
only (configuration binding, the Production-without-wrapping startup
validator and its `AllowUnwrappedKeys` escape hatch, and that
`ProtectKeysWithAzureKeyVault` is invoked and sets
`KeyManagementOptions.XmlEncryptor`). There is no Azure subscription available
in this environment, so wrapping/unwrapping against a real Key Vault key,
managed-identity authentication, and Key Vault RBAC were **not** exercised
end-to-end; verify those against a real vault before relying on this in
production.
