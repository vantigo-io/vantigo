namespace Vantigo.Identity.Database.Accounts;

public enum AccessGroupMembershipOverride
{
    ForceMember,
    ForceNonMember
}

/// <summary>Observed and locally overridden membership for a group and user.</summary>
public sealed class AccessGroupMembership
{
    public Guid GroupId { get; set; }
    public Guid UserId { get; set; }
    public Guid? TenantId { get; set; }
    public AccessGroupSource Source { get; set; }
    public bool IsUpstreamPresent { get; set; }
    public AccessGroupMembershipOverride? Override { get; set; }
    public DateTimeOffset UpdatedAt { get; set; }
}