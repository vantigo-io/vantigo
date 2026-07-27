using Vantigo.Customers.Api.Domain.Contacts;
using Vantigo.Customers.Api.Endpoints.Contacts.Dtos;

namespace Vantigo.Customers.Api.Endpoints.Customers.Contacts.Dtos;

/// <summary>
/// The shared representation of a customer-contact association returned by the
/// customer contact endpoints: the contact itself plus the role and optional
/// connection-specific contact details.
/// </summary>
internal readonly record struct CustomerContactResponse
{
    public required ContactResponse Contact { get; init; }
    public required string Role { get; init; }
    public string? Phone { get; init; }
    public string? Email { get; init; }

    /// <summary>
    /// Maps a domain <see cref="CustomerContact"/> (with its contact loaded) to its
    /// API representation.
    /// </summary>
    internal static CustomerContactResponse FromDomain(CustomerContact association) => new()
    {
        Contact = ContactResponse.FromDomain(association.Contact),
        Role = association.Role,
        Phone = association.Phone is { } phone ? (string)phone : null,
        Email = association.Email is { } email ? (string)email : null,
    };
}