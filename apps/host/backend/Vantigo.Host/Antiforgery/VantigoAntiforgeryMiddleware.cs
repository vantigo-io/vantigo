using Microsoft.AspNetCore.Antiforgery;

using Vantigo.Contracts.Web;

namespace Vantigo.Host.Antiforgery;

internal sealed class VantigoAntiforgeryMiddleware(RequestDelegate next, IAntiforgery antiforgery)
{
    private static readonly HashSet<string> SafeMethods = new(StringComparer.OrdinalIgnoreCase)
    {
        "GET",
        "HEAD",
        "OPTIONS",
        "TRACE",
    };

    public async Task InvokeAsync(HttpContext context)
    {
        if (ShouldSkip(context))
        {
            await next(context);
            return;
        }

        try
        {
            await antiforgery.ValidateRequestAsync(context);
        }
        catch (AntiforgeryValidationException)
        {
            context.Response.StatusCode = StatusCodes.Status400BadRequest;
            await context.Response.WriteAsJsonAsync(
                new AuthErrorResponse(new AuthError(
                    "csrf_validation_failed",
                    "A valid X-XSRF-TOKEN header and antiforgery cookie are required.")));
            return;
        }

        await next(context);
    }

    private static bool ShouldSkip(HttpContext context)
    {
        if (SafeMethods.Contains(context.Request.Method))
        {
            return true;
        }

        var endpoint = context.GetEndpoint();
        return endpoint?.Metadata.GetMetadata<SkipAntiforgeryAttribute>() is not null;
    }
}