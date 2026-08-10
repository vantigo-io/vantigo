using Vantigo.Customers.Domain.Exceptions;

namespace Vantigo.Customers.Domain.Contacts.Common;

/// <summary>
/// A phone number a contact can be reached on. Validation is deliberately permissive
/// (digits, spaces and common formatting characters) since numbers are entered by
/// hand and formats vary between countries.
/// </summary>
public readonly record struct PhoneNumber
{
    private readonly string _value;

    public const int MaxLength = 30;

    public PhoneNumber(string value)
    {
        if (Validate(value) is { } error)
        {
            throw new DomainException(error);
        }

        _value = value.Trim();
    }

    /// <summary>
    /// Attempts to create a phone number from the given value, returning a
    /// human-readable error instead of throwing when the value is invalid.
    /// </summary>
    public static bool TryCreate(string? value, out PhoneNumber result, out string? error)
    {
        error = Validate(value);
        result = error is null ? new PhoneNumber(value!) : default;
        return error is null;
    }

    private static string? Validate(string? value) => value switch
    {
        _ when string.IsNullOrWhiteSpace(value) => "A phone number cannot be null or empty",
        { Length: > MaxLength } => $"A phone number cannot be longer than {MaxLength} characters, the given value was {value.Length} characters",
        _ when !value.Trim().All(IsAllowedCharacter) => "A phone number can only contain digits, spaces and the characters + - ( ) .",
        _ when !value.Any(char.IsDigit) => "A phone number must contain at least one digit",
        _ => null,
    };

    private static bool IsAllowedCharacter(char character) =>
        char.IsDigit(character) || character is ' ' or '+' or '-' or '(' or ')' or '.';

    public static implicit operator PhoneNumber(string value) => new(value);
    public static implicit operator string(PhoneNumber phoneNumber) => phoneNumber._value;

    /// <summary>
    /// Converts the phone number to its persisted representation.
    /// </summary>
    public string ToPersistence() => _value;

    /// <summary>
    /// Recreates a phone number from its persisted representation.
    /// </summary>
    public static PhoneNumber FromPersistence(string value) => new(value);
}