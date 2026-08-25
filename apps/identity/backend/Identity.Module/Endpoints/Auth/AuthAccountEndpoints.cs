using System.Security.Claims;
using System.Text;

using Microsoft.AspNetCore.Identity;
using Microsoft.AspNetCore.WebUtilities;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Options;

using Npgsql;

using Vantigo.Configuration;
using Vantigo.Identity.Authorization;
using Vantigo.Identity.Database.Accounts;
using Vantigo.Identity.Services;

namespace Vantigo.Identity.Endpoints.Auth;

internal static class AuthAccountEndpoints
{
    internal static IEndpointRouteBuilder MapAccountAuthEndpoints(this IEndpointRouteBuilder app)
    {
        var owner = app.MapGroup("/api/v1/identity/owner")
            .WithTags("Owner account management")
            .RequireAuthorization(AuthPolicies.Owner);

        var ownerManagement = owner.MapGroup("")
            .RequireAuthorization(AuthPolicies.OwnerManagement);
        ownerManagement.AddEndpointFilter(async (context, next) =>
        {
            try
            {
                return await next(context);
            }
            catch (Exception exception) when (AuthAccountState.IsExpectedConflict(exception))
            {
                return Error(StatusCodes.Status409Conflict, "account_conflict",
                    "The account change conflicts with another account change.");
            }
        });

        ownerManagement.MapPost("/invitations", CreateInvitation)

            .RequireRateLimiting(AuthRateLimitPolicies.Invitations);
        ownerManagement.MapGet("/invitations", ListInvitations)
            .RequireRateLimiting(AuthRateLimitPolicies.Invitations);
        ownerManagement.MapPost("/invitations/{id:guid}/revoke", RevokeInvitation)

            .RequireRateLimiting(AuthRateLimitPolicies.Invitations);
        ownerManagement.MapPost("/invitations/{id:guid}/resend", ResendInvitation)

            .RequireRateLimiting(AuthRateLimitPolicies.Invitations);

        // These are self-service endpoints. Keep the established URL contract,
        // but do not inherit the Owner group policy used by the surrounding
        // account-management endpoints.
        var selfMfa = app.MapGroup("/api/v1/identity/owner/mfa").RequireAuthorization("ActiveAccount");
        selfMfa.MapGet("", MfaStatus).RequireRateLimiting(AuthRateLimitPolicies.Mfa);
        selfMfa.MapGet("/setup", MfaSetup).RequireRateLimiting(AuthRateLimitPolicies.Mfa);
        selfMfa.MapPost("/setup", InitializeMfa).RequireRateLimiting(AuthRateLimitPolicies.Mfa);
        selfMfa.MapPost("/enable", EnableMfa).RequireRateLimiting(AuthRateLimitPolicies.Mfa);
        selfMfa.MapPost("/disable", DisableMfa).RequireRateLimiting(AuthRateLimitPolicies.Mfa);
        selfMfa.MapPost("/recovery-codes", RegenerateRecoveryCodes).RequireRateLimiting(AuthRateLimitPolicies.Mfa);

        var accountMfa = app.MapGroup("/api/v1/identity/account/mfa").RequireAuthorization("ActiveAccount");
        accountMfa.MapGet("", MfaStatus).RequireRateLimiting(AuthRateLimitPolicies.Mfa);
        accountMfa.MapGet("/setup", MfaSetup).RequireRateLimiting(AuthRateLimitPolicies.Mfa);
        accountMfa.MapPost("/setup", InitializeMfa).RequireRateLimiting(AuthRateLimitPolicies.Mfa);
        accountMfa.MapPost("/enable", EnableMfa).RequireRateLimiting(AuthRateLimitPolicies.Mfa);
        accountMfa.MapPost("/disable", DisableMfa).RequireRateLimiting(AuthRateLimitPolicies.Mfa);
        accountMfa.MapPost("/recovery-codes", RegenerateRecoveryCodes).RequireRateLimiting(AuthRateLimitPolicies.Mfa);
        ownerManagement.MapPost("/mfa/reset/{userId:guid}", ResetOwnerMfa)

            .RequireRateLimiting(AuthRateLimitPolicies.Mfa);
        ownerManagement.MapGet("/users", ListUsers)
            .RequireRateLimiting(AuthRateLimitPolicies.UserManagement);
        ownerManagement.MapGet("/users/{id:guid}/avatar", GetManagedUserAvatar)
            .RequireRateLimiting(AuthRateLimitPolicies.OwnerAvatarRead);
        ownerManagement.MapPost("/users", CreateUser)
            .RequireRateLimiting(AuthRateLimitPolicies.UserManagement);
        ownerManagement.MapPut("/users/{id:guid}", UpdateUser)
            .RequireRateLimiting(AuthRateLimitPolicies.UserManagement);
        ownerManagement.MapPost("/users/{id:guid}/password-reset", SendUserPasswordReset)
            .RequireRateLimiting(AuthRateLimitPolicies.UserManagement);
        ownerManagement.MapPost("/users/{id:guid}/password", SetUserPassword)
            .RequireRateLimiting(AuthRateLimitPolicies.UserManagement);
        ownerManagement.MapPost("/users/{id:guid}/disable", DisableUser)
            .RequireRateLimiting(AuthRateLimitPolicies.UserManagement);
        ownerManagement.MapPost("/users/{id:guid}/enable", EnableUser)
            .RequireRateLimiting(AuthRateLimitPolicies.UserManagement);
        ownerManagement.MapDelete("/users/{id:guid}", DeleteUser)
            .RequireRateLimiting(AuthRateLimitPolicies.UserManagement);

        app.MapGet("/api/v1/identity/invitations/validate", ValidateInvitation)
            .RequireRateLimiting(AuthRateLimitPolicies.InvitationAcceptance);
        app.MapPost("/api/v1/identity/invitations/accept", AcceptInvitation)

            .RequireRateLimiting(AuthRateLimitPolicies.InvitationAcceptance);
        app.MapPost("/api/v1/identity/password-recovery/request", RequestPasswordRecovery)

            .RequireRateLimiting(AuthRateLimitPolicies.PasswordRecovery);
        app.MapPost("/api/v1/identity/password-recovery/reset", ResetPassword)

            .RequireRateLimiting(AuthRateLimitPolicies.PasswordRecovery);

        return app;
    }

    private static async Task<IResult> MfaStatus(
        ClaimsPrincipal principal,
        UserManager<ApplicationUser> userManager,
        TenantMembershipService tenantMembershipService,
        IOptions<VantigoAuthenticationOptions> options,
        SignInManager<ApplicationUser> signInManager)
    {
        var user = await userManager.GetUserAsync(principal);
        if (user is null)
        {
            return Error(StatusCodes.Status401Unauthorized, "unauthenticated", "Authentication is required.");
        }

        var isOwner = await userManager.IsInRoleAsync(user, AuthRoles.Owner);
        return TypedResults.Ok(new MfaStatusResponse(
            user.TwoFactorEnabled,
            isOwner && options.Value.Owners.RequireMfa && !user.TwoFactorEnabled));
    }

    private static async Task<IResult> MfaSetup(
        ClaimsPrincipal principal,
        UserManager<ApplicationUser> userManager,
        IOptions<VantigoAuthenticationOptions> options)
    {
        var user = await userManager.GetUserAsync(principal);
        if (user is null)
        {
            return Error(StatusCodes.Status401Unauthorized, "unauthenticated", "Authentication is required.");
        }

        var initialized = !string.IsNullOrWhiteSpace(await userManager.GetAuthenticatorKeyAsync(user));
        // The setup secret is only returned by the authenticated POST that
        // initializes enrollment. Never replay it from a GET endpoint.
        return TypedResults.Ok(new MfaSetupResponse(null, null, initialized));
    }

