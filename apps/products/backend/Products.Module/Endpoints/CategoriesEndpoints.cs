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
            .WithDescription("Returns the flat adjacency list; clients build the tree from parentId.");

        group.MapPost("/", CreateCategoryEndpoint.Handler)
            .RequireAntiforgery()
            .WithSummary("Create a new category");

        group.MapGet("/{id:int}", GetCategoryEndpoint.Handler)
            .WithName(GetCategoryRouteName)
            .WithSummary("Get a category by id");

        group.MapPut("/{id:int}", UpdateCategoryEndpoint.Handler)
            .RequireAntiforgery()
            .WithSummary("Update a category")
            .WithDescription("Renames and/or re-parents the category. Moves that would create a cycle are rejected.");

        group.MapDelete("/{id:int}", DeleteCategoryEndpoint.Handler)
            .RequireAntiforgery()
            .WithSummary("Delete a category")
            .WithDescription("Restricted while the category has subcategories or assigned products.");

        return app;
    }
}