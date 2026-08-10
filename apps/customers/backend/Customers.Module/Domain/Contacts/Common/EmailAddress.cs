using Vantigo.Customers.Domain.Exceptions;

namespace Vantigo.Customers.Domain.Contacts.Common;

/// <summary>
/// An email address a contact can be reached on. Validation only checks the basic
/// shape (local part, a single @, domain with a dot) — full RFC 5322 validation
/// creates more false negatives than it prevents mistakes.
/// </summary>
public readonly record struct EmailAddress
{
    private readonly string _value;

    public const int MaxLength = 255;

    public EmailAddress(string value)
    {
        if (Validate(value) is { } error)
        {
            throw new DomainException(error);
        }

        _value = value.Trim().ToLower();
    }

    /// <summary>
    /// Attempts to create an email address from the given value, returning a
    /// human-readable error instead of throwing when the value is invalid.
    /// </summary>
    public static bool TryCreate(string? value, out EmailAddress result, out string? error)
    {
        error = Validate(value);
        result = error is null ? new EmailAddress(value!) : default;
        return error is null;
    }

    private static string? Validate(string? value) => value switch
    {
        _ when string.IsNullOrWhiteSpace(value) => "An email address cannot be null or empty",
        { Length: > MaxLength } => $"An email address cannot be longer than {MaxLength} characters, the given value was {value.Length} characters",
        _ when !HasValidShape(value.Trim()) => "An email address must have the shape 'name@domain.tld'",
        _ => null,
    };

    private static bool HasValidShape(string value)
    {
        var atIndex = value.IndexOf('@');

        return atIndex > 0
               && atIndex == value.LastIndexOf('@')
               && value.IndexOf('.', atIndex) > atIndex + 1
               && !value.EndsWith('.')
               && !value.Contains(' ');
    }

    public static implicit operator EmailAddress(string value) => new(value);
    public static implicit operator string(EmailAddress emailAddress) => emailAddress._value;

    /// <summary>
    /// Converts the email address to its persisted representation.
    /// </summary>
    public string ToPersistence() => _value;

    /// <summary>
    /// Recreates an email address from its persisted representation.
    /// </summary>
    public static EmailAddress FromPersistence(string value) => new(value);
}