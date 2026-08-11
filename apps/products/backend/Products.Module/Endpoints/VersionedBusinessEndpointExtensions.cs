using Asp.Versioning;

using Vantigo.Contracts.Identity;

namespace Vantigo.Products.Endpoints;

internal static class VersionedBusinessEndpointExtensions
{
    public static IEndpointRouteBuilder MapVersionedBusinessEndpoints(this IEndpointRouteBuilder endpoints)
    {
        var api = endpoints.NewVersionedApi()
            .MapGroup("/api/v{version:apiVersion}/products")
            .HasApiVersion(new ApiVersion(1))
            .RequireAuthorization(AuthPolicies.Business);

        api.MapProductsEndpoints();
        api.MapCategoriesEndpoints();
        api.MapTaxCategoriesEndpoints();

        return endpoints;
    }
}