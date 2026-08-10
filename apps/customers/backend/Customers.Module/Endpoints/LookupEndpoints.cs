using Vantigo.Customers.Endpoints.Lookup;

namespace Vantigo.Customers.Endpoints;

internal static class LookupEndpoints
{
    internal static IEndpointRouteBuilder MapLookupEndpoints(this IEndpointRouteBuilder app)
    {
        var group = app.MapGroup("/lookup")
            .WithTags("Lookup");

        group.MapGet("/brreg", BrregLookupEndpoint.Handler)
            .WithSummary("Look up business entities in Brønnøysundregisteret");

        return app;
    }
}