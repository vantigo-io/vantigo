using Vantigo.Contracts.Identity;

namespace Vantigo.Communications.Endpoints;

public static class VersionedBusinessEndpointExtensions
{
    public static IEndpointRouteBuilder MapCommunicationsModule(this IEndpointRouteBuilder endpoints)
    {
        endpoints.MapVersionedBusinessEndpoints();
        return endpoints;
    }
}