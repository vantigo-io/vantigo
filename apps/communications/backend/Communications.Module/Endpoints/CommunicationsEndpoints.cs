using Asp.Versioning;

using Vantigo.Tenancy;

namespace Vantigo.Communications.Endpoints;

internal static class CommunicationsEndpoints
{
    internal static IEndpointRouteBuilder MapVersionedBusinessEndpoints(this IEndpointRouteBuilder endpoints)
    {
        var api = endpoints.NewVersionedApi().MapTenantGroup("/api/v{version:apiVersion}/communications").HasApiVersion(new ApiVersion(1));
        api.MapConversationEndpoints();
        api.MapConversationAiEndpoints();
        api.MapConversationMutationEndpoints();
        api.MapTagEndpoints();
        api.MapChannelEndpoints();
        api.MapSuppressionEndpoints();
        return endpoints;
    }
}