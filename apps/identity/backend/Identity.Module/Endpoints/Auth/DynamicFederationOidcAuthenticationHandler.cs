using System.Text.Encodings.Web;

using Microsoft.AspNetCore.Authentication;
using Microsoft.Extensions.Options;

using Vantigo.Identity.Services;

namespace Vantigo.Identity.Endpoints.Auth;

internal sealed class DynamicFederationOidcAuthenticationHandler(
    IOptionsMonitor<AuthenticationSchemeOptions> options,
    ILoggerFactory logger,
    UrlEncoder encoder,
    ISystemClock clock,
    DynamicFederationOidcService federation)
    : AuthenticationHandler<AuthenticationSchemeOptions>(options, logger, encoder, clock)
{
    protected override Task<AuthenticateResult> HandleAuthenticateAsync() =>
        Task.FromResult(AuthenticateResult.NoResult());

    protected override async Task HandleChallengeAsync(AuthenticationProperties properties)
    {
        var challenge = await federation.CreateChallengeAsync(properties, Context, Context.RequestAborted);
        if (challenge is null)
        {
            Response.StatusCode = StatusCodes.Status404NotFound;
            return;
        }

        Response.Cookies.Append(
            challenge.CorrelationCookieName,
            challenge.CorrelationValue,
            challenge.CookieOptions);
        Response.Redirect(challenge.AuthorizationUrl);
    }
}