    private static async Task<IResult> InitializeMfa(
        ClaimsPrincipal principal,
        UserManager<ApplicationUser> userManager,
        SignInManager<ApplicationUser> signInManager,
        IOptions<VantigoAuthenticationOptions> options,
        MfaCodeRequest? request)
    {
        var user = await userManager.GetUserAsync(principal);
        if (user is null)
        {
            return Error(StatusCodes.Status401Unauthorized, "unauthenticated", "Authentication is required.");
        }

        var reauthentication = await RequireLocalPassword(user, request?.Password, userManager);
        if (reauthentication is not null)
        {
            return reauthentication;
        }

        var result = await userManager.ResetAuthenticatorKeyAsync(user);
        if (!result.Succeeded)
        {
            return IdentityFailure(result, "MFA setup could not be initialized.");
        }

        var key = await userManager.GetAuthenticatorKeyAsync(user);
        if (string.IsNullOrWhiteSpace(key))
        {
            return Error(StatusCodes.Status500InternalServerError, "mfa_setup_failed", "MFA setup could not be initialized.");
        }

        // ResetAuthenticatorKeyAsync updates the security stamp. Reissue the
        // browser cookie now so immediate security-stamp validation does not
        // invalidate the caller between setup and code verification.
        await signInManager.SignInWithClaimsAsync(
            user,
            new Microsoft.AspNetCore.Authentication.AuthenticationProperties { IsPersistent = false },
            principal.Claims.Where(claim => claim.Type is "amr" or ClaimTypes.AuthenticationMethod)
                .Where(claim => !MfaClaims.Is(claim)).ToArray());

        var issuer = Uri.EscapeDataString(options.Value.Owners.MfaIssuer);
        var account = Uri.EscapeDataString(user.Email ?? user.UserName ?? user.Id.ToString());
        var uri = $"otpauth://totp/{issuer}:{account}?secret={key}&issuer={issuer}&digits=6";
        return TypedResults.Ok(new MfaSetupResponse(key, uri, true));
    }

    private static async Task<IResult> EnableMfa(
        MfaCodeRequest? request,
        ClaimsPrincipal principal,
        UserManager<ApplicationUser> userManager,
        SignInManager<ApplicationUser> signInManager,
        CancellationToken cancellationToken)
    {
        var user = await userManager.GetUserAsync(principal);
        if (user is null)
        {
            return Error(StatusCodes.Status401Unauthorized, "unauthenticated", "Authentication is required.");
        }

        var reauthentication = await RequireLocalPassword(user, request?.Password, userManager);
        if (reauthentication is not null)
        {
            return reauthentication;
        }

        if (string.IsNullOrWhiteSpace(request?.Code) ||
            !await userManager.VerifyTwoFactorTokenAsync(user, TokenOptions.DefaultAuthenticatorProvider, request.Code.Replace(" ", string.Empty, StringComparison.Ordinal)))
        {
            return Error(StatusCodes.Status400BadRequest, "invalid_mfa_code", "The authenticator code is invalid.");
        }

        var enabled = await userManager.SetTwoFactorEnabledAsync(user, true);
        if (!enabled.Succeeded)
        {
            return IdentityFailure(enabled, "MFA could not be enabled.");
        }

        var codes = await userManager.GenerateNewTwoFactorRecoveryCodesAsync(user, 10);
        var historicalMfaClaims = (await userManager.GetClaimsAsync(user)).Where(MfaClaims.Is).ToArray();
        if (historicalMfaClaims.Length > 0)
        {
            var cleanup = await userManager.RemoveClaimsAsync(user, historicalMfaClaims);
            if (!cleanup.Succeeded)
            {
                return IdentityFailure(cleanup, "MFA could not be enabled.");
            }
        }

        await signInManager.SignInWithClaimsAsync(
            user,
            new Microsoft.AspNetCore.Authentication.AuthenticationProperties { IsPersistent = false },
            MfaClaims.Issue());
        return TypedResults.Ok(new MfaEnableResponse(true, codes?.ToArray() ?? []));
    }

    private static async Task<IResult> DisableMfa(
        MfaCodeRequest? request,
        ClaimsPrincipal principal,
        UserManager<ApplicationUser> userManager,
        AccountsDbContext dbContext,
        IOptions<VantigoAuthenticationOptions> options,
        SignInManager<ApplicationUser> signInManager)
    {
        var user = await userManager.GetUserAsync(principal);
        if (user is null)
        {
            return Error(StatusCodes.Status401Unauthorized, "unauthenticated", "Authentication is required.");
        }

        var activeDelegatedAdministrator = await dbContext.AuthorizationDelegations.AsNoTracking().AnyAsync(item =>
            item.GranteeUserId == user.Id && item.RevokedAt == null &&
            (item.ExpiresAt == null || item.ExpiresAt > DateTimeOffset.UtcNow));
        if (options.Value.Owners.RequireMfa &&
            (await userManager.IsInRoleAsync(user, AuthRoles.Owner) || activeDelegatedAdministrator))
        {
            return Error(StatusCodes.Status403Forbidden, activeDelegatedAdministrator &&
                !await userManager.IsInRoleAsync(user, AuthRoles.Owner)
                    ? "delegated_admin_mfa_required"
                    : "mfa_required",
                "MFA cannot be disabled while privileged management access requires it.");
        }

        var reauthentication = await RequireLocalPassword(user, request?.Password, userManager);
        if (reauthentication is not null)
        {
            return reauthentication;
        }

        var result = await userManager.SetTwoFactorEnabledAsync(user, false);
        if (!result.Succeeded)
        {
            return IdentityFailure(result, "MFA could not be disabled.");
        }

        var existingClaims = (await userManager.GetClaimsAsync(user)).Where(MfaClaims.Is).ToArray();
        if (existingClaims.Length > 0)
        {
            var removeClaimsResult = await userManager.RemoveClaimsAsync(user, existingClaims);
            if (!removeClaimsResult.Succeeded)
            {
                return IdentityFailure(removeClaimsResult, "MFA could not be disabled.");
            }
        }

        var stampResult = await userManager.UpdateSecurityStampAsync(user);
        if (!stampResult.Succeeded)
        {
            return IdentityFailure(stampResult, "MFA could not be disabled.");
        }

        await signInManager.SignInWithClaimsAsync(
            user,
            new Microsoft.AspNetCore.Authentication.AuthenticationProperties { IsPersistent = false },
            []);
        return TypedResults.Ok(new MfaStatusResponse(false, false));
    }

    private static async Task<IResult> RegenerateRecoveryCodes(
        MfaCodeRequest? request,
        ClaimsPrincipal principal,
        UserManager<ApplicationUser> userManager)
    {
        var user = await userManager.GetUserAsync(principal);
        if (user is null)
        {
            return Error(StatusCodes.Status401Unauthorized, "unauthenticated", "Authentication is required.");
        }

        var reauthentication = await RequireLocalPassword(user, request?.Password, userManager);
        if (reauthentication is not null)
        {
            return reauthentication;
        }

        if (!user.TwoFactorEnabled || string.IsNullOrWhiteSpace(request?.Code) ||
            !await userManager.VerifyTwoFactorTokenAsync(user, TokenOptions.DefaultAuthenticatorProvider, request.Code.Replace(" ", string.Empty, StringComparison.Ordinal)))
        {
            return Error(StatusCodes.Status400BadRequest, "invalid_mfa_code", "A valid authenticator code is required.");
        }

        var codes = await userManager.GenerateNewTwoFactorRecoveryCodesAsync(user, 10);
        return TypedResults.Ok(new MfaRecoveryCodesResponse(codes?.ToArray() ?? []));
    }

