using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Products.Database.Products;
using Vantigo.Products.Domain.Products;
using Vantigo.Products.Endpoints.Products.Dtos;

namespace Vantigo.Products.Endpoints.Products.Variants;

/// <summary>Updates a variant; its SKU cannot change once its product is not Draft.</summary>
internal static class UpdateProductVariantEndpoint
{
    internal static async Task<Results<Ok<ProductVariantResponse>, NotFound, ValidationProblem, ProblemHttpResult>> Handler(
        int id, int variantId, VariantRequest request, ProductsDbContext dbContext, CancellationToken cancellationToken)
    {
        var errors = new Dictionary<string, string[]>();
        request.Validate(string.Empty, errors);
        if (errors.Count > 0)
        {
            return TypedResults.ValidationProblem(errors, title: "Invalid variant");
        }

        var variant = await dbContext.ProductVariants
            .Include(item => item.Prices)
            .FirstOrDefaultAsync(item => item.Id == variantId && item.ProductId == id, cancellationToken);
        if (variant is null)
        {
            return TypedResults.NotFound();
        }

        var productStatus = await dbContext.Products
            .Where(product => product.Id == id)
            .Select(product => product.Status)
            .SingleAsync(cancellationToken);

        var candidate = request.ToDomain();
        if (!string.Equals(variant.Sku, candidate.Sku, StringComparison.Ordinal))
        {
            if (productStatus != ProductStatus.Draft)
            {
                return TypedResults.Problem(title: "SKU is immutable", detail: "The SKU cannot be changed after the product has been activated.", statusCode: StatusCodes.Status409Conflict);
            }

            if (await dbContext.ProductVariants.AnyAsync(existing => existing.Sku == candidate.Sku && existing.Id != variantId, cancellationToken))
            {
                return TypedResults.Problem(title: "Duplicate SKU", detail: $"A variant with SKU '{candidate.Sku}' already exists.", statusCode: StatusCodes.Status409Conflict);
            }
        }

        if (candidate.Barcode is not null && await dbContext.ProductVariants.AnyAsync(existing => existing.Barcode == candidate.Barcode && existing.Id != variantId, cancellationToken))
        {
            return TypedResults.Problem(title: "Duplicate barcode", detail: $"A variant with barcode '{candidate.Barcode}' already exists.", statusCode: StatusCodes.Status409Conflict);
        }

        variant.Sku = candidate.Sku;
        variant.Barcode = candidate.Barcode;
        variant.Unit = candidate.Unit;
        variant.StandardCost = candidate.StandardCost;
        variant.WeightKg = candidate.WeightKg;
        variant.LengthCm = candidate.LengthCm;
        variant.WidthCm = candidate.WidthCm;
        variant.HeightCm = candidate.HeightCm;
        variant.OptionValues = new Dictionary<string, string>(candidate.OptionValues, StringComparer.OrdinalIgnoreCase);
        await dbContext.SaveChangesAsync(cancellationToken);
        return TypedResults.Ok(ProductVariantResponse.FromDomain(variant, DateTimeOffset.UtcNow));
    }
}