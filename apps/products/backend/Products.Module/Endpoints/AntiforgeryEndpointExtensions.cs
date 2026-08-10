namespace Vantigo.Products.Endpoints;

internal static class AntiforgeryEndpointExtensions
{
    internal static RouteHandlerBuilder RequireAntiforgery(this RouteHandlerBuilder builder) => builder;
}