using Vantigo.Products.Endpoints.Products;
using Vantigo.Products.Endpoints.Products.Prices;
using Vantigo.Products.Endpoints.Products.Variants;

namespace Vantigo.Products.Endpoints;

internal static class ProductsEndpoints
{
    internal const string GetProductRouteName = "GetProduct";

    internal static IEndpointRouteBuilder MapProductsEndpoints(this IEndpointRouteBuilder app)
    {
        var group = app.MapGroup("").WithTags("Products");

        group.MapGet("/", GetProductsEndpoint.Handler).WithSummary("List all products");
        group.MapPost("/", CreateProductEndpoint.Handler).RequireAntiforgery().WithSummary("Create a new product");
        group.MapGet("/{id:int}", GetProductEndpoint.Handler).WithName(GetProductRouteName).WithSummary("Get a product by id");
        group.MapPut("/{id:int}", UpdateProductEndpoint.Handler).RequireAntiforgery().WithSummary("Update a product");
        group.MapDelete("/{id:int}", ArchiveProductEndpoint.Handler).RequireAntiforgery().WithSummary("Archive a product")
            .WithDescription("Marks the product as discontinued. Products are never hard-deleted because other services reference them.");

        group.MapGet("/{id:int}/variants", GetProductVariantsEndpoint.Handler).WithSummary("List variants of a product");
        group.MapPost("/{id:int}/variants", AddProductVariantEndpoint.Handler).RequireAntiforgery().WithSummary("Add a variant to a product");
        group.MapPut("/{id:int}/variants/{variantId:int}", UpdateProductVariantEndpoint.Handler).RequireAntiforgery().WithSummary("Update a product variant");
        group.MapDelete("/{id:int}/variants/{variantId:int}", DeleteProductVariantEndpoint.Handler).RequireAntiforgery().WithSummary("Remove a product variant");

        group.MapGet("/{id:int}/variants/{variantId:int}/prices", GetProductPricesEndpoint.Handler).WithSummary("List prices of a variant");
        group.MapPost("/{id:int}/variants/{variantId:int}/prices", AddProductPriceEndpoint.Handler).RequireAntiforgery().WithSummary("Add a price to a variant");
        group.MapPut("/{id:int}/variants/{variantId:int}/prices/{priceId:int}", UpdateProductPriceEndpoint.Handler).RequireAntiforgery().WithSummary("Update a variant price");
        group.MapDelete("/{id:int}/variants/{variantId:int}/prices/{priceId:int}", DeleteProductPriceEndpoint.Handler).RequireAntiforgery().WithSummary("Remove a variant price");

        return app;
    }
}