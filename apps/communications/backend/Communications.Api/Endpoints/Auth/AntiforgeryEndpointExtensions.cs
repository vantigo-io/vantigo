using Microsoft.AspNetCore.Antiforgery;

namespace Vantigo.Communications.Api.Endpoints.Auth;

internal static class AntiforgeryEndpointExtensions
{
    internal static RouteHandlerBuilder RequireAntiforgery(this RouteHandlerBuilder builder)
    {
        builder.AddEndpointFilter(async (context, next) =>
        {
            try
            {
                await context.HttpContext.RequestServices.GetRequiredService<IAntiforgery>()
                    .ValidateRequestAsync(context.HttpContext);
            }
            catch (AntiforgeryValidationException)
            {
                return TypedResults.Json(new AuthErrorResponse(new AuthError(
                    "csrf_validation_failed", "A valid X-XSRF-TOKEN header and antiforgery cookie are required.")),
                    statusCode: StatusCodes.Status400BadRequest);
            }
            return await next(context);
        });
        return builder;
    }
}