    private static async Task<IResult> ResetOwnerMfa(
        Guid userId,
        ClaimsPrincipal principal,
        UserManager<ApplicationUser> userManager,
        IOptions<VantigoAuthenticationOptions> options)
    {
        var caller = await userManager.GetUserAsync(principal);
        if (caller is null || !await userManager.IsInRoleAsync(caller, AuthRoles.Owner) || !MfaClaims.Any(principal.Claims))
        {
            return Error(StatusCodes.Status403Forbidden, "forbidden", "Only an Owner can reset Owner MFA.");
        }

        if (caller.Id == userId)
        {
            return Error(StatusCodes.Status400BadRequest, "invalid_request", "Use the self-service MFA endpoints for your own account.");
        }

        var target = await userManager.FindByIdAsync(userId.ToString());
        if (target is null || !await userManager.IsInRoleAsync(target, AuthRoles.Owner))
        {
            return TypedResults.NotFound();
        }

        var resetKeyResult = await userManager.ResetAuthenticatorKeyAsync(target);
        if (!resetKeyResult.Succeeded)
        {
            return MfaResetFailure(resetKeyResult, "resetting the authenticator key");
        }

        var disableResult = await userManager.SetTwoFactorEnabledAsync(target, false);
        if (!disableResult.Succeeded)
        {
            return MfaResetFailure(disableResult, "disabling two-factor authentication");
        }

        // Recovery capability is immediately restored by a fresh recovery-code set;
        // the target must enroll a new authenticator before MFA is enabled again.
        var codes = await userManager.GenerateNewTwoFactorRecoveryCodesAsync(target, 10);
        var targetClaims = (await userManager.GetClaimsAsync(target)).Where(MfaClaims.Is).ToArray();
        if (targetClaims.Length > 0)
        {
            var removeClaimsResult = await userManager.RemoveClaimsAsync(target, targetClaims);
            if (!removeClaimsResult.Succeeded)
            {
                return MfaResetFailure(removeClaimsResult, "removing persisted MFA claims");
            }
        }

        var stampResult = await userManager.UpdateSecurityStampAsync(target);
        if (!stampResult.Succeeded)
        {
            return MfaResetFailure(stampResult, "invalidating existing sessions");
        }

        return TypedResults.Ok(new MfaResetResponse(target.Id, codes?.ToArray() ?? []));
    }

    private static async Task<IResult?> RequireLocalPassword(
        ApplicationUser user,
        string? password,
        UserManager<ApplicationUser> userManager)
    {
        if (user.PasswordHash is null)
        {
            return Error(StatusCodes.Status409Conflict, "local_password_unavailable",
                "This OIDC-only account does not have a local password.");
        }

        return string.IsNullOrEmpty(password) || !await userManager.CheckPasswordAsync(user, password)
            ? Error(StatusCodes.Status400BadRequest, "reauthentication_required", "The current password is invalid.")
            : null;
    }

    private static async Task<IResult> ListUsers(
        AccountsDbContext dbContext,
        WorkforceOidcOptions oidcOptions,
        CancellationToken cancellationToken)
    {
        var oidcLoginProvider = WorkforceOidcOptions.TryNormalizeIssuer(oidcOptions.Authority, out var normalizedAuthority)
            ? normalizedAuthority
            : null;
        var users = await dbContext.Users
            .AsNoTracking()
            .OrderBy(user => user.DisplayName)
            .ThenBy(user => user.Id)
            .Select(user => new
            {
                user.Id,
                user.DisplayName,
                user.Email,
                user.IsDisabled,
                user.LockoutEnd,
                user.TwoFactorEnabled,
                AvatarUrl = dbContext.ProfileAvatars
                    .Where(avatar => avatar.UserId == user.Id)
                    .Select(_ => $"/api/v1/identity/owner/users/{user.Id}/avatar")
                    .SingleOrDefault(),
                SsoEnabled = oidcLoginProvider != null && dbContext.UserLogins.Any(login => login.UserId == user.Id &&
                    login.LoginProvider == oidcLoginProvider),
            })
            .ToListAsync(cancellationToken);

        var roleAssignments = await dbContext.UserRoles
            .Join(dbContext.Roles, userRole => userRole.RoleId, role => role.Id,
                (userRole, role) => new { userRole.UserId, role.Name })
            .ToListAsync(cancellationToken);
        var rolesByUser = roleAssignments
            .GroupBy(item => item.UserId)
            .ToDictionary(group => group.Key,
                group => AuthRoleOrdering.ManagedRole(group.Select(item => item.Name)));

        var now = DateTimeOffset.UtcNow;
        var response = users.Select(user => new OwnerUserResponse(
            user.Id,
            user.DisplayName,
            user.Email,
            rolesByUser.GetValueOrDefault(user.Id, AuthRoles.User),
            !user.IsDisabled && !AuthAccountState.IsLockedOut(user.LockoutEnd, now),
            user.IsDisabled,
            AuthAccountState.IsLockedOut(user.LockoutEnd, now),
            user.LockoutEnd,
            user.TwoFactorEnabled,
            user.AvatarUrl,
            user.SsoEnabled))
            .OrderBy(user => string.Equals(user.Role, AuthRoles.Owner, StringComparison.Ordinal) ? 0 : 1)
            .ThenBy(user => user.DisplayName, StringComparer.Ordinal)
            .ThenBy(user => user.Id)
            .ToArray();
        return TypedResults.Ok(response);
    }

    private static async Task<IResult> GetManagedUserAvatar(
        Guid id,
        AccountsDbContext dbContext,
        CancellationToken cancellationToken,
        HttpResponse response)
    {
        var avatar = await dbContext.ProfileAvatars.AsNoTracking()
            .Where(item => item.UserId == id)
            .Select(item => new { item.Data, item.ContentType })
            .SingleOrDefaultAsync(cancellationToken);
        if (avatar is null)
        {
            return TypedResults.NotFound();
        }

        response.Headers.CacheControl = "private, no-store";
        response.Headers.ContentDisposition = "inline";
        response.Headers["X-Content-Type-Options"] = "nosniff";
        return TypedResults.Bytes(avatar.Data, avatar.ContentType);
    }

    private static async Task<IResult> CreateUser(
        OwnerUserCreateRequest? request,
        HttpContext httpContext,
        AccountsDbContext dbContext,
        UserManager<ApplicationUser> userManager,
        AuthorizationAuditWriter auditWriter,
        TenantMembershipService tenantMembershipService,
        CancellationToken cancellationToken)
    {
        var validation = ValidateOwnerUserCreateRequest(request);
        if (validation.Count > 0)
        {
            return Error(StatusCodes.Status400BadRequest, "invalid_request", "The user request is invalid.", validation);
        }

        var email = request!.Email!.Trim();
        var password = string.IsNullOrWhiteSpace(request.TemporaryPassword)
            ? request.Password
            : request.TemporaryPassword;
        await using var transaction = await dbContext.Database.BeginTransactionAsync(
            System.Data.IsolationLevel.Serializable, cancellationToken);
        if (string.Equals(request.Role, AuthRoles.Owner, StringComparison.Ordinal))
        {
            await AuthAccountState.AcquireOwnerMutationLock(dbContext, cancellationToken);
        }
        if (await userManager.FindByEmailAsync(email) is not null)
        {
            return Error(StatusCodes.Status409Conflict, "account_exists", "An account already exists for this email address.");
        }

        await RevokeActiveInvitations(dbContext, NormalizeEmail(email), DateTimeOffset.UtcNow, cancellationToken);
        var user = new ApplicationUser
        {
            UserName = email,
            Email = email,
            EmailConfirmed = true,
            DisplayName = request.DisplayName!.Trim(),
        };
        IdentityResult createResult;
        try
        {
            createResult = await userManager.CreateAsync(user, password!);
        }
        catch (Exception exception) when (AuthAccountState.IsExpectedConflict(exception))
        {
            return Error(StatusCodes.Status409Conflict, "account_conflict", "The user account conflicts with another account change.");
        }
        if (!createResult.Succeeded)
        {
            return IdentityFailure(createResult, "The user account could not be created.");
        }

        await tenantMembershipService.EnsureDefaultMembershipAsync(user.Id, cancellationToken);

        var roleResult = await userManager.AddToRoleAsync(user, request.Role!);
        if (!roleResult.Succeeded)
        {
            return IdentityFailure(roleResult, "The user account could not be assigned its role.");
        }

        var actor = await userManager.GetUserAsync(httpContext.User);
        var after = await auditWriter.CaptureUserAsync(dbContext, user.Id, cancellationToken);
        await auditWriter.WriteAsync(dbContext, httpContext, actor?.Id, user.Id, null,
            "user.created-with-role", new { UserId = (Guid?)null, Roles = Array.Empty<string>(), PermissionKeys = Array.Empty<string>() }, after, cancellationToken);

        try
        {
            await transaction.CommitAsync(cancellationToken);
            return TypedResults.Created($"/api/v1/identity/owner/users/{user.Id}",
                new OwnerUserResponse(user.Id, user.DisplayName, user.Email, request.Role!, true, false, false, null, false));
        }
        catch (Exception exception) when (AuthAccountState.IsExpectedConflict(exception))
        {
            return Error(StatusCodes.Status409Conflict, "account_conflict", "The user account conflicts with another account change.");
        }
    }

