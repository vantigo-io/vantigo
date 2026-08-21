using System.Globalization;
using System.Security.Claims;

using Microsoft.AspNetCore.Authentication;
using Microsoft.AspNetCore.Authentication.Cookies;
using Microsoft.AspNetCore.Identity;
using Microsoft.Extensions.Options;

using Vantigo.Configuration;

namespace Vantigo.Identity.Services;

/// <summary>
/// Bounds and revokes application cookie sessions.
///
/// The cookie's sliding window alone lets an actively used session renew forever,
/// so every validation additionally enforces an absolute lifetime measured from
/// sign-in and an idle window, both tighter for privileged (Owner, SystemAdmin)
/// sessions. The same pass performs the server-side revocation check: the security
/// stamp carried by the cookie must still match the stored one, which is how a
/// revoked, password-changed, or MFA-changed account loses its live sessions.
///
/// The account read behind that check is cached for
/// <see cref="SessionAuthenticationOptions.RevocationCacheDuration"/>, but only as a
/// fast path for admitting a request: any negative outcome is re-read from the
/// database before the session is rejected, and <see cref="Invalidate"/> drops the
/// entry when sessions are revoked deliberately. A stale cache can therefore delay
/// an incidental revocation briefly, but can never sign a valid session out.
/// </summary>
public sealed class SessionValidationService(
    ScimLifecycleService lifecycle,
    SessionStateCache cache,
    IOptions<VantigoAuthenticationOptions> authenticationOptions,
    IOptions<IdentityOptions> identityOptions,
    ILogger<SessionValidationService> logger)
{
    /// <summary>Sign-in time of the session, as unix seconds. Survives cookie renewal.</summary>
    internal const string StartedAtPropertyKey = "vantigo.session.started";

    /// <summary>Last observed activity, as unix seconds. Drives the idle window.</summary>
    internal const string LastSeenPropertyKey = "vantigo.session.seen";

    private const string CarriedSessionItemKey = "vantigo.session.carried";

    /// <summary>
    /// Upper bound on how often the activity marker is rewritten. Each rewrite costs
    /// a Set-Cookie, so activity is tracked coarsely; the idle window is only ever
    /// enforced this much later than the true last request.
    /// </summary>
    private static readonly TimeSpan MaxActivityWriteInterval = TimeSpan.FromMinutes(5);

    /// <summary>
    /// Validates the session bounds and the revocation stamp, signing the session
    /// out when either fails. Runs on every cookie validation.
    /// </summary>
    public async Task ValidateAsync(CookieValidatePrincipalContext context)
    {
        ClaimsPrincipal? principal = context.Principal;
        if (principal?.Identity?.IsAuthenticated != true)
        {
            return;
        }

        if (!Guid.TryParse(principal.FindFirst(ClaimTypes.NameIdentifier)?.Value, out Guid userId))
        {
            await RejectAsync(context, Guid.Empty, "the cookie carries no usable subject");
            return;
        }

        SessionAuthenticationOptions sessions = authenticationOptions.Value.Sessions;
        bool privileged = IsPrivileged(principal);
        TimeSpan absoluteLifetime = privileged ? sessions.PrivilegedAbsoluteLifetime : sessions.AbsoluteLifetime;
        TimeSpan idleTimeout = privileged ? sessions.PrivilegedIdleTimeout : sessions.IdleTimeout;

        DateTimeOffset now = DateTimeOffset.UtcNow;
        DateTimeOffset issuedAt = context.Properties.IssuedUtc ?? now;
        // A cookie issued before this check existed carries neither marker. Adopting
        // its issue time starts the clock instead of signing every live session out
        // the moment this ships.
        DateTimeOffset startedAt = ReadTimestamp(context.Properties, StartedAtPropertyKey) ?? issuedAt;
        DateTimeOffset lastSeenAt = ReadTimestamp(context.Properties, LastSeenPropertyKey) ?? issuedAt;

        if (now - startedAt >= absoluteLifetime)
        {
            await RejectAsync(context, userId, "the absolute session lifetime elapsed");
            return;
        }

        if (now - lastSeenAt >= idleTimeout)
        {
            await RejectAsync(context, userId, "the session exceeded its idle window");
            return;
        }

        if (!await IsCurrentAsync(context, principal, userId))
        {
            return;
        }

        bool renew = false;
        if (!context.Properties.Items.ContainsKey(StartedAtPropertyKey))
        {
            WriteTimestamp(context.Properties, StartedAtPropertyKey, startedAt);
            renew = true;
        }

        if (now - lastSeenAt >= ActivityWriteInterval(idleTimeout))
        {
            WriteTimestamp(context.Properties, LastSeenPropertyKey, now);
            renew = true;
        }

        if (renew)
        {
            context.ShouldRenew = true;
        }

        // Reissuing the cookie later in this request (tenant switch, profile update,
        // MFA change) must not restart the absolute clock.
        context.HttpContext.Items[CarriedSessionItemKey] = new CarriedSession(userId, startedAt);
    }

    /// <summary>
    /// Drops the cached account state so the next validation re-reads it. Called
    /// when sessions are revoked so revocation does not wait for the cache.
    /// </summary>
    public void Invalidate(Guid userId) => cache.Invalidate(userId);

    /// <summary>
    /// Keeps the absolute session clock across a cookie reissued for the same user
    /// within one request. A credential sign-in has no carried session, so it always
    /// starts a new clock.
    /// </summary>
    internal static void CarryForwardSessionStart(CookieSigningInContext context)
    {
        if (context.Properties.Items.ContainsKey(StartedAtPropertyKey))
        {
            return;
        }

        if (context.HttpContext.Items.TryGetValue(CarriedSessionItemKey, out object? carried) &&
            carried is CarriedSession session &&
            Guid.TryParse(context.Principal?.FindFirst(ClaimTypes.NameIdentifier)?.Value, out Guid userId) &&
            session.UserId == userId)
        {
            WriteTimestamp(context.Properties, StartedAtPropertyKey, session.StartedAt);
        }
    }

    /// <summary>
    /// Forgets the carried session so the cookie about to be issued starts a fresh
    /// absolute lifetime. Credential sign-in paths call this: presenting credentials
    /// again is what earns a new session, and nothing else may extend one.
    /// </summary>
    public static void BeginFreshSession(HttpContext httpContext) =>
        httpContext.Items.Remove(CarriedSessionItemKey);

    private async Task<bool> IsCurrentAsync(
        CookieValidatePrincipalContext context,
        ClaimsPrincipal principal,
        Guid userId)
    {
        string? sessionStamp = principal.FindFirst(identityOptions.Value.ClaimsIdentity.SecurityStampClaimType)?.Value;
        if (IsAdmissible(cache.Get(userId), sessionStamp))
        {
            return true;
        }

        AccountSessionState? current = await lifecycle.LoadSessionStateAsync(userId, context.HttpContext.RequestAborted);
        if (current is not null)
        {
            cache.Set(userId, current, authenticationOptions.Value.Sessions.RevocationCacheDuration);
        }
        else
        {
            cache.Invalidate(userId);
        }

        if (IsAdmissible(current, sessionStamp))
        {
            return true;
        }

        await RejectAsync(context, userId, current is null or { IsEffectivelyDisabled: true }
            ? "the account is no longer active"
            : "the session was revoked");
        return false;
    }

    private static bool IsAdmissible(AccountSessionState? state, string? sessionStamp) =>
        state is { IsEffectivelyDisabled: false } &&
        string.Equals(state.SecurityStamp, sessionStamp, StringComparison.Ordinal);

    private async Task RejectAsync(CookieValidatePrincipalContext context, Guid userId, string reason)
    {
        logger.LogInformation("Rejected the application cookie for user {UserId} because {Reason}.", userId, reason);
        context.RejectPrincipal();
        await context.HttpContext.SignOutAsync(IdentityConstants.ApplicationScheme);
    }

    private static bool IsPrivileged(ClaimsPrincipal principal) =>
        principal.IsInRole(AuthRoles.Owner) || principal.IsInRole(AuthRoles.SystemAdmin);

    private static TimeSpan ActivityWriteInterval(TimeSpan idleTimeout)
    {
        TimeSpan quarter = idleTimeout / 4;
        return quarter < MaxActivityWriteInterval ? quarter : MaxActivityWriteInterval;
    }

    private static DateTimeOffset? ReadTimestamp(AuthenticationProperties properties, string key) =>
        properties.Items.TryGetValue(key, out string? value) &&
        long.TryParse(value, NumberStyles.Integer, CultureInfo.InvariantCulture, out long seconds)
            ? DateTimeOffset.FromUnixTimeSeconds(seconds)
            : null;

    private static void WriteTimestamp(AuthenticationProperties properties, string key, DateTimeOffset value) =>
        properties.Items[key] = value.ToUnixTimeSeconds().ToString(CultureInfo.InvariantCulture);

    private sealed record CarriedSession(Guid UserId, DateTimeOffset StartedAt);
}