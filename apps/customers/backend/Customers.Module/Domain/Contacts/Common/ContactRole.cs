using Vantigo.Customers.Domain.Exceptions;

namespace Vantigo.Customers.Domain.Contacts.Common;

/// <summary>
/// The role a contact holds in its association with a customer, such as "CEO",
/// "CTO" or "Custodian". Free text, since roles vary wildly between organizations.
/// </summary>
public readonly record struct ContactRole
{
    private readonly string _value;

    public const int MaxLength = 255;

    public ContactRole(string value)
    {
        if (Validate(value) is { } error)
        {
            throw new DomainException(error);
        }

        _value = value.Trim();
    }

    /// <summary>
    /// Attempts to create a contact role from the given value, returning a
    /// human-readable error instead of throwing when the value is invalid.
    /// </summary>
    public static bool TryCreate(string? value, out ContactRole result, out string? error)
    {
        error = Validate(value);
        result = error is null ? new ContactRole(value!) : default;
        return error is null;
    }

    private static string? Validate(string? value) => value switch
    {
        _ when string.IsNullOrWhiteSpace(value) => "A role cannot be null or empty",
        { Length: > MaxLength } => $"A role cannot be longer than {MaxLength} characters, the given value was {value.Length} characters",
        _ => null,
    };

    public static implicit operator ContactRole(string value) => new(value);
    public static implicit operator string(ContactRole contactRole) => contactRole._value;

    /// <summary>
    /// Converts the contact role to its persisted representation.
    /// </summary>
    public string ToPersistence() => _value;

    /// <summary>
    /// Recreates a contact role from its persisted representation.
    /// </summary>
    public static ContactRole FromPersistence(string value) => new(value);
}