    private static async Task<IResult> UpdateUser(
        Guid id,
        OwnerUserUpdateRequest? request,
        ClaimsPrincipal principal,
        HttpContext httpContext,
        AccountsDbContext dbContext,
        UserManager<ApplicationUser> userManager,
        AuthorizationAuditWriter auditWriter,
        CancellationToken cancellationToken)
    {
        var validation = ValidateOwnerUserUpdateRequest(request);
        if (validation.Count > 0)
        {
            return Error(StatusCodes.Status400BadRequest, "invalid_request", "The user request is invalid.", validation);
        }

        var caller = await userManager.GetUserAsync(principal);
        if (caller is null)
        {
            return Error(StatusCodes.Status401Unauthorized, "unauthenticated", "Authentication is required.");
        }

        var target = await userManager.FindByIdAsync(id.ToString());
        if (target is null)
        {
            return TypedResults.NotFound();
        }

        var selfError = RejectSelfManagement(caller, target);
        if (selfError is not null)
        {
            return selfError;
        }

        var email = request!.Email!.Trim();
        var existingEmail = await userManager.FindByEmailAsync(email);
        if (existingEmail is not null && existingEmail.Id != target.Id)
        {
            return Error(StatusCodes.Status409Conflict, "account_exists", "An account already exists for this email address.");
        }

        await using var transaction = await dbContext.Database.BeginTransactionAsync(
            System.Data.IsolationLevel.Serializable, cancellationToken);
        await AuthAccountState.AcquireOwnerMutationLock(dbContext, cancellationToken);
        await dbContext.Entry(target).ReloadAsync(cancellationToken);
        var currentRoles = await userManager.GetRolesAsync(target);
        var isDemotion = currentRoles.Contains(AuthRoles.Owner, StringComparer.Ordinal) &&
            string.Equals(request.Role, AuthRoles.User, StringComparison.Ordinal);
        if (isDemotion && await AuthAccountState.IsLastActiveOwner(dbContext, target.Id, DateTimeOffset.UtcNow, cancellationToken))
        {
            return Error(StatusCodes.Status409Conflict, "last_active_owner", "The last active Owner cannot be demoted.");
        }

        var oldEmail = target.Email;
        var requestedDisplayName = request.DisplayName!;
        var detailsChanged = !string.Equals(target.DisplayName, requestedDisplayName.Trim(), StringComparison.Ordinal) ||
            !string.Equals(oldEmail, email, StringComparison.OrdinalIgnoreCase);

        if (!string.Equals(oldEmail, email, StringComparison.OrdinalIgnoreCase))
        {
            await RevokeActiveInvitations(dbContext, NormalizeEmail(oldEmail ?? string.Empty), DateTimeOffset.UtcNow, cancellationToken);
            await RevokeActiveInvitations(dbContext, NormalizeEmail(email), DateTimeOffset.UtcNow, cancellationToken);
        }

        target.DisplayName = request.DisplayName!.Trim();
        target.Email = email;
        target.UserName = email;
        target.EmailConfirmed = true;
        IdentityResult updateResult;
        try
        {
            updateResult = await userManager.UpdateAsync(target);
        }
        catch (Exception exception) when (AuthAccountState.IsExpectedConflict(exception))
        {
            return Error(StatusCodes.Status409Conflict, "account_conflict", "The user account conflicts with another account change.");
        }
        if (!updateResult.Succeeded)
        {
            return IdentityFailure(updateResult, "The user account could not be updated.");
        }

        var managedRoles = currentRoles.Where(IsManagedRole).ToArray();
        var roleChanged = managedRoles.Length != 1 || !string.Equals(managedRoles[0], request.Role, StringComparison.Ordinal);
        if (roleChanged)
        {
            if (managedRoles.Length > 0)
            {
                var removeRolesResult = await userManager.RemoveFromRolesAsync(target, managedRoles);
                if (!removeRolesResult.Succeeded)
                {
                    return IdentityFailure(removeRolesResult, "The user account roles could not be updated.");
                }
            }

            var addRoleResult = await userManager.AddToRoleAsync(target, request.Role!);
            if (!addRoleResult.Succeeded)
            {
                return IdentityFailure(addRoleResult, "The user account role could not be assigned.");
            }

            await auditWriter.WriteAsync(dbContext, httpContext, caller.Id, target.Id, null,
                "user.role-changed", new { Roles = currentRoles },
                new { Roles = await userManager.GetRolesAsync(target) }, cancellationToken);
        }

        if (detailsChanged || roleChanged)
        {
            var stampResult = await userManager.UpdateSecurityStampAsync(target);
            if (!stampResult.Succeeded)
            {
                return IdentityFailure(stampResult, "The user account sessions could not be invalidated.");
            }
        }

        try
        {
            await transaction.CommitAsync(cancellationToken);
            return TypedResults.Ok(new OwnerUserResponse(target.Id, target.DisplayName, target.Email, request.Role!,
                !target.IsDisabled && !AuthAccountState.IsLockedOut(target.LockoutEnd, DateTimeOffset.UtcNow),
                target.IsDisabled, AuthAccountState.IsLockedOut(target.LockoutEnd, DateTimeOffset.UtcNow),
                target.LockoutEnd, target.TwoFactorEnabled));
        }
        catch (Exception exception) when (AuthAccountState.IsExpectedConflict(exception))
        {
            return Error(StatusCodes.Status409Conflict, "account_conflict", "The user account conflicts with another account change.");
        }
    }

    private static async Task<IResult> SendUserPasswordReset(
        Guid id,
        ClaimsPrincipal principal,
        UserManager<ApplicationUser> userManager,
        IOptions<VantigoAuthenticationOptions> options,
        AppPublicUrls appPublicUrls,
        IApplicationEmailSender emailSender,
        CancellationToken cancellationToken)
    {
        var targetResult = await FindManagedTarget(id, principal, userManager);
        if (targetResult.Error is not null)
        {
            return targetResult.Error;
        }

        if (targetResult.User!.PasswordHash is not null &&
            targetResult.User.EmailConfirmed &&
            !string.IsNullOrWhiteSpace(targetResult.User.Email))
        {
            await SendPasswordResetEmail(targetResult.User, userManager, options, appPublicUrls, emailSender, cancellationToken);
        }

        return TypedResults.Ok(new PasswordRecoveryResponse(true));
    }

    private static async Task<IResult> SetUserPassword(
        Guid id,
        OwnerUserPasswordRequest? request,
        ClaimsPrincipal principal,
        AccountsDbContext dbContext,
        UserManager<ApplicationUser> userManager,
        CancellationToken cancellationToken)
    {
        var validation = ValidateOwnerUserPasswordRequest(request);
        if (validation.Count > 0)
        {
            return Error(StatusCodes.Status400BadRequest, "invalid_request", "The password request is invalid.", validation);
        }

        var targetResult = await FindManagedTarget(id, principal, userManager);
        if (targetResult.Error is not null)
        {
            return targetResult.Error;
        }

        await using var transaction = await dbContext.Database.BeginTransactionAsync(cancellationToken);
        var token = await userManager.GeneratePasswordResetTokenAsync(targetResult.User!);
        var resetResult = await userManager.ResetPasswordAsync(targetResult.User!, token, request!.Password!);
        if (!resetResult.Succeeded)
        {
            return IdentityFailure(resetResult, "The user password could not be changed.");
        }

        var stampResult = await userManager.UpdateSecurityStampAsync(targetResult.User!);
        if (!stampResult.Succeeded)
        {
            return IdentityFailure(stampResult, "The user account sessions could not be invalidated.");
        }

        await transaction.CommitAsync(cancellationToken);
        return TypedResults.Ok(new PasswordResetResponse(true));
    }

