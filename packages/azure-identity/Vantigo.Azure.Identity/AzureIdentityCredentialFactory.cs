namespace Vantigo.Azure.Identity;

/// <summary>
/// Creates the same <see cref="global::Azure.Core.TokenCredential"/> implementation
/// Vantigo registers for process-wide Azure SDK authentication. Exposed so
/// callers that need a credential instance before the DI container is built
/// (for example, Data Protection key wrapping, which is configured eagerly
/// during <c>ConfigureServices</c>) stay consistent with
/// <see cref="AzureIdentityServiceCollectionExtensions.AddVantigoAzureIdentity"/>
/// instead of hand-rolling credential resolution.
/// </summary>
public static class AzureIdentityCredentialFactory
{
    /// <summary>
    /// Creates a new <see cref="global::Azure.Identity.DefaultAzureCredential"/>.
    /// Construction does not acquire a token or contact Azure; the credential
    /// only authenticates when a client actually uses it.
    /// </summary>
    public static global::Azure.Core.TokenCredential Create() => new global::Azure.Identity.DefaultAzureCredential();
}