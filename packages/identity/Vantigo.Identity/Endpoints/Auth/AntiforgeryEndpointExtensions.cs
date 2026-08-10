using Microsoft.AspNetCore.Antiforgery;

namespace Vantigo.Identity.Endpoints.Auth;

public static class AntiforgeryEndpointExtensions
{
    public static RouteHandlerBuilder RequireAntiforgery(this RouteHandlerBuilder builder)
    {
        builder.AddEndpointFilter(async (context, next) =>
        {
            var antiforgery = context.HttpContext.RequestServices.GetRequiredService<IAntiforgery>();
            try
            {
                await antiforgery.ValidateRequestAsync(context.HttpContext);
            }
            catch (AntiforgeryValidationException)
            {
                return TypedResults.Json(
                    new AuthErrorResponse(new AuthError(
                        "csrf_validation_failed",
                        "A valid X-XSRF-TOKEN header and antiforgery cookie are required.")),
                    statusCode: StatusCodes.Status400BadRequest);
            }

            return await next(context);
        });

        return builder;
    }
}