namespace Vantigo.Customers.Domain.Timeline;

/// <summary>
/// The current, query-optimized state of one customer timeline entry. Generated entries
/// are immutable; manual entries are changed by appending a revision and replacing this
/// snapshot.
/// </summary>
public sealed class CustomerTimelineEntry
{
    public int Id { get; set; }
    public int CustomerId { get; set; }
    public Customers.Customer Customer { get; set; } = null!;

    public string Provenance { get; set; } = TimelineProvenance.Manual;
    public string Producer { get; set; } = "customers.api";
    public string EventType { get; set; } = null!;
    public DateOnly OccurredOn { get; set; }
    public DateTimeOffset? OccurredAt { get; set; }
    public string? Summary { get; set; }
    public string? Note { get; set; }
    public string? SourceUrl { get; set; }
    public string? PayloadJson { get; set; }
    public int PayloadVersion { get; set; } = 1;

    public int CurrentRevision { get; set; }
    public string State { get; set; } = TimelineState.Active;
    public string ActorKind { get; set; } = TimelineActorKind.Unattributed;
    public string? ActorDisplay { get; set; }
    public DateTimeOffset CreatedAt { get; set; }
    public DateTimeOffset UpdatedAt { get; set; }
    public DateTimeOffset? DeletedAt { get; set; }

    public ICollection<CustomerTimelineEntryRevision> Revisions { get; set; } =
        new List<CustomerTimelineEntryRevision>();
}

public static class TimelineProvenance
{
    public const string Manual = "manual";
    public const string Generated = "generated";
}

public static class TimelineState
{
    public const string Active = "active";
    public const string Deleted = "deleted";
    public const string Voided = "voided";
}

public static class TimelineActorKind
{
    public const string Unattributed = "unattributed";
    public const string System = "system";
}