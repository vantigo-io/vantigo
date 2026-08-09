using Vantigo.Products.Api.Domain.Products;

namespace Vantigo.Products.Api.Endpoints.Products.Dtos;

/// <summary>
/// The representation of a single product price row.
/// </summary>
internal readonly record struct ProductPriceResponse
{
    public required int Id { get; init; }
    public required string Currency { get; init; }
    public required decimal Amount { get; init; }
    public DateTimeOffset? ValidFrom { get; init; }
    public DateTimeOffset? ValidTo { get; init; }

    internal static ProductPriceResponse FromDomain(ProductPrice price) => new()
    {
        Id = price.Id,
        Currency = price.Currency,
        Amount = price.Amount,
        ValidFrom = price.ValidFrom,
        ValidTo = price.ValidTo,
    };
}