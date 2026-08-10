using Vantigo.Customers.Domain.Contacts;
using Vantigo.Customers.Domain.Contacts.Common;

namespace Vantigo.Customers.Endpoints.Contacts.Dtos;

/// <summary>
/// The shared representation of a contact accepted by the contact create and update
/// endpoints. Optional fields treat blank values as absent, so clients can submit
/// empty form fields without tripping validation.
/// </summary>
internal readonly record struct ContactRequest
{
    public required string FirstName { get; init; }
    public required string LastName { get; init; }
    public string? MiddleName { get; init; }
    public string? Prefix { get; init; }
    public string? Suffix { get; init; }
    public string? Phone { get; init; }
    public string? Email { get; init; }

    /// <summary>
    /// Validates the request and parses it into domain values. When one or more
    /// values are invalid, all validation errors are returned at once, keyed by the
    /// camelCase JSON path of the offending field.
    /// </summary>
    internal bool TryParse(out ParsedContact parsed, out Dictionary<string, string[]> errors)
    {
        errors = [];

        if (!PersonName.TryCreate(FirstName, out var firstName, out var firstNameError))
        {
            errors["firstName"] = [firstNameError!];
        }

        if (!PersonName.TryCreate(LastName, out var lastName, out var lastNameError))
        {
            errors["lastName"] = [lastNameError!];
        }

        var middleName = TryParseOptional<PersonName>("middleName", MiddleName, PersonName.TryCreate, errors);
        var prefix = TryParseOptional<NamePart>("prefix", Prefix, NamePart.TryCreate, errors);
        var suffix = TryParseOptional<NamePart>("suffix", Suffix, NamePart.TryCreate, errors);
        var phone = TryParseOptional<PhoneNumber>("phone", Phone, PhoneNumber.TryCreate, errors);
        var email = TryParseOptional<EmailAddress>("email", Email, EmailAddress.TryCreate, errors);

        if (errors.Count > 0)
        {
            parsed = default;
            return false;
        }

        parsed = new ParsedContact(firstName, lastName, middleName, prefix, suffix, phone, email);
        return true;
    }

    private delegate bool TryCreateValue<T>(string? value, out T result, out string? error);

    private static T? TryParseOptional<T>(
        string field,
        string? value,
        TryCreateValue<T> tryCreate,
        Dictionary<string, string[]> errors)
        where T : struct
    {
        if (string.IsNullOrWhiteSpace(value))
        {
            return null;
        }

        if (tryCreate(value, out var result, out var error))
        {
            return result;
        }

        errors[field] = [error!];
        return null;
    }
}

/// <summary>
/// The validated domain values of a <see cref="ContactRequest"/>, ready to be
/// applied to a new or existing <see cref="Contact"/>.
/// </summary>
internal readonly record struct ParsedContact(
    PersonName FirstName,
    PersonName LastName,
    PersonName? MiddleName,
    NamePart? Prefix,
    NamePart? Suffix,
    PhoneNumber? Phone,
    EmailAddress? Email)
{
    internal void ApplyTo(Contact contact)
    {
        contact.FirstName = FirstName;
        contact.LastName = LastName;
        contact.MiddleName = MiddleName;
        contact.Prefix = Prefix;
        contact.Suffix = Suffix;
        contact.Phone = Phone;
        contact.Email = Email;
    }
}