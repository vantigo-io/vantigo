namespace Vantigo.Customers.Api.Domain.Timeline;

/// <summary>
/// An immutable full snapshot of a timeline entry at a particular revision.
/// </summary>
public sealed class CustomerTimelineEntryRevision
{
    public int Id { get; set; }
    public int CustomerTimelineEntryId { get; set; }
    public CustomerTimelineEntry Entry { get; set; } = null!;

    public int RevisionNumber { get; set; }
    public string Provenance { get; set; } = TimelineProvenance.Manual;
    public string Producer { get; set; } = "customers.api";
    public string EventType { get; set; } = null!;
    public DateOnly OccurredOn { get; set; }
    public DateTimeOffset? OccurredAt { get; set; }
    public string? Summary { get; set; }
    public string? Note { get; set; }
    public string? SourceUrl { get; set; }
    public string? PayloadJson { get; set; }
    public int PayloadVersion { get; set; }
    public string State { get; set; } = TimelineState.Active;
    public string ActorKind { get; set; } = TimelineActorKind.Unattributed;
    public string? ActorDisplay { get; set; }
    public DateTimeOffset CreatedAt { get; set; }
    public DateTimeOffset? DeletedAt { get; set; }
}
