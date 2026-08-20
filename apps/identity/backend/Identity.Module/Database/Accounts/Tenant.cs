namespace Vantigo.Identity.Database.Accounts;

/// <summary>The control-plane tenant.</summary>
public sealed class Tenant
{
    public Guid Id { get; set; } = Guid.NewGuid();

    public required string Name { get; set; }

    public required string Slug { get; set; }

    public TenantStatus Status { get; set; } = TenantStatus.Active;

    public string[] EnabledModules { get; set; } = [];

    public DateTimeOffset CreatedAtUtc { get; set; }
}

/// <summary>The lifecycle state of a tenant.</summary>
public enum TenantStatus
{
    Active,
    Suspended,
}

/// <summary>Associates an application user with a tenant.</summary>
public sealed class TenantMembership
{
    public Guid UserId { get; set; }

    public Guid TenantId { get; set; }

    public DateTimeOffset CreatedAtUtc { get; set; }
}