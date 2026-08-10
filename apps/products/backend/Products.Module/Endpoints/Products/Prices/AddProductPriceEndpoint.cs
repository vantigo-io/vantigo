using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Products.Database.Products;
using Vantigo.Products.Domain.Products;
using Vantigo.Products.Endpoints.Products.Dtos;

namespace Vantigo.Products.Endpoints.Products.Prices;

/// <summary>
/// Adds a price row to a product. Campaign prices are added as new bounded rows next
/// to the open-ended base price rather than mutating it, which preserves price
/// history. Rows that would make effective-price resolution ambiguous are rejected.
/// </summary>
internal static class AddProductPriceEndpoint
{
    internal static async Task<Results<Created<ProductPriceResponse>, NotFound, ValidationProblem, ProblemHttpResult>> Handler(
        int id,
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

        if (product is null)
        {
            return TypedResults.NotFound();
        }

        var price = request.ToDomain();
        if (product.Prices.Any(existing => ProductPricing.Conflicts(price, existing)))
        {
            return TypedResults.Problem(
                title: "Overlapping price",
                detail: $"The price overlaps an existing {price.Currency} price of the same kind.",
                statusCode: StatusCodes.Status409Conflict);
        }

        product.Prices.Add(price);
        await dbContext.SaveChangesAsync(cancellationToken);

        return TypedResults.Created(
            $"/api/v1/products/{id}/prices",
            ProductPriceResponse.FromDomain(price));
    }
}