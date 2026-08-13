namespace Vantigo.Identity.Database.Accounts;

public enum ScimProvisioningMode
{
    Authoritative,
    Additive
}

public sealed class ScimConnection
{
    public Guid Id { get; set; } = Guid.NewGuid();
    public Guid FederationConnectionId { get; set; }
    public ScimProvisioningMode Mode { get; set; } = ScimProvisioningMode.Authoritative;
    public bool IsEnabled { get; set; }
    public int TokenVersion { get; set; } = 1;
    public DateTimeOffset CreatedAt { get; set; }
    public DateTimeOffset UpdatedAt { get; set; }
    public DateTimeOffset? LastRotatedAt { get; set; }
    public DateTimeOffset? LastRevokedAt { get; set; }
    public required string ConcurrencyStamp { get; set; }
}

public sealed class ScimBearerToken
{
    public Guid Id { get; set; } = Guid.NewGuid();
    public Guid ScimConnectionId { get; set; }
    public int Version { get; set; }
    public required string TokenHash { get; set; }
    public DateTimeOffset CreatedAt { get; set; }
    public DateTimeOffset? ExpiresAt { get; set; }
    public DateTimeOffset? RevokedAt { get; set; }
    public bool IsCurrent { get; set; }
}

public enum ScimLifecycleOverride
{
    ForceEnable,
    ForceDisable
}

public sealed class ScimUserMapping
{
    public Guid Id { get; set; } = Guid.NewGuid();
    public Guid ScimConnectionId { get; set; }
    public Guid UserId { get; set; }
    public required string ResourceId { get; set; }
    public required string ExternalId { get; set; }
    public required string UserName { get; set; }
    public bool UpstreamActive { get; set; }
    public ScimLifecycleOverride? LifecycleOverride { get; set; }
    public string? LifecycleOverrideReason { get; set; }
    public string? SourceProfileJson { get; set; }
    public DateTimeOffset LastSynchronizedAt { get; set; }
    public int Version { get; set; } = 1;
    public required string ETag { get; set; }
    public DateTimeOffset CreatedAt { get; set; }
    public DateTimeOffset UpdatedAt { get; set; }
}