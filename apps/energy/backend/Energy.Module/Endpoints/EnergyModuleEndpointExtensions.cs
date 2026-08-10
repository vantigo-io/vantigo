namespace Vantigo.Energy.Endpoints;

public static class EnergyModuleEndpointExtensions
{
    public static IEndpointRouteBuilder MapEnergyModule(this IEndpointRouteBuilder endpoints)
    {
        endpoints.MapVersionedBusinessEndpoints();
        return endpoints;
    }
}