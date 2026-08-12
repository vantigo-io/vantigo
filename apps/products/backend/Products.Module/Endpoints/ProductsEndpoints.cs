using Vantigo.Contracts.AspNetCore.Authorization;
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

        group.MapGet("/", GetProductsEndpoint.Handler).WithSummary("List all products")
            .RequirePermission("products:products-view")
            .RequirePermission("products:variants-view")
            .RequirePermission("products:pricing-view")
            .RequirePermission("products:categories-view")
            .RequirePermission("products:tax-categories-view");
        group.MapPost("/", CreateProductEndpoint.Handler).WithSummary("Create a new product")
            .RequirePermission("products:products-manage")
            .RequirePermission("products:variants-manage")
            .RequirePermission("products:variants-view")
            .RequirePermission("products:pricing-view")
            .RequirePermission("products:categories-view")
            .RequirePermission("products:tax-categories-view");
        group.MapGet("/{id:int}", GetProductEndpoint.Handler).WithName(GetProductRouteName).WithSummary("Get a product by id")
            .RequirePermission("products:products-view")
            .RequirePermission("products:variants-view")
            .RequirePermission("products:pricing-view")
            .RequirePermission("products:categories-view")
            .RequirePermission("products:tax-categories-view");
        group.MapPut("/{id:int}", UpdateProductEndpoint.Handler).WithSummary("Update a product")
            .RequirePermission("products:products-manage")
            .RequirePermission("products:variants-view")
            .RequirePermission("products:pricing-view")
            .RequirePermission("products:categories-view")
            .RequirePermission("products:tax-categories-view");
        group.MapDelete("/{id:int}", ArchiveProductEndpoint.Handler).WithSummary("Archive a product")
            .WithDescription("Marks the product as discontinued. Products are never hard-deleted because other services reference them.")
            .RequirePermission("products:products-manage");

        group.MapGet("/{id:int}/variants", GetProductVariantsEndpoint.Handler).WithSummary("List variants of a product")
            .RequirePermission("products:variants-view")
            .RequirePermission("products:pricing-view");
        group.MapPost("/{id:int}/variants", AddProductVariantEndpoint.Handler).WithSummary("Add a variant to a product")
            .RequirePermission("products:variants-manage")
            .RequirePermission("products:variants-view")
            .RequirePermission("products:pricing-view");
        group.MapPut("/{id:int}/variants/{variantId:int}", UpdateProductVariantEndpoint.Handler).WithSummary("Update a product variant")
            .RequirePermission("products:variants-manage")
            .RequirePermission("products:variants-view")
            .RequirePermission("products:pricing-view")
            .RequirePermission("products:pricing-manage");
        group.MapDelete("/{id:int}/variants/{variantId:int}", DeleteProductVariantEndpoint.Handler).WithSummary("Remove a product variant")
            .RequirePermission("products:variants-manage")
            .RequirePermission("products:pricing-manage");

        group.MapGet("/{id:int}/variants/{variantId:int}/prices", GetProductPricesEndpoint.Handler).WithSummary("List prices of a variant")
            .RequirePermission("products:pricing-view");
        group.MapPost("/{id:int}/variants/{variantId:int}/prices", AddProductPriceEndpoint.Handler).WithSummary("Add a price to a variant")
            .RequirePermission("products:pricing-manage")
            .RequirePermission("products:pricing-view");
        group.MapPut("/{id:int}/variants/{variantId:int}/prices/{priceId:int}", UpdateProductPriceEndpoint.Handler).WithSummary("Update a variant price")
            .RequirePermission("products:pricing-manage")
            .RequirePermission("products:pricing-view");
        group.MapDelete("/{id:int}/variants/{variantId:int}/prices/{priceId:int}", DeleteProductPriceEndpoint.Handler).WithSummary("Remove a variant price")
            .RequirePermission("products:pricing-manage");

        return app;
    }
}