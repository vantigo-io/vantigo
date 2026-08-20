using System.Diagnostics;

namespace Vantigo.Host.Diagnostics;

/// <summary>
/// Registers the centralized exception pipeline: RFC 7807 Problem Details plus
/// the global <see cref="VantigoExceptionHandler"/>. Pair this with
/// <c>app.UseExceptionHandler()</c> as the outermost API middleware.
/// </summary>
public static class VantigoExceptionHandlingExtensions
{
    /// <summary>
    /// Adds Problem Details generation and the global exception handler that
    /// turns unhandled exceptions into sanitized, trace-correlated responses.
    /// </summary>
    public static IServiceCollection AddVantigoExceptionHandling(this IServiceCollection services)
    {
        // The framework defaults stamp the full activity id; the trace id alone is
        // what correlates a caller-reported failure with the exported traces and
        // the logged exception. This customization runs after those defaults.
        services.AddProblemDetails(options => options.CustomizeProblemDetails = context =>
            context.ProblemDetails.Extensions["traceId"] = TraceId(context.HttpContext));
        services.AddExceptionHandler<VantigoExceptionHandler>();
        return services;
    }

    /// <summary>Trace id of the current activity, falling back to the request identifier.</summary>
    internal static string TraceId(HttpContext httpContext) =>
        Activity.Current?.TraceId.ToString() ?? httpContext.TraceIdentifier;
}