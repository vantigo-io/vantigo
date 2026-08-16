using Asp.Versioning;

using Vantigo.Tenancy;

namespace Vantigo.Products.Endpoints;

internal static class VersionedBusinessEndpointExtensions
{
    public static IEndpointRouteBuilder MapVersionedBusinessEndpoints(this IEndpointRouteBuilder endpoints)
    {
        var api = endpoints.NewVersionedApi()
            .MapTenantGroup("/api/v{version:apiVersion}/products")
            .HasApiVersion(new ApiVersion(1))
            .RequireAuthorization();

        api.MapProductsEndpoints();
        api.MapCategoriesEndpoints();
        api.MapTaxCategoriesEndpoints();

        return endpoints;
    }
}