using Vantigo.Customers.Domain.Exceptions;

namespace Vantigo.Customers.Domain.Contacts.Common;

/// <summary>
/// A single part of a person's name (first, middle or last name).
/// </summary>
public readonly record struct PersonName
{
    private readonly string _value;

    public const int MaxLength = 100;

    public PersonName(string value)
    {
        if (Validate(value) is { } error)
        {
            throw new DomainException(error);
        }

        _value = value.Trim();
    }

    /// <summary>
    /// Attempts to create a person name from the given value, returning a
    /// human-readable error instead of throwing when the value is invalid.
    /// </summary>
    public static bool TryCreate(string? value, out PersonName result, out string? error)
    {
        error = Validate(value);
        result = error is null ? new PersonName(value!) : default;
        return error is null;
    }

    private static string? Validate(string? value) => value switch
    {
        _ when string.IsNullOrWhiteSpace(value) => "A name cannot be null or empty",
        { Length: > MaxLength } => $"A name cannot be longer than {MaxLength} characters, the given value was {value.Length} characters",
        _ => null,
    };

    public static implicit operator PersonName(string value) => new(value);
    public static implicit operator string(PersonName personName) => personName._value;

    /// <summary>
    /// Converts the person name to its persisted representation.
    /// </summary>
    public string ToPersistence() => _value;

    /// <summary>
    /// Recreates a person name from its persisted representation.
    /// </summary>
    public static PersonName FromPersistence(string value) => new(value);
}