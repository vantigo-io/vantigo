using Vantigo.Customers.Api.Domain.Exceptions;

namespace Vantigo.Customers.Api.Domain.Customers.Common;

/// <summary>
/// The "friendly name" of a customer. It is not necessarily connected to the legal
/// name of the customer, but a name that can be used to identify the customer either
/// before the legal data is known or as an alias.
/// </summary>
public readonly record struct FriendlyName
{
    private readonly string _value;

    public const int MaxLength = 255;

    public FriendlyName(string value)
    {
        if (Validate(value) is { } error)
        {
            throw new DomainException(error);
        }

        _value = value.Trim();
    }

    /// <summary>
    /// Attempts to create a friendly name from the given value, returning a
    /// human-readable error instead of throwing when the value is invalid.
    /// </summary>
    public static bool TryCreate(string? value, out FriendlyName result, out string? error)
    {
        error = Validate(value);
        result = error is null ? new FriendlyName(value!) : default;
        return error is null;
    }

    private static string? Validate(string? value) => value switch
    {
        _ when string.IsNullOrWhiteSpace(value) => "A friendly name cannot be null or empty",
        { Length: > MaxLength } => $"A friendly name cannot be longer than {MaxLength} characters, the given value was {value.Length} characters",
        _ => null,
    };

    public static implicit operator FriendlyName(string value) => new(value);
    public static implicit operator string(FriendlyName friendlyName) => friendlyName._value;

    /// <summary>
    /// Converts the friendly name to its persisted representation.
    /// </summary>
    public string ToPersistence() => _value;

    /// <summary>
    /// Recreates a friendly name from its persisted representation.
    /// </summary>
    public static FriendlyName FromPersistence(string value) => new(value);
}