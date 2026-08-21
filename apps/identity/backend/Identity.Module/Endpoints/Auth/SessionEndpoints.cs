using System.Security.Claims;

using Microsoft.AspNetCore.Identity;

using Vantigo.Identity.Authorization;
using Vantigo.Identity.Database.Accounts;
using Vantigo.Identity.Services;

namespace Vantigo.Identity.Endpoints.Auth;

/// <summary>
/// Server-side session revocation. Rotating the account's security stamp
/// invalidates every cookie already issued to it, which is the only way to cut off
/// a session that is being actively renewed from somewhere else.
/// </summary>
internal static class SessionEndpoints
{
    internal static void Map(IEndpointRouteBuilder app)
    {
        app.MapPost("/api/v1/identity/account/sessions/revoke", RevokeOwnSessions)
            .WithTags("Personal account settings")
            .WithSummary("Sign out of every session for the current account")
            .RequireAuthorization("ActiveAccount");

        app.MapPost("/api/v1/identity/system/users/{userId:guid}/sessions/revoke", RevokeUserSessions)
            .WithTags("Identity system")
            .WithSummary("Revoke every session for an account")
            .RequireAuthorization(AuthPolicies.SystemAdmin);
    }

    private static async Task<IResult> RevokeOwnSessions(
        HttpContext httpContext,
        ClaimsPrincipal principal,
        UserManager<ApplicationUser> userManager,
        SignInManager<ApplicationUser> signInManager,
        SessionValidationService sessions,
        AccountsDbContext dbContext,
        AuthorizationAuditWriter auditWriter,
        CancellationToken cancellationToken)
    {
        ApplicationUser? user = await userManager.GetUserAsync(principal);
        if (user is null)
        {
            return Error(StatusCodes.Status401Unauthorized, "unauthenticated", "Authentication is required.");
        }

        IResult? failure = await RevokeAsync(httpContext, user, userManager, sessions, dbContext, auditWriter,
            user.Id, cancellationToken);
        if (failure is not null)
        {
            return failure;
        }

        // The caller's own cookie was issued against the old stamp, so it is one of
        // the sessions that was just revoked. Clear it rather than leaving the
        // browser to discover the rejection on its next request.
        await signInManager.SignOutAsync();
        return TypedResults.Ok(new SessionRevocationResponse(user.Id, true));
    }

    private static async Task<IResult> RevokeUserSessions(
        Guid userId,
        HttpContext httpContext,
        ClaimsPrincipal principal,
        UserManager<ApplicationUser> userManager,
        SignInManager<ApplicationUser> signInManager,
        SessionValidationService sessions,
        AccountsDbContext dbContext,
        AuthorizationAuditWriter auditWriter,
        CancellationToken cancellationToken)
    {
        ApplicationUser? actor = await userManager.GetUserAsync(principal);
        if (actor is null)
        {
            return Error(StatusCodes.Status401Unauthorized, "unauthenticated", "Authentication is required.");
        }

        ApplicationUser? target = await userManager.FindByIdAsync(userId.ToString());
        if (target is null)
        {
            return Error(StatusCodes.Status404NotFound, "user_not_found", "The account does not exist.");
        }

        IResult? failure = await RevokeAsync(httpContext, target, userManager, sessions, dbContext, auditWriter,
            actor.Id, cancellationToken);
        if (failure is not null)
        {
            return failure;
        }

        if (target.Id == actor.Id)
        {
            await signInManager.SignOutAsync();
        }

        return TypedResults.Ok(new SessionRevocationResponse(target.Id, true));
    }

    private static async Task<IResult?> RevokeAsync(
        HttpContext httpContext,
        ApplicationUser target,
        UserManager<ApplicationUser> userManager,
        SessionValidationService sessions,
        AccountsDbContext dbContext,
        AuthorizationAuditWriter auditWriter,
        Guid actorUserId,
        CancellationToken cancellationToken)
    {
        IdentityResult stamp = await userManager.UpdateSecurityStampAsync(target);
        if (!stamp.Succeeded)
        {
            return Error(StatusCodes.Status500InternalServerError, "session_revocation_failed",
                "The sessions could not be revoked.");
        }

        // Without this the cached account state could keep admitting the revoked
        // cookie until the cache entry expires.
        sessions.Invalidate(target.Id);
        await auditWriter.WriteAsync(dbContext, httpContext, actorUserId, target.Id, null, "user.sessions-revoked",
            new { sessionsRevoked = false },
            new { sessionsRevoked = true, revokedAt = DateTimeOffset.UtcNow },
            cancellationToken);
        return null;
    }

    private static IResult Error(int statusCode, string code, string message) =>
        TypedResults.Json(new AuthErrorResponse(new AuthError(code, message)), statusCode: statusCode);
}

internal sealed record SessionRevocationResponse(Guid UserId, bool Revoked);