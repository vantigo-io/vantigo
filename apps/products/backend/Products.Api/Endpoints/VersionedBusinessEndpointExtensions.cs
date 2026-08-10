using Asp.Versioning;

using Vantigo.Products.Api.Endpoints.Auth;

namespace Vantigo.Products.Api.Endpoints;

internal static class VersionedBusinessEndpointExtensions
{
    internal static IEndpointRouteBuilder MapVersionedBusinessEndpoints(this IEndpointRouteBuilder endpoints)
    {
        var api = endpoints.NewVersionedApi()
            .MapGroup("/api/v{version:apiVersion}")
            .HasApiVersion(new ApiVersion(1))
            .RequireAuthorization(AuthPolicies.Business);

        api.MapProductsEndpoints();
        api.MapCategoriesEndpoints();

        return endpoints;
    }
}