namespace Vantigo.Customers.Endpoints.Customers.Dtos;

/// <summary>
/// Non-sensitive customer projection. It intentionally contains no legal identity,
/// contact, association, timeline payload, note, actor, source, or revision data.
/// </summary>
internal readonly record struct SafeCustomerResponse
{
    public required int Id { get; init; }
    public required long CustomerNumber { get; init; }
    public required string Name { get; init; }
    public required SafeTimelineSummary TimelineSummary { get; init; }
}

internal readonly record struct SafeTimelineSummary
{
    public required int EntryCount { get; init; }
    public DateOnly? LatestOccurredOn { get; init; }
}