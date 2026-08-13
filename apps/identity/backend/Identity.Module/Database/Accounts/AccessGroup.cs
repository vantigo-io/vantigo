namespace Vantigo.Identity.Database.Accounts;

public enum AccessGroupSource
{
    Local,
    Scim
}

/// <summary>Deployment-scoped group used for durable membership and role mappings.</summary>
public sealed class AccessGroup
{
    public Guid Id { get; set; } = Guid.NewGuid();
    public Guid? ScimConnectionId { get; set; }
    public required string DisplayName { get; set; }
    public AccessGroupSource Source { get; set; }
    public string? ExternalId { get; set; }
    public bool IsActive { get; set; }
    public DateTimeOffset CreatedAt { get; set; }
    public DateTimeOffset UpdatedAt { get; set; }
    public required string ConcurrencyStamp { get; set; }
}