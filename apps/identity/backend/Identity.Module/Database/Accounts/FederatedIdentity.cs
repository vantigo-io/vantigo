namespace Vantigo.Identity.Database.Accounts;

/// <summary>
/// The deployment connection that owns an external OIDC identity.  Identity's
/// built-in UserLogin row remains the sign-in lookup, while this row makes the
/// connection boundary explicit and prevents two connections using the same
/// issuer and subject from ever being treated as the same login.
/// </summary>
public sealed class FederatedIdentity
{
    public Guid Id { get; set; } = Guid.NewGuid();
    public Guid ConnectionId { get; set; }
    public required string Issuer { get; set; }
    public required string Subject { get; set; }
    public string? DirectoryTenantId { get; set; }
    public Guid? DirectoryObjectId { get; set; }
    public Guid UserId { get; set; }
    public DateTimeOffset CreatedAt { get; set; }
    public DateTimeOffset UpdatedAt { get; set; }
}