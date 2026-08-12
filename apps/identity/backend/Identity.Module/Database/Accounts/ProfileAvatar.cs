namespace Vantigo.Identity.Database.Accounts;

/// <summary>
/// Private profile avatar storage kept separate from the user row so normal
/// cookie/user lookups never hydrate the opaque blob.
/// </summary>
public sealed class ProfileAvatar
{
    public required Guid UserId { get; set; }
    public required byte[] Data { get; set; }
    public required string ContentType { get; set; }
    public DateTimeOffset UpdatedAt { get; set; }
    public long Version { get; set; }
}