using Vantigo.Customers.Api.Domain.Exceptions;

namespace Vantigo.Customers.Api.Domain.Customers.Common;

public readonly record struct LegalType
{
    private readonly string _value;

    public const string Person = "person";
    public const string Business = "business";

    public LegalType(string value)
    {
        if (Validate(value) is { } error)
        {
            throw new DomainException(error);
        }

        _value = value.Trim().ToLower();
    }

    /// <summary>
    /// Attempts to create a legal type from the given value, returning a
    /// human-readable error instead of throwing when the value is invalid.
    /// </summary>
    public static bool TryCreate(string? value, out LegalType result, out string? error)
    {
        error = Validate(value);
        result = error is null ? new LegalType(value!) : default;
        return error is null;
    }

    private static string? Validate(string? value) =>
        string.IsNullOrWhiteSpace(value)
            ? "A legal type cannot be null or empty"
            : null;

    public static implicit operator LegalType(string value) => new (value);
    public static implicit operator string(LegalType legalType) => legalType._value;

    /// <summary>
    /// Converts the legal type to its persisted representation.
    /// </summary>
    public string ToPersistence() => _value;

    /// <summary>
    /// Recreates a legal type from its persisted representation.
    /// </summary>
    public static LegalType FromPersistence(string value) => new (value);
}
