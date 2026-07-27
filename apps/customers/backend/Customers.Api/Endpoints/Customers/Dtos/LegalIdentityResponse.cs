using Vantigo.Customers.Api.Domain.Customers.ValueObjects;

namespace Vantigo.Customers.Api.Endpoints.Customers.Dtos;

/// <summary>
/// The shared representation of a customer's legal identity returned by the customer endpoints.
/// </summary>
internal readonly record struct LegalIdentityResponse
{
    public required string Country { get; init; }
    public required string Type { get; init; }
    public required string Id { get; init; }
    public required string Name { get; init; }
    public required string Source { get; init; }

    /// <summary>
    /// Maps a domain <see cref="LegalIdentity"/> to its API representation.
    /// </summary>
    internal static LegalIdentityResponse FromDomain(LegalIdentity identity) => new()
    {
        Country = identity.Country,
        Type = identity.Type,
        Id = identity.Id,
        Name = identity.Name,
        Source = identity.Source,
    };
}
