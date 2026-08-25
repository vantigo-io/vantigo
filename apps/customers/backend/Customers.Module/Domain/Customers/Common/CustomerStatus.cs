using Vantigo.Customers.Domain.Exceptions;

namespace Vantigo.Customers.Domain.Customers.Common;

/// <summary>
/// The lifecycle status of a customer. A customer is <see cref="Active"/> by default, can be
/// <see cref="Disabled"/> when it should no longer be used in day-to-day workflows, and
/// becomes <see cref="Archived"/> instead of ever being hard-deleted: Communications and
/// Energy keep historical references to customer ids, so the row must survive for those
/// views and for the audit timeline. Archived customers are hidden from default listings
/// but stay resolvable by id.
/// </summary>
public readonly record struct CustomerStatus
{
    private readonly string _value;

    public const string Active = "active";
    public const string Disabled = "disabled";
    public const string Archived = "archived";

    private static readonly string[] AllowedValues = [Active, Disabled, Archived];

    public CustomerStatus(string value)
    {
        if (Validate(value) is { } error)
        {
            throw new DomainException(error);
        }

        _value = value.Trim().ToLower();
    }

    /// <summary>
    /// Attempts to create a customer status from the given value, returning a
    /// human-readable error instead of throwing when the value is invalid.
    /// </summary>
    public static bool TryCreate(string? value, out CustomerStatus result, out string? error)
    {
        error = Validate(value);
        result = error is null ? new CustomerStatus(value!) : default;
        return error is null;
    }

    private static string? Validate(string? value)
    {
        if (string.IsNullOrWhiteSpace(value))
        {
            return "A customer status cannot be null or empty";
        }

        return AllowedValues.Contains(value.Trim().ToLower())
            ? null
            : $"A customer status must be one of '{Active}', '{Disabled}' or '{Archived}', but was '{value}'";
    }

    public static implicit operator CustomerStatus(string value) => new(value);
    public static implicit operator string(CustomerStatus status) => status._value;

    /// <summary>
    /// Converts the customer status to its persisted representation.
    /// </summary>
    public string ToPersistence() => _value;

    /// <summary>
    /// Recreates a customer status from its persisted representation.
    /// </summary>
    public static CustomerStatus FromPersistence(string value) => new(value);
}