    private static async Task<IResult> DisableUser(
        Guid id,
        ClaimsPrincipal principal,
        HttpContext httpContext,
        AccountsDbContext dbContext,
        UserManager<ApplicationUser> userManager,
        AuthorizationAuditWriter auditWriter,
        CancellationToken cancellationToken)
    {
        var targetResult = await FindManagedTarget(id, principal, userManager);
        if (targetResult.Error is not null)
        {
            return targetResult.Error;
        }

        var target = targetResult.User!;
        await using var transaction = await dbContext.Database.BeginTransactionAsync(
            System.Data.IsolationLevel.Serializable, cancellationToken);
        await AuthAccountState.AcquireOwnerMutationLock(dbContext, cancellationToken);
        await dbContext.Entry(target).ReloadAsync(cancellationToken);
        var before = await auditWriter.CaptureUserAsync(dbContext, target.Id, cancellationToken);
        var now = DateTimeOffset.UtcNow;
        if (await AuthAccountState.IsLastActiveOwner(dbContext, target.Id, now, cancellationToken))
        {
            return Error(StatusCodes.Status409Conflict, "last_active_owner", "The last active Owner cannot be disabled.");
        }

        if (!target.IsDisabled)
        {
            target.IsDisabled = true;
            IdentityResult updateResult;
            try
            {
                updateResult = await userManager.UpdateAsync(target);
            }
            catch (Exception exception) when (AuthAccountState.IsExpectedConflict(exception))
            {
                return Error(StatusCodes.Status409Conflict, "account_conflict", "The user account conflicts with another account change.");
            }
            if (!updateResult.Succeeded)
            {
                return IdentityFailure(updateResult, "The user account could not be disabled.");
            }

            var stampResult = await userManager.UpdateSecurityStampAsync(target);
            if (!stampResult.Succeeded)
            {
                return IdentityFailure(stampResult, "The user account sessions could not be invalidated.");
            }

            await auditWriter.WriteAsync(dbContext, httpContext, (await userManager.GetUserAsync(principal))?.Id,
                target.Id, null, "user.disabled", before, before with { IsDisabled = true }, cancellationToken);
        }

        try
        {
            await transaction.CommitAsync(cancellationToken);
            return await ManagedUserResponse(target, userManager);
        }
        catch (Exception exception) when (AuthAccountState.IsExpectedConflict(exception))
        {
            return Error(StatusCodes.Status409Conflict, "account_conflict", "The user account conflicts with another account change.");
        }
    }

    private static async Task<IResult> EnableUser(
        Guid id,
        ClaimsPrincipal principal,
        HttpContext httpContext,
        AccountsDbContext dbContext,
        UserManager<ApplicationUser> userManager,
        AuthorizationAuditWriter auditWriter,
        CancellationToken cancellationToken)
    {
        var targetResult = await FindManagedTarget(id, principal, userManager);
        if (targetResult.Error is not null)
        {
            return targetResult.Error;
        }

        var target = targetResult.User!;
        await using var transaction = await dbContext.Database.BeginTransactionAsync(
            System.Data.IsolationLevel.Serializable, cancellationToken);
        await AuthAccountState.AcquireOwnerMutationLock(dbContext, cancellationToken);
        await dbContext.Entry(target).ReloadAsync(cancellationToken);
        var before = await auditWriter.CaptureUserAsync(dbContext, target.Id, cancellationToken);
        if (target.IsDisabled)
        {
            target.IsDisabled = false;
            IdentityResult updateResult;
            try
            {
                updateResult = await userManager.UpdateAsync(target);
            }
            catch (Exception exception) when (AuthAccountState.IsExpectedConflict(exception))
            {
                return Error(StatusCodes.Status409Conflict, "account_conflict", "The user account conflicts with another account change.");
            }
            if (!updateResult.Succeeded)
            {
                return IdentityFailure(updateResult, "The user account could not be enabled.");
            }

            var stampResult = await userManager.UpdateSecurityStampAsync(target);
            if (!stampResult.Succeeded)
            {
                return IdentityFailure(stampResult, "The user account sessions could not be invalidated.");
            }

            await auditWriter.WriteAsync(dbContext, httpContext, (await userManager.GetUserAsync(principal))?.Id,
                target.Id, null, "user.enabled", before, before with { IsDisabled = false }, cancellationToken);
        }

        try
        {
            await transaction.CommitAsync(cancellationToken);
            return await ManagedUserResponse(target, userManager);
        }
        catch (Exception exception) when (AuthAccountState.IsExpectedConflict(exception))
        {
            return Error(StatusCodes.Status409Conflict, "account_conflict", "The user account conflicts with another account change.");
        }
    }

    private static async Task<IResult> DeleteUser(
        Guid id,
        ClaimsPrincipal principal,
        HttpContext httpContext,
        AccountsDbContext dbContext,
        UserManager<ApplicationUser> userManager,
        AuthorizationAuditWriter auditWriter,
        CancellationToken cancellationToken)
    {
        var targetResult = await FindManagedTarget(id, principal, userManager);
        if (targetResult.Error is not null)
        {
            return targetResult.Error;
        }

        var target = targetResult.User!;
        await using var transaction = await dbContext.Database.BeginTransactionAsync(
            System.Data.IsolationLevel.Serializable, cancellationToken);
        await AuthAccountState.AcquireOwnerMutationLock(dbContext, cancellationToken);
        await dbContext.Entry(target).ReloadAsync(cancellationToken);
        var before = await auditWriter.CaptureUserAsync(dbContext, target.Id, cancellationToken);
        if (await AuthAccountState.IsLastActiveOwner(dbContext, target.Id, DateTimeOffset.UtcNow, cancellationToken))
        {
            return Error(StatusCodes.Status409Conflict, "last_active_owner", "The last active Owner cannot be deleted.");
        }

        if (await AuthAccountState.HasScimProvenanceAsync(dbContext, target.Id, cancellationToken))
        {
            return Error(StatusCodes.Status409Conflict, "provenance_conflict",
                "This user has SCIM or federated identity history and cannot be deleted.");
        }

        IdentityResult deleteResult;
        try
        {
            deleteResult = await userManager.DeleteAsync(target);
        }
        catch (Exception exception) when (AuthAccountState.IsProvenanceDeletionConflict(exception))
        {
            return Error(StatusCodes.Status409Conflict, "provenance_conflict",
                "This user has SCIM or federated identity history and cannot be deleted.");
        }
        if (!deleteResult.Succeeded)
        {
            return IdentityFailure(deleteResult, "The user account could not be deleted.");
        }

        try
        {
            await auditWriter.WriteAsync(dbContext, httpContext, (await userManager.GetUserAsync(principal))?.Id,
                target.Id, null, "user.deleted", before, before with
                {
                    Roles = Array.Empty<string>(),
                    PermissionKeys = Array.Empty<string>(),
                    IsDeleted = true,
                }, cancellationToken);
            await transaction.CommitAsync(cancellationToken);
            return TypedResults.NoContent();
        }
        catch (Exception exception) when (AuthAccountState.IsExpectedConflict(exception))
        {
            return Error(StatusCodes.Status409Conflict, "account_conflict", "The user account conflicts with another account change.");
        }
    }

