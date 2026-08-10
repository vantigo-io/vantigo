using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Products.Database.Products;
using Vantigo.Products.Endpoints.Products.Dtos;

namespace Vantigo.Products.Endpoints.Products.Variants;

/// <summary>Adds a sellable variant to a product.</summary>
internal static class AddProductVariantEndpoint
{
    internal static async Task<Results<Created<ProductVariantResponse>, NotFound, ValidationProblem, ProblemHttpResult>> Handler(
        int id, VariantRequest request, ProductsDbContext dbContext, CancellationToken cancellationToken)
    {
        var errors = new Dictionary<string, string[]>();
        request.Validate(string.Empty, errors);
        if (errors.Count > 0)
        {
            return TypedResults.ValidationProblem(errors, title: "Invalid variant");
        }

        if (!await dbContext.Products.AnyAsync(product => product.Id == id, cancellationToken))
        {
            return TypedResults.NotFound();
        }

        var variant = request.ToDomain();
        if (await dbContext.ProductVariants.AnyAsync(existing => existing.Sku == variant.Sku, cancellationToken))
        {
            return TypedResults.Problem(title: "Duplicate SKU", detail: $"A variant with SKU '{variant.Sku}' already exists.", statusCode: StatusCodes.Status409Conflict);
        }

        if (variant.Barcode is not null && await dbContext.ProductVariants.AnyAsync(existing => existing.Barcode == variant.Barcode, cancellationToken))
        {
            return TypedResults.Problem(title: "Duplicate barcode", detail: $"A variant with barcode '{variant.Barcode}' already exists.", statusCode: StatusCodes.Status409Conflict);
        }

        variant.ProductId = id;
        dbContext.ProductVariants.Add(variant);
        await dbContext.SaveChangesAsync(cancellationToken);
        return TypedResults.Created($"/api/v1/products/{id}/variants/{variant.Id}", ProductVariantResponse.FromDomain(variant, DateTimeOffset.UtcNow));
    }
}