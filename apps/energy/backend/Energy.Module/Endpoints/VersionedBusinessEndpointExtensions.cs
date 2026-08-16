using Asp.Versioning;

using Vantigo.Tenancy;

namespace Vantigo.Energy.Endpoints;

public static class VersionedBusinessEndpointExtensions
{
    public static IEndpointRouteBuilder MapEnergyModule(this IEndpointRouteBuilder endpoints)
    {
        var api = endpoints.NewVersionedApi()
            .MapTenantGroup("/api/v{version:apiVersion}/energy")
            .HasApiVersion(new ApiVersion(1));
        api.MapMeteringPointEndpoints();
        api.MapCustomerEnergyEndpoints();
        return endpoints;
    }
}