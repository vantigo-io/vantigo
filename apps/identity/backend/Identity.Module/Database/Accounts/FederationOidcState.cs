namespace Vantigo.Identity.Database.Accounts;

/// <summary>
/// Deployment-shared one-time state for a dynamic federation flow. Only a
/// digest of the opaque state identifier is persisted; the protected state
/// contains the nonce and PKCE verifier.
/// </summary>
public sealed class FederationOidcState
{
    public Guid StateId { get; set; }
    public required string StateHash { get; set; }
    public DateTimeOffset IssuedAt { get; set; }
    public DateTimeOffset ExpiresAt { get; set; }
    public DateTimeOffset? ConsumedAt { get; set; }
}