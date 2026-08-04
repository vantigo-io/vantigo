namespace Vantigo.Customers.Api.Database.Accounts;

/// <summary>
/// A single-use invitation for a local account. The raw token is never stored;
/// <see cref="TokenHash"/> contains its SHA-256 digest.
/// </summary>
public sealed class Invitation
{
    public Guid Id { get; set; } = Guid.NewGuid();

    public required string Email { get; set; }

    public required string NormalizedEmail { get; set; }

    public required string Role { get; set; }

    public string? DisplayName { get; set; }

    public required string TokenHash { get; set; }

    public DateTimeOffset CreatedAt { get; set; }

    public DateTimeOffset ExpiresAt { get; set; }

    public DateTimeOffset? RevokedAt { get; set; }

    public DateTimeOffset? AcceptedAt { get; set; }

    public Guid InvitedByUserId { get; set; }
}