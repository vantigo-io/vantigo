using Vantigo.Products.Endpoints.Products;
using Vantigo.Products.Endpoints.Products.Prices;

namespace Vantigo.Products.Endpoints;

internal static class ProductsEndpoints
{
    internal const string GetProductRouteName = "GetProduct";

    internal static IEndpointRouteBuilder MapProductsEndpoints(this IEndpointRouteBuilder app)
    {
        var group = app.MapGroup("")
            .WithTags("Products");

        group.MapGet("/", GetProductsEndpoint.Handler)
            .WithSummary("List all products");

        group.MapPost("/", CreateProductEndpoint.Handler)
            .RequireAntiforgery()
            .WithSummary("Create a new product");

        group.MapGet("/{id:int}", GetProductEndpoint.Handler)
            .WithName(GetProductRouteName)
            .WithSummary("Get a product by id");

        group.MapPut("/{id:int}", UpdateProductEndpoint.Handler)
            .RequireAntiforgery()
            .WithSummary("Update a product");

        group.MapDelete("/{id:int}", ArchiveProductEndpoint.Handler)
            .RequireAntiforgery()
            .WithSummary("Archive a product")
            .WithDescription("Marks the product as discontinued. Products are never hard-deleted because other services reference them.");

        group.MapGet("/{id:int}/prices", GetProductPricesEndpoint.Handler)
            .WithSummary("List all price rows of a product");

        group.MapPost("/{id:int}/prices", AddProductPriceEndpoint.Handler)
            .RequireAntiforgery()
            .WithSummary("Add a price row to a product");

        group.MapPut("/{id:int}/prices/{priceId:int}", UpdateProductPriceEndpoint.Handler)
            .RequireAntiforgery()
            .WithSummary("Update a price row")
            .WithDescription("Rewrites the row in place. Prefer adding a new bounded row for planned price changes; consumers snapshot prices, so existing transactions are unaffected.");

        group.MapDelete("/{id:int}/prices/{priceId:int}", DeleteProductPriceEndpoint.Handler)
            .RequireAntiforgery()
            .WithSummary("Remove a price row from a product");

        return app;
    }
}