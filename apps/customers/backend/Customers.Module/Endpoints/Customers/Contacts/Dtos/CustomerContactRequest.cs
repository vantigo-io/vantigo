using Vantigo.Customers.Domain.Contacts;
using Vantigo.Customers.Domain.Contacts.Common;

namespace Vantigo.Customers.Endpoints.Customers.Contacts.Dtos;

/// <summary>
/// The role and optional connection-specific contact details of a customer-contact
/// association, shared by the attach and update endpoints. Blank optional values
/// are treated as absent.
/// </summary>
internal readonly record struct CustomerContactRequest
{
    public required string Role { get; init; }
    public string? Phone { get; init; }
    public string? Email { get; init; }

    /// <summary>
    /// Validates the request and applies it to the given association. When one or
    /// more values are invalid, all validation errors are returned at once, keyed by
    /// the camelCase JSON path of the offending field.
    /// </summary>
    internal bool TryApplyTo(CustomerContact association, out Dictionary<string, string[]> errors)
    {
        errors = [];

        if (!ContactRole.TryCreate(Role, out var role, out var roleError))
        {
            errors["role"] = [roleError!];
        }

        PhoneNumber? phone = null;
        if (!string.IsNullOrWhiteSpace(Phone))
        {
            if (PhoneNumber.TryCreate(Phone, out var parsedPhone, out var phoneError))
            {
                phone = parsedPhone;
            }
            else
            {
                errors["phone"] = [phoneError!];
            }
        }

        EmailAddress? email = null;
        if (!string.IsNullOrWhiteSpace(Email))
        {
            if (EmailAddress.TryCreate(Email, out var parsedEmail, out var emailError))
            {
                email = parsedEmail;
            }
            else
            {
                errors["email"] = [emailError!];
            }
        }

        if (errors.Count > 0)
        {
            return false;
        }

        association.Role = role;
        association.Phone = phone;
        association.Email = email;
        return true;
    }
}