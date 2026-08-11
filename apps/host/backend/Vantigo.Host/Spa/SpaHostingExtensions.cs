using Microsoft.AspNetCore.Builder;
using Microsoft.AspNetCore.Http;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Options;

using Vantigo.Configuration;

namespace Vantigo.Host;

/// <summary>
/// Wires up base-path serving and the runtime-templated SPA entry point for the
/// Vantigo host.
/// </summary>
public static class SpaHostingExtensions
{
    /// <summary>
    /// Registers the templated SPA entry document and Vantigo configuration
    /// options required to render it.
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
        var basePath = app.Services.GetRequiredService<IOptions<AppBasePathOptions>>().Value.Normalized;
        if (basePath is not null)
        {
            app.UsePathBase(basePath);
        }

        return basePath;
    }

    /// <summary>
    /// Prevents the raw wwwroot/index.html from being served by the static-file
    /// middleware; the request falls through to the templated SPA fallback. Place
    /// before <c>UseStaticFiles</c>.
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
    /// matched. Responds 404 when no frontend is published.
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