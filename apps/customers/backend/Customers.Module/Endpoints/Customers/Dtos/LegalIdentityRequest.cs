namespace Vantigo.Customers.Endpoints.Customers.Dtos;

/// <summary>
/// The shared representation of a customer's legal identity accepted by the customer
/// create and update endpoints.
/// </summary>
internal readonly record struct LegalIdentityRequest
{
    public required string Country { get; init; }
    public required string Type { get; init; }
    public required string Id { get; init; }
    public required string Name { get; init; }
    public required string Source { get; init; }
}