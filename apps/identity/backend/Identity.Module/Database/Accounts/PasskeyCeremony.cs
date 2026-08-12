namespace Vantigo.Identity.Database.Accounts;

/// <summary>
/// Server-side binding for a native Identity WebAuthn ceremony. The native
/// Identity state is deliberately stored here rather than trusted from the
/// browser, and each row is consumed before the ceremony is evaluated.
/// </summary>
public sealed class PasskeyCeremony
{
    public Guid Id { get; set; } = Guid.NewGuid();
    public Guid? UserId { get; set; }
    public required string Kind { get; set; }
    public required string State { get; set; }
    public string? CredentialName { get; set; }
    public string? ClientAddress { get; set; }
    public DateTimeOffset ExpiresAt { get; set; }
    public bool Consumed { get; set; }
}