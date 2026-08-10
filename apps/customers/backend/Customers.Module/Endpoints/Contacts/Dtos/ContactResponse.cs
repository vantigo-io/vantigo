using Vantigo.Customers.Domain.Contacts;

namespace Vantigo.Customers.Endpoints.Contacts.Dtos;

/// <summary>
/// The shared representation of a contact returned by the contact endpoints. All
/// name parts are returned raw — composing a display name is left to the clients.
/// </summary>
internal readonly record struct ContactResponse
{
    public required int Id { get; init; }
    public required string FirstName { get; init; }
    public required string LastName { get; init; }
    public string? MiddleName { get; init; }
    public string? Prefix { get; init; }
    public string? Suffix { get; init; }
    public string? Phone { get; init; }
    public string? Email { get; init; }

    /// <summary>
    /// Maps a domain <see cref="Contact"/> to its API representation.
    /// </summary>
    internal static ContactResponse FromDomain(Contact contact) => new()
    {
        Id = contact.Id,
        FirstName = contact.FirstName,
        LastName = contact.LastName,
        MiddleName = contact.MiddleName is { } middleName ? (string)middleName : null,
        Prefix = contact.Prefix is { } prefix ? (string)prefix : null,
        Suffix = contact.Suffix is { } suffix ? (string)suffix : null,
        Phone = contact.Phone is { } phone ? (string)phone : null,
        Email = contact.Email is { } email ? (string)email : null,
    };
}