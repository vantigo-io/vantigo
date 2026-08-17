namespace Vantigo.Identity.Database.Accounts;

/// <summary>Identity role metadata owned by the RBAC domain, separate from the Identity role type.</summary>
public sealed class RoleMetadata
{
    public Guid RoleId { get; set; }
    public required string DisplayName { get; set; }
    public required string Description { get; set; }
    public bool IsSystem { get; set; }
    public bool IsBuiltIn { get; set; }
    public Guid? StewardUserId { get; set; }
    public required string ConcurrencyStamp { get; set; }
}

public sealed class TenantOffboardingState
{
    public Guid TenantId { get; set; }
    public Guid ExportId { get; set; }
    public required string PurgeToken { get; set; }
    public DateTimeOffset RequestedAtUtc { get; set; }
    public DateTimeOffset? PurgeRequestedAtUtc { get; set; }
}

public sealed class RolePermission
{
    public Guid RoleId { get; set; }
    public required string PermissionKey { get; set; }
}

public sealed class AuthorizationDelegation
{
    public Guid Id { get; set; } = Guid.NewGuid();
    public Guid GranteeUserId { get; set; }
    public Guid CreatedByUserId { get; set; }
    public DateTimeOffset? ExpiresAt { get; set; }
    public DateTimeOffset? RevokedAt { get; set; }
    public required string ConcurrencyStamp { get; set; }
    public bool CanCreateRoles { get; set; }
}

public sealed class AuthorizationDelegationPermission
{
    public Guid DelegationId { get; set; }
    public required string PermissionKey { get; set; }
}

public sealed class AuthorizationDelegationRole
{
    public Guid DelegationId { get; set; }
    public Guid RoleId { get; set; }
}

public sealed class AuthorizationAuditEvent
{
    public long Id { get; set; }
    public Guid? ActorUserId { get; set; }
    public Guid? TargetUserId { get; set; }
    public Guid? TargetRoleId { get; set; }
    public required string Action { get; set; }
    public required string Details { get; set; }
    public string BeforeJson { get; set; } = "{}";
    public string AfterJson { get; set; } = "{}";
    public string CorrelationId { get; set; } = "";
    public bool MfaAuthenticated { get; set; }
    public DateTimeOffset OccurredAt { get; set; }
}