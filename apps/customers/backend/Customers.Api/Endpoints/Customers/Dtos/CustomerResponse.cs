using Vantigo.Customers.Api.Domain.Customers;

namespace Vantigo.Customers.Api.Endpoints.Customers.Dtos;

/// <summary>
/// The shared representation of a customer returned by the customer endpoints.
/// </summary>
internal readonly record struct CustomerResponse
{
    public required int Id { get; init; }
    public required string Name { get; init; }
    public LegalIdentityResponse? Identity { get; init; }

    /// <summary>
    /// Maps a domain <see cref="Customer"/> to its API representation.
    /// </summary>
    internal static CustomerResponse FromDomain(Customer customer) => new()
    {
        Id = customer.Id,
        Name = customer.Name,
        Identity = customer.Identity is { } identity
            ? LegalIdentityResponse.FromDomain(identity)
            : null,
    };
}
