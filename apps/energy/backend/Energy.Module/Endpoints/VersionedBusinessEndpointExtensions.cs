using Asp.Versioning;

using Vantigo.Tenancy;

namespace Vantigo.Energy.Endpoints;

internal static class VersionedBusinessEndpointExtensions
{
    internal static IEndpointRouteBuilder MapVersionedBusinessEndpoints(this IEndpointRouteBuilder endpoints)
    {
        var api = endpoints.NewVersionedApi()
            .MapTenantGroup("/api/v{version:apiVersion}/energy")
            .HasApiVersion(new ApiVersion(1));
        api.MapMeteringPointEndpoints();
        api.MapCustomerEnergyEndpoints();
        return endpoints;
    }
}