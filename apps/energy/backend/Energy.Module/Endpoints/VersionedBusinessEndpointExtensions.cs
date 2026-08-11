using Asp.Versioning;

using Vantigo.Contracts.Identity;

namespace Vantigo.Energy.Endpoints;

internal static class VersionedBusinessEndpointExtensions
{
    internal static IEndpointRouteBuilder MapVersionedBusinessEndpoints(this IEndpointRouteBuilder endpoints)
    {
        var api = endpoints.NewVersionedApi()
            .MapGroup("/api/v{version:apiVersion}/energy")
            .HasApiVersion(new ApiVersion(1))
            .RequireAuthorization(AuthPolicies.Business);
        api.MapMeteringPointEndpoints();
        api.MapCustomerEnergyEndpoints();
        return endpoints;
    }
}