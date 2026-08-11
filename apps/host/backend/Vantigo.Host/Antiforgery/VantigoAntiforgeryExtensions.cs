namespace Vantigo.Host.Antiforgery;

internal static class VantigoAntiforgeryExtensions
{
    public static IApplicationBuilder UseVantigoAntiforgery(this IApplicationBuilder app)
        => app.UseMiddleware<VantigoAntiforgeryMiddleware>();
}