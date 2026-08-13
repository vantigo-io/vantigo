namespace Vantigo.Identity.Database.Accounts;

/// <summary>Maps a group to an existing application role.</summary>
public sealed class AccessGroupRoleMapping
{
    public Guid GroupId { get; set; }
    public Guid RoleId { get; set; }
    public AccessGroupSource Source { get; set; }
    public DateTimeOffset CreatedAt { get; set; }
}