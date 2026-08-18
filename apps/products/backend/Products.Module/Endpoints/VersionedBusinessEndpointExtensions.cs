using Asp.Versioning;

using Vantigo.Tenancy;

namespace Vantigo.Products.Endpoints;

public static class VersionedBusinessEndpointExtensions
{
    public static IEndpointRouteBuilder MapProductsModule(this IEndpointRouteBuilder endpoints)
    {
        var api = endpoints.NewVersionedApi()
            .MapTenantGroup("/api/v{version:apiVersion}/products")
            .HasApiVersion(new ApiVersion(1))
            .RequireAuthorization();

        api.MapProductsEndpoints();
        api.MapProductStatsEndpoints();
        api.MapCategoriesEndpoints();
        api.MapTaxCategoriesEndpoints();

        return endpoints;
    }
}