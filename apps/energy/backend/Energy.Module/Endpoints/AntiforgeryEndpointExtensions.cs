namespace Vantigo.Energy.Endpoints;

internal static class AntiforgeryEndpointExtensions
{
    internal static RouteHandlerBuilder RequireAntiforgery(this RouteHandlerBuilder builder) => builder;
}