namespace Vantigo.Identity.Database.Accounts;

public sealed class ScimConnection
{
    public static readonly Guid StaticId = new("7f6b7f8a-7c7e-4c19-8f06-6c64d3b7f4d2");
    public Guid Id { get; set; } = Guid.NewGuid();
    public DateTimeOffset CreatedAt { get; set; }
    public DateTimeOffset UpdatedAt { get; set; }
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
    public string? SourceProfileJson { get; set; }
    public DateTimeOffset LastSynchronizedAt { get; set; }
    public int Version { get; set; } = 1;
    public required string ETag { get; set; }
    public DateTimeOffset CreatedAt { get; set; }
    public DateTimeOffset UpdatedAt { get; set; }
}