using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Products.Api.Database.Products;
using Vantigo.Products.Api.Domain.Products;
using Vantigo.Products.Api.Endpoints.Products.Dtos;

namespace Vantigo.Products.Api.Endpoints.Products.Prices;

/// <summary>
/// Updates a price row in place. This rewrites the row's history: transactions are
/// unaffected because consumers snapshot prices, but anything re-reading the row
/// will see the new values. For planned price changes, adding a new bounded row is
/// preferred over editing. The updated row must not make effective-price resolution
/// ambiguous against the product's other rows.
/// </summary>
internal static class UpdateProductPriceEndpoint
{
    internal static async Task<Results<Ok<ProductPriceResponse>, NotFound, ValidationProblem, ProblemHttpResult>> Handler(
        int id,
        int priceId,
        ProductPriceRequest request,
        ProductsDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var errors = new Dictionary<string, string[]>();
        request.Validate(string.Empty, errors);
        if (errors.Count > 0)
        {
            return TypedResults.ValidationProblem(errors, title: "Invalid price");
        }

        var product = await dbContext.Products
            .Include(p => p.Prices)
            .FirstOrDefaultAsync(p => p.Id == id, cancellationToken);

        var price = product?.Prices.FirstOrDefault(p => p.Id == priceId);
        if (product is null || price is null)
        {
            return TypedResults.NotFound();
        }

        var candidate = request.ToDomain();
        if (product.Prices.Any(existing =>
                existing.Id != priceId && ProductPricing.Conflicts(candidate, existing)))
        {
            return TypedResults.Problem(
                title: "Overlapping price",
                detail: $"The price overlaps an existing {candidate.Currency} price of the same kind.",
                statusCode: StatusCodes.Status409Conflict);
        }

        price.Currency = candidate.Currency;
        price.Amount = candidate.Amount;
        price.ValidFrom = candidate.ValidFrom;
        price.ValidTo = candidate.ValidTo;
        await dbContext.SaveChangesAsync(cancellationToken);

        return TypedResults.Ok(ProductPriceResponse.FromDomain(price));
    }
}