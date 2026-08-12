using Vantigo.Contracts.AspNetCore.Authorization;
using Vantigo.Products.Endpoints.Categories;

namespace Vantigo.Products.Endpoints;

internal static class CategoriesEndpoints
{
    internal const string GetCategoryRouteName = "GetCategory";

    internal static IEndpointRouteBuilder MapCategoriesEndpoints(this IEndpointRouteBuilder app)
    {
        var group = app.MapGroup("/categories")
            .WithTags("Categories");

        group.MapGet("/", GetCategoriesEndpoint.Handler)
            .WithSummary("List all categories")
            .WithDescription("Returns the flat adjacency list; clients build the tree from parentId.")
            .RequirePermission("products:categories-view");

        group.MapPost("/", CreateCategoryEndpoint.Handler)

            .WithSummary("Create a new category")
            .RequirePermission("products:categories-manage")
            .RequirePermission("products:categories-view");

        group.MapGet("/{id:int}", GetCategoryEndpoint.Handler)
            .WithName(GetCategoryRouteName)
            .WithSummary("Get a category by id")
            .RequirePermission("products:categories-view");

        group.MapPut("/{id:int}", UpdateCategoryEndpoint.Handler)

            .WithSummary("Update a category")
            .WithDescription("Renames and/or re-parents the category. Moves that would create a cycle are rejected.")
            .RequirePermission("products:categories-manage")
            .RequirePermission("products:categories-view");

        group.MapDelete("/{id:int}", DeleteCategoryEndpoint.Handler)

            .WithSummary("Delete a category")
            .WithDescription("Restricted while the category has subcategories or assigned products.")
            .RequirePermission("products:categories-manage");

        return app;
    }
}