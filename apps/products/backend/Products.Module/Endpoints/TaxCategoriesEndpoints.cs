using Vantigo.Contracts.AspNetCore.Authorization;
using Vantigo.Products.Endpoints.TaxCategories;

namespace Vantigo.Products.Endpoints;

internal static class TaxCategoriesEndpoints
{
    internal const string GetTaxCategoryRouteName = "GetTaxCategory";

    internal static IEndpointRouteBuilder MapTaxCategoriesEndpoints(this IEndpointRouteBuilder app)
    {
        var group = app.MapGroup("/tax-categories")
            .WithTags("Tax categories");

        group.MapGet("/", GetTaxCategoriesEndpoint.Handler)
            .WithSummary("List all tax categories")
            .RequirePermission("products:tax-categories-view");

        group.MapPost("/", CreateTaxCategoryEndpoint.Handler)

            .WithSummary("Create a tax category")
            .RequirePermission("products:tax-categories-manage")
            .RequirePermission("products:tax-categories-view");

        group.MapGet("/{id:int}", GetTaxCategoryEndpoint.Handler)
            .WithName(GetTaxCategoryRouteName)
            .WithSummary("Get a tax category by id")
            .RequirePermission("products:tax-categories-view");

        group.MapPut("/{id:int}", UpdateTaxCategoryEndpoint.Handler)

            .WithSummary("Update a tax category")
            .RequirePermission("products:tax-categories-manage")
            .RequirePermission("products:tax-categories-view");

        group.MapDelete("/{id:int}", DeleteTaxCategoryEndpoint.Handler)

            .WithSummary("Delete a tax category")
            .WithDescription("Restricted while products reference the tax category.")
            .RequirePermission("products:tax-categories-manage");

        return app;
    }
}