    private static async Task<(ApplicationUser? User, IResult? Error)> FindManagedTarget(
        Guid id,
        ClaimsPrincipal principal,
        UserManager<ApplicationUser> userManager)
    {
        var caller = await userManager.GetUserAsync(principal);
        if (caller is null)
        {
            return (null, Error(StatusCodes.Status401Unauthorized, "unauthenticated", "Authentication is required."));
        }

        var target = await userManager.FindByIdAsync(id.ToString());
        if (target is null)
        {
            return (null, TypedResults.NotFound());
        }

        return (target, RejectSelfManagement(caller, target));
    }

    private static IResult? RejectSelfManagement(ApplicationUser caller, ApplicationUser target) =>
        caller.Id == target.Id
            ? Error(StatusCodes.Status400BadRequest, "invalid_request", "Use the self-service account endpoints for your own account.")
            : null;

    private static bool IsManagedRole(string? role) =>
        string.Equals(role, AuthRoles.User, StringComparison.Ordinal) ||
        string.Equals(role, AuthRoles.Owner, StringComparison.Ordinal);

    private static async Task RevokeActiveInvitations(
        AccountsDbContext dbContext,
        string normalizedEmail,
        DateTimeOffset revokedAt,
        CancellationToken cancellationToken) =>
        await dbContext.Invitations
            .Where(item => item.NormalizedEmail == normalizedEmail &&
                item.AcceptedAt == null && item.RevokedAt == null)
            .ExecuteUpdateAsync(setters => setters.SetProperty(item => item.RevokedAt, revokedAt), cancellationToken);

    private static Dictionary<string, string[]> ValidateOwnerUserCreateRequest(OwnerUserCreateRequest? request)
    {
        var errors = ValidateOwnerUserDetails(request?.DisplayName, request?.Email, request?.Role);
        var password = string.IsNullOrWhiteSpace(request?.TemporaryPassword)
            ? request?.Password
            : request.TemporaryPassword;
        if (string.IsNullOrWhiteSpace(password))
        {
            errors["password"] = ["Password is required."];
        }
        else if (password.Length > 256)
        {
            errors["password"] = ["Password must be 256 characters or fewer."];
        }

        return errors;
    }

    private static Dictionary<string, string[]> ValidateOwnerUserUpdateRequest(OwnerUserUpdateRequest? request) =>
        ValidateOwnerUserDetails(request?.DisplayName, request?.Email, request?.Role);

    private static Dictionary<string, string[]> ValidateOwnerUserPasswordRequest(OwnerUserPasswordRequest? request)
    {
        var errors = new Dictionary<string, string[]>();
        if (string.IsNullOrWhiteSpace(request?.Password))
        {
            errors["password"] = ["Password is required."];
        }
        else if (request.Password.Length > 256)
        {
            errors["password"] = ["Password must be 256 characters or fewer."];
        }

        return errors;
    }

    private static Dictionary<string, string[]> ValidateOwnerUserDetails(string? displayName, string? email, string? role)
    {
        var errors = new Dictionary<string, string[]>();
        if (string.IsNullOrWhiteSpace(displayName) || displayName.Trim().Length > 200)
        {
            errors["displayName"] = ["Display name is required and must be at most 200 characters."];
        }

        if (string.IsNullOrWhiteSpace(email) || !IsValidEmail(email))
        {
            errors["email"] = ["A valid email is required."];
        }

        if (!IsManagedRole(role))
        {
            errors["role"] = ["Role must be User or Owner."];
        }

        return errors;
    }

    private static bool IsValidEmail(string email) =>
        email.Length <= 256 &&
        !email.Any(char.IsWhiteSpace) &&
        !email.Any(char.IsControl) &&
        email.Count(character => character == '@') == 1 &&
        email.IndexOf('@') > 0 &&
        email.IndexOf('@') < email.Length - 1;

    private static async Task SendPasswordResetEmail(
        ApplicationUser user,
        UserManager<ApplicationUser> userManager,
        IOptions<VantigoAuthenticationOptions> options,
        AppPublicUrls appPublicUrls,
        IApplicationEmailSender emailSender,
        CancellationToken cancellationToken)
    {
        var token = await userManager.GeneratePasswordResetTokenAsync(user);
        var encodedToken = WebEncoders.Base64UrlEncode(Encoding.UTF8.GetBytes(token));
        var url = InvitationTokenService.PasswordResetUrl(options.Value.PasswordReset, appPublicUrls, user.Email!, encodedToken);
        try
        {
            await emailSender.SendAsync(new ApplicationEmail(
                user.Email!,
                "Reset your Vantigo password",
                $"Use this link to reset your password: {url}"), cancellationToken);
        }
        catch (Exception) when (!cancellationToken.IsCancellationRequested)
        {
            // Preserve the generic response used by the public recovery flow.
        }
    }

    private static async Task<IResult> ManagedUserResponse(ApplicationUser user, UserManager<ApplicationUser> userManager)
    {
        var roles = await userManager.GetRolesAsync(user);
        var role = AuthRoleOrdering.ManagedRole(roles);
        return TypedResults.Ok(new OwnerUserResponse(user.Id, user.DisplayName, user.Email, role,
            !user.IsDisabled && !AuthAccountState.IsLockedOut(user.LockoutEnd, DateTimeOffset.UtcNow),
            user.IsDisabled, AuthAccountState.IsLockedOut(user.LockoutEnd, DateTimeOffset.UtcNow),
            user.LockoutEnd, user.TwoFactorEnabled));
    }

    private static async Task<IResult> CreateInvitation(
        InvitationRequest? request,
        ClaimsPrincipal principal,
        AccountsDbContext dbContext,
        UserManager<ApplicationUser> userManager,
        IOptions<VantigoAuthenticationOptions> options,
        AppPublicUrls appPublicUrls,
        IApplicationEmailSender emailSender,
        TenantMembershipService tenantMembershipService,
        CancellationToken cancellationToken)
    {
        var validation = ValidateInvitationRequest(request);
        if (validation.Count > 0)
        {
            return Error(StatusCodes.Status400BadRequest, "invalid_request", "The invitation request is invalid.", validation);
        }

        var email = request!.Email!.Trim();
        var normalizedEmail = NormalizeEmail(email);
        if (await userManager.FindByEmailAsync(email) is not null)
        {
            return Error(StatusCodes.Status409Conflict, "account_exists", "An account already exists for this email address.");
        }

        var invitedByUser = await userManager.GetUserAsync(principal);
        if (invitedByUser is null)
        {
            return Error(StatusCodes.Status401Unauthorized, "unauthenticated", "Authentication is required.");
        }

        var now = DateTimeOffset.UtcNow;
        var expiration = InvitationTokenService.GetLifetime(options.Value.Invitations);

        var (rawToken, tokenHash) = InvitationTokenService.Create();
        var invitation = new Invitation
        {
            Email = email,
            NormalizedEmail = normalizedEmail,
            Role = request.Role!,
            DisplayName = string.IsNullOrWhiteSpace(request.DisplayName) ? null : request.DisplayName.Trim(),
            TokenHash = tokenHash,
            CreatedAt = now,
            ExpiresAt = now.Add(expiration),
            InvitedByUserId = invitedByUser.Id,
            TenantId = (await tenantMembershipService.ResolveActiveTenantIdAsync(invitedByUser, principal, cancellationToken)),
        };

        await using (var transaction = await dbContext.Database.BeginTransactionAsync(
            System.Data.IsolationLevel.Serializable, cancellationToken))
        {
            await dbContext.Invitations
                .Where(item => item.NormalizedEmail == normalizedEmail && item.AcceptedAt == null && item.RevokedAt == null)
                .ExecuteUpdateAsync(setters => setters.SetProperty(item => item.RevokedAt, now), cancellationToken);
            dbContext.Invitations.Add(invitation);
            await dbContext.SaveChangesAsync(cancellationToken);
            await transaction.CommitAsync(cancellationToken);
        }

        try
        {
            await SendInvitation(emailSender, options, appPublicUrls, invitation, rawToken, cancellationToken);
        }
        catch
        {
            invitation.RevokedAt = DateTimeOffset.UtcNow;
            await dbContext.SaveChangesAsync(CancellationToken.None);
            throw;
        }

        return TypedResults.Created($"/api/v1/identity/owner/invitations/{invitation.Id}", ToResponse(invitation));
    }

    private static async Task<IResult> ListInvitations(
        ClaimsPrincipal principal,
        AccountsDbContext dbContext,
        UserManager<ApplicationUser> userManager,
        CancellationToken cancellationToken)
    {
        var user = await userManager.GetUserAsync(principal);
        if (user is null)
        {
            return Error(StatusCodes.Status401Unauthorized, "unauthenticated", "Authentication is required.");
        }

        var invitations = await dbContext.Invitations
            .AsNoTracking()
            .OrderByDescending(invitation => invitation.CreatedAt)
            .Select(invitation => new InvitationResponse(
                invitation.Id,
                invitation.Email,
                invitation.Role,
                invitation.DisplayName,
                invitation.CreatedAt,
                invitation.ExpiresAt,
                invitation.RevokedAt,
                invitation.AcceptedAt))
            .ToListAsync(cancellationToken);
        return TypedResults.Ok(invitations);
    }

    private static async Task<IResult> RevokeInvitation(
        Guid id,
        ClaimsPrincipal principal,
        AccountsDbContext dbContext,
        UserManager<ApplicationUser> userManager,
        CancellationToken cancellationToken)
    {
        if (await userManager.GetUserAsync(principal) is null)
        {
            return Error(StatusCodes.Status401Unauthorized, "unauthenticated", "Authentication is required.");
        }

        var invitation = await dbContext.Invitations.SingleOrDefaultAsync(item => item.Id == id, cancellationToken);
        if (invitation is null)
        {
            return TypedResults.NotFound();
        }

        if (invitation.AcceptedAt is null && invitation.RevokedAt is null)
        {
            invitation.RevokedAt = DateTimeOffset.UtcNow;
            await dbContext.SaveChangesAsync(cancellationToken);
        }

        return TypedResults.Ok(ToResponse(invitation));
    }

    private static async Task<IResult> ResendInvitation(
        Guid id,
        ClaimsPrincipal principal,
        AccountsDbContext dbContext,
        UserManager<ApplicationUser> userManager,
        IOptions<VantigoAuthenticationOptions> options,
        AppPublicUrls appPublicUrls,
        IApplicationEmailSender emailSender,
        CancellationToken cancellationToken)
    {
        if (await userManager.GetUserAsync(principal) is null)
        {
            return Error(StatusCodes.Status401Unauthorized, "unauthenticated", "Authentication is required.");
        }

        var invitation = await dbContext.Invitations.SingleOrDefaultAsync(item => item.Id == id, cancellationToken);
        if (invitation is null)
        {
            return TypedResults.NotFound();
        }

        if (invitation.AcceptedAt is not null)
        {
            return Error(StatusCodes.Status409Conflict, "invitation_not_active", "The invitation is no longer active.");
        }

        var now = DateTimeOffset.UtcNow;
        var (rawToken, tokenHash) = InvitationTokenService.Create();
        var replacement = new Invitation
        {
            Email = invitation.Email,
            NormalizedEmail = invitation.NormalizedEmail,
            Role = invitation.Role,
            DisplayName = invitation.DisplayName,
            TokenHash = tokenHash,
            CreatedAt = now,
            ExpiresAt = now.Add(InvitationTokenService.GetLifetime(options.Value.Invitations)),
            InvitedByUserId = invitation.InvitedByUserId,
        };
        await using (var transaction = await dbContext.Database.BeginTransactionAsync(
            System.Data.IsolationLevel.Serializable, cancellationToken))
        {
            await dbContext.Invitations
                .Where(item => item.NormalizedEmail == invitation.NormalizedEmail && item.AcceptedAt == null && item.RevokedAt == null)
                .ExecuteUpdateAsync(setters => setters.SetProperty(item => item.RevokedAt, now), cancellationToken);
            dbContext.Invitations.Add(replacement);
            await dbContext.SaveChangesAsync(cancellationToken);
            await transaction.CommitAsync(cancellationToken);
        }

        try
        {
            await SendInvitation(emailSender, options, appPublicUrls, replacement, rawToken, cancellationToken);
        }
        catch
        {
            replacement.RevokedAt = DateTimeOffset.UtcNow;
            await dbContext.SaveChangesAsync(CancellationToken.None);
            throw;
        }

        return TypedResults.Ok(ToResponse(replacement));
    }

    private static async Task<IResult> ValidateInvitation(
        string? token,
        AccountsDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var invitation = await FindActiveInvitation(dbContext, token, cancellationToken);
        return TypedResults.Ok(invitation is null
            ? new InvitationAcceptanceResponse(false)
            : new InvitationAcceptanceResponse(true, invitation.Email, invitation.Role, invitation.ExpiresAt));
    }

    private static async Task<IResult> AcceptInvitation(
        InvitationAcceptanceRequest? request,
        AccountsDbContext dbContext,
        UserManager<ApplicationUser> userManager,
        SignInManager<ApplicationUser> signInManager,
        AuthorizationAuditWriter auditWriter,
        TenantMembershipService tenantMembershipService,
        HttpContext httpContext,
        IOptions<VantigoAuthenticationOptions> options,
        CancellationToken cancellationToken)
    {
        if (string.IsNullOrWhiteSpace(request?.Token) || string.IsNullOrWhiteSpace(request.Password))
        {
            return Error(StatusCodes.Status400BadRequest, "invitation_invalid", "The invitation is invalid or no longer available.");
        }

        await using var transaction = await dbContext.Database.BeginTransactionAsync(
            System.Data.IsolationLevel.Serializable, cancellationToken);
        var tokenHash = InvitationTokenService.Hash(request.Token);
        Invitation? invitation;
        try
        {
            invitation = await dbContext.Invitations
                .FromSqlInterpolated($"SELECT * FROM identity.invitations WHERE token_hash = {tokenHash} FOR UPDATE")
                .SingleOrDefaultAsync(cancellationToken);
        }
        catch (Exception exception) when (HasPostgresState(exception, PostgresErrorCodes.SerializationFailure))
        {
            return Error(StatusCodes.Status400BadRequest, "invitation_invalid", "The invitation is invalid or no longer available.");
        }
        if (invitation is null || invitation.RevokedAt is not null || invitation.AcceptedAt is not null || invitation.ExpiresAt <= DateTimeOffset.UtcNow)
        {
            return Error(StatusCodes.Status400BadRequest, "invitation_invalid", "The invitation is invalid or no longer available.");
        }

        if (string.Equals(invitation.Role, AuthRoles.Owner, StringComparison.Ordinal))
        {
            await AuthAccountState.AcquireOwnerMutationLock(dbContext, cancellationToken);
        }

        var user = new ApplicationUser
        {
            UserName = invitation.Email,
            Email = invitation.Email,
            EmailConfirmed = true,
            DisplayName = string.IsNullOrWhiteSpace(request.DisplayName)
                ? invitation.DisplayName ?? invitation.Email
                : request.DisplayName.Trim(),
        };
        IdentityResult createResult;
        try
        {
            createResult = await userManager.CreateAsync(user, request.Password);
        }
        catch (DbUpdateException) when (dbContext.Database.CurrentTransaction is not null)
        {
            return Error(StatusCodes.Status400BadRequest, "invitation_invalid", "The invitation is invalid or no longer available.");
        }
        if (!createResult.Succeeded)
        {
            return IdentityFailure(createResult, "The invitation account could not be created.");
        }

        var roleResult = await userManager.AddToRoleAsync(user, invitation.Role);
        if (!roleResult.Succeeded)
        {
            return IdentityFailure(roleResult, "The invitation account could not be assigned its role.");
        }

        var tenantId = invitation.TenantId is Guid invitationTenantId
            ? new Vantigo.Tenancy.Abstractions.TenantId(invitationTenantId)
            : await tenantMembershipService.GetDefaultTenantIdAsync(cancellationToken);
        await tenantMembershipService.EnsureMembershipAsync(user.Id, tenantId, cancellationToken);

        invitation.AcceptedAt = DateTimeOffset.UtcNow;
        var after = await auditWriter.CaptureUserAsync(dbContext, user.Id, cancellationToken);
        await auditWriter.WriteAsync(dbContext, httpContext, null, user.Id, null,
            "invitation.accepted-with-role", new { InvitationId = invitation.Id, Roles = Array.Empty<string>() }, after, cancellationToken);
        await transaction.CommitAsync(cancellationToken);
        await tenantMembershipService.SignInWithActiveTenantAsync(signInManager, user, isPersistent: false,
            cancellationToken: cancellationToken);

        var roles = await userManager.GetRolesAsync(user);
        var orderedRoles = AuthRoleOrdering.Ordered(roles);
        return TypedResults.Created("/api/v1/identity/session", new AuthSuccessResponse(
            new AuthUserResponse(user.Id, user.DisplayName, user.Email!, orderedRoles),
            false,
            user.TwoFactorEnabled,
            orderedRoles.Contains(AuthRoles.Owner, StringComparer.Ordinal) &&
                options.Value.Owners.RequireMfa && !user.TwoFactorEnabled));
    }

    private static async Task<IResult> RequestPasswordRecovery(
        PasswordRecoveryRequest? request,
        UserManager<ApplicationUser> userManager,
        IOptions<VantigoAuthenticationOptions> options,
        AppPublicUrls appPublicUrls,
        IApplicationEmailSender emailSender,
        CancellationToken cancellationToken)
    {
        if (!string.IsNullOrWhiteSpace(request?.Email))
        {
            var email = request.Email.Trim();
            var user = await userManager.FindByEmailAsync(email);
            if (user is not null && user.PasswordHash is not null && user.EmailConfirmed)
            {
                var token = await userManager.GeneratePasswordResetTokenAsync(user);
                var encodedToken = WebEncoders.Base64UrlEncode(Encoding.UTF8.GetBytes(token));
                var url = InvitationTokenService.PasswordResetUrl(options.Value.PasswordReset, appPublicUrls, user.Email!, encodedToken);
                try
                {
                    await emailSender.SendAsync(new ApplicationEmail(
                        user.Email!,
                        "Reset your Vantigo password",
                        $"Use this link to reset your password: {url}"), cancellationToken);
                }
                catch (Exception) when (!cancellationToken.IsCancellationRequested)
                {
                    // Deliberately preserve the same generic response when delivery
                    // fails; the caller must not learn whether an account exists.
                }
            }
        }

        return TypedResults.Ok(new PasswordRecoveryResponse(true));
    }

    private static async Task<IResult> ResetPassword(
        PasswordResetRequest? request,
        UserManager<ApplicationUser> userManager,
        CancellationToken cancellationToken)
    {
        if (string.IsNullOrWhiteSpace(request?.Email) || string.IsNullOrWhiteSpace(request.Token))
        {
            return InvalidResetToken();
        }

        ApplicationUser? user = await userManager.FindByEmailAsync(request!.Email!.Trim());
        string token;
        try
        {
            token = Encoding.UTF8.GetString(WebEncoders.Base64UrlDecode(request.Token!));
        }
        catch (FormatException)
        {
            return InvalidResetToken();
        }

        if (user is null || !await userManager.VerifyUserTokenAsync(
                user,
                TokenOptions.DefaultProvider,
                UserManager<ApplicationUser>.ResetPasswordTokenPurpose,
                token))
        {
            return InvalidResetToken();
        }

        if (string.IsNullOrWhiteSpace(request.NewPassword))
        {
            return Error(StatusCodes.Status400BadRequest, "invalid_request", "The password reset request is invalid.",
                new Dictionary<string, string[]> { ["newPassword"] = ["A new password is required."] });
        }

        var result = await userManager.ResetPasswordAsync(user, token, request.NewPassword!);
        if (!result.Succeeded)
        {
            return IdentityFailure(result, "The password reset could not be completed.", "invalid_reset_token");
        }

        await userManager.UpdateSecurityStampAsync(user);
        return TypedResults.Ok(new PasswordResetResponse(true));
    }

    private static IResult InvalidResetToken() =>
        Error(StatusCodes.Status400BadRequest, "invalid_reset_token", "The password reset token is invalid or expired.");

    private static async Task SendInvitation(
        IApplicationEmailSender emailSender,
        IOptions<VantigoAuthenticationOptions> options,
        AppPublicUrls appPublicUrls,
        Invitation invitation,
        string rawToken,
        CancellationToken cancellationToken)
    {
        var url = InvitationTokenService.InvitationUrl(options.Value.Invitations, appPublicUrls, rawToken);
        await emailSender.SendAsync(new ApplicationEmail(
            invitation.Email,
            "You are invited to Vantigo",
            $"Use this link to create your Vantigo account: {url}"), cancellationToken);
    }

    private static async Task<Invitation?> FindActiveInvitation(
        AccountsDbContext dbContext,
        string? rawToken,
        CancellationToken cancellationToken)
    {
        var hash = InvitationTokenService.Hash(rawToken ?? string.Empty);
        return await dbContext.Invitations.AsNoTracking().SingleOrDefaultAsync(invitation =>
            invitation.TokenHash == hash && invitation.RevokedAt == null && invitation.AcceptedAt == null && invitation.ExpiresAt > DateTimeOffset.UtcNow,
            cancellationToken);
    }

    private static Dictionary<string, string[]> ValidateInvitationRequest(InvitationRequest? request)
    {
        var errors = new Dictionary<string, string[]>();
        if (string.IsNullOrWhiteSpace(request?.Email) || !request.Email.Contains('@', StringComparison.Ordinal))
            errors["email"] = ["A valid email is required."];
        if (!string.Equals(request?.Role, AuthRoles.User, StringComparison.Ordinal) && !string.Equals(request?.Role, AuthRoles.Owner, StringComparison.Ordinal))
            errors["role"] = ["Role must be User or Owner."];
        return errors;
    }

    private static string NormalizeEmail(string email) => email.Trim().ToUpperInvariant();

    private static InvitationResponse ToResponse(Invitation invitation) => new(
        invitation.Id, invitation.Email, invitation.Role, invitation.DisplayName, invitation.CreatedAt,
        invitation.ExpiresAt, invitation.RevokedAt, invitation.AcceptedAt);

    private static IResult IdentityFailure(IdentityResult result, string message, string? code = null) =>
        TypedResults.Json(new AuthErrorResponse(new AuthError(
            code ?? "identity_validation_failed",
            message,
            result.Errors.GroupBy(error => error.Code).ToDictionary(group => group.Key, group => group.Select(error => error.Description).ToArray()))),
            statusCode: StatusCodes.Status400BadRequest);

    private static IResult MfaResetFailure(IdentityResult result, string operation) =>
        TypedResults.Json(new AuthErrorResponse(new AuthError(
            "mfa_reset_failed",
            $"Owner MFA reset failed while {operation}.",
            result.Errors.GroupBy(error => error.Code)
                .ToDictionary(group => group.Key, group => group.Select(error => error.Description).ToArray()))),
            statusCode: StatusCodes.Status500InternalServerError);

    private static IResult Error(int statusCode, string code, string message, IReadOnlyDictionary<string, string[]>? fields = null) =>
        TypedResults.Json(new AuthErrorResponse(new AuthError(code, message, fields)), statusCode: statusCode);

    private static bool HasPostgresState(Exception exception, string sqlState)
    {
        for (var current = exception; current is not null; current = current.InnerException)
        {
            if (current is PostgresException postgres && postgres.SqlState == sqlState)
            {
                return true;
            }
        }

        return false;
    }
}