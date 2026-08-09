using Microsoft.AspNetCore.Builder;
using Microsoft.AspNetCore.Http;
using Microsoft.Extensions.DependencyInjection;

namespace Vantigo.Hosting;

/// <summary>
/// Wires up base-path serving and the runtime-templated SPA entry point for a
/// Vantigo backend that hosts its frontend from wwwroot.
/// </summary>
public static class SpaHostingExtensions
{
    /// <summary>
    /// Registers the templated SPA entry document.
    /// <paramref name="buildTimeBasePath"/> is the prefix the frontend build
    /// embeds in its asset URLs (the app's VITE_BASE_PATH default) and
    /// <paramref name="defaultTitle"/> the application title used when
    /// <c>App:Title</c> is not configured.
    /// </summary>
    public static IServiceCollection AddSpaIndexDocument(
        this IServiceCollection services,
        string buildTimeBasePath,
        string defaultTitle)
    {
        services.AddSingleton(new SpaIndexDocumentOptions(buildTimeBasePath, defaultTitle));
        services.AddSingleton<SpaIndexDocument>();
        return services;
    }

    /// <summary>
    /// Mounts the application under the configured <c>App:BasePath</c>. Requests
    /// without the prefix pass through untouched, so serving from the root keeps
    /// working. Place after forwarded-headers handling and before static files
    /// and routing. Returns the effective base path, or <c>null</c> for root.
    /// </summary>
    public static string? UseAppBasePath(this WebApplication app)
    {
        var basePath = AppBasePath.Normalize(app.Configuration[AppBasePath.ConfigurationKey]);
        if (basePath is not null)
        {
            app.UsePathBase(basePath);
        }

        return basePath;
    }

    /// <summary>
    /// Prevents the raw wwwroot/index.html (with stale build-time URLs) from ever
    /// being served by the static-file middleware; the request falls through to
    /// the templated SPA fallback instead. Place before <c>UseStaticFiles</c>.
    /// </summary>
    public static IApplicationBuilder UseSpaIndexRewrite(this IApplicationBuilder app) =>
        app.Use((context, next) =>
        {
            if (context.Request.Path == "/index.html")
            {
                context.Request.Path = "/";
            }
            return next(context);
        });

    /// <summary>
    /// Serves the templated SPA entry document for any request no other endpoint
    /// matched (deep links, "/"). Responds 404 when no frontend is published.
    /// </summary>
    public static IEndpointConventionBuilder MapSpaFallback(this WebApplication app) =>
        app.MapFallback((HttpContext context, SpaIndexDocument index) =>
        {
            if (index.Html is null)
            {
                return Results.NotFound();
            }

            context.Response.Headers.CacheControl = "no-cache";
            return Results.Content(index.Html, "text/html", System.Text.Encoding.UTF8);
        });
}