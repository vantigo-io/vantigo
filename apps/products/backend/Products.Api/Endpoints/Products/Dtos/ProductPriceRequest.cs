using System.Globalization;

using Vantigo.Products.Api.Domain.Products;

namespace Vantigo.Products.Api.Endpoints.Products.Dtos;

/// <summary>
/// A sales price supplied by API clients, excluding VAT. Validation errors are keyed
/// by the JSON path of the offending field so clients can map them onto form fields.
/// </summary>
internal readonly record struct ProductPriceRequest
{
    public required string Currency { get; init; }
    public required decimal Amount { get; init; }
    public DateTimeOffset? ValidFrom { get; init; }
    public DateTimeOffset? ValidTo { get; init; }

    internal void Validate(string prefix, Dictionary<string, string[]> errors)
    {
        if (string.IsNullOrWhiteSpace(Currency) ||
            Currency.Trim().Length != ProductPrice.CurrencyLength ||
            !Currency.Trim().All(char.IsAsciiLetter))
        {
            errors[$"{prefix}currency"] = ["'currency' must be a three-letter ISO 4217 currency code."];
        }

        if (Amount < 0)
        {
            errors[$"{prefix}amount"] =
                [$"'amount' must be zero or greater, but was {Amount.ToString(CultureInfo.InvariantCulture)}."];
        }

        if (ValidFrom is { } from && ValidTo is { } to && to <= from)
        {
            errors[$"{prefix}validTo"] = ["'validTo' must be after 'validFrom'."];
        }
    }

    internal ProductPrice ToDomain() => new()
    {
        Currency = Currency.Trim().ToUpperInvariant(),
        Amount = Amount,
        ValidFrom = ValidFrom,
        ValidTo = ValidTo,
    };
}