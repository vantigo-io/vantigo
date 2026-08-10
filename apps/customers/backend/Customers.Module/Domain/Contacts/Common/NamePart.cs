using Vantigo.Customers.Domain.Exceptions;

namespace Vantigo.Customers.Domain.Contacts.Common;

/// <summary>
/// A short honorific part of a person's name, such as a prefix ("Dr.") or a
/// suffix ("Jr.", "PhD").
/// </summary>
public readonly record struct NamePart
{
    private readonly string _value;

    public const int MaxLength = 20;

    public NamePart(string value)
    {
        if (Validate(value) is { } error)
        {
            throw new DomainException(error);
        }

        _value = value.Trim();
    }

    /// <summary>
    /// Attempts to create a name part from the given value, returning a
    /// human-readable error instead of throwing when the value is invalid.
    /// </summary>
    public static bool TryCreate(string? value, out NamePart result, out string? error)
    {
        error = Validate(value);
        result = error is null ? new NamePart(value!) : default;
        return error is null;
    }

    private static string? Validate(string? value) => value switch
    {
        _ when string.IsNullOrWhiteSpace(value) => "A name part cannot be null or empty",
        { Length: > MaxLength } => $"A name part cannot be longer than {MaxLength} characters, the given value was {value.Length} characters",
        _ => null,
    };

    public static implicit operator NamePart(string value) => new(value);
    public static implicit operator string(NamePart namePart) => namePart._value;

    /// <summary>
    /// Converts the name part to its persisted representation.
    /// </summary>
    public string ToPersistence() => _value;

    /// <summary>
    /// Recreates a name part from its persisted representation.
    /// </summary>
    public static NamePart FromPersistence(string value) => new(value);
}