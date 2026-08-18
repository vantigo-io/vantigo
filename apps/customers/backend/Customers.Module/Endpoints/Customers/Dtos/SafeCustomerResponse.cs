namespace Vantigo.Customers.Endpoints.Customers.Dtos;

/// <summary>
/// Non-sensitive customer projection. It intentionally contains no contact, association,
/// timeline payload, note, actor, source, or revision data. The legal identity summary is
/// only populated when the caller holds the legal-identity view permission.
/// </summary>
internal readonly record struct SafeCustomerResponse
{
    public required int Id { get; init; }
    public required long CustomerNumber { get; init; }
    public required string Name { get; init; }
    public required string Status { get; init; }
    public required DateTimeOffset CreatedAt { get; init; }
    public required DateTimeOffset UpdatedAt { get; init; }
    public required SafeTimelineSummary TimelineSummary { get; init; }

    /// <summary>
    /// A summary of the customer's legal identity. Null when the customer has no identity
    /// or when the caller lacks the legal-identity view permission.
    /// </summary>
    public SafeCustomerIdentity? Identity { get; init; }
}

internal readonly record struct SafeCustomerIdentity
{
    public required string Country { get; init; }
    public required string Type { get; init; }
    public required string Id { get; init; }
}

internal readonly record struct SafeTimelineSummary
{
    public required int EntryCount { get; init; }
    public DateOnly? LatestOccurredOn { get; init; }
}