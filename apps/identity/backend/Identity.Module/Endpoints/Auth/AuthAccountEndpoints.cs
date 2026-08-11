using System.Security.Claims;
using System.Text;

using Microsoft.AspNetCore.Identity;
using Microsoft.AspNetCore.WebUtilities;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Options;

using Npgsql;

using Vantigo.Configuration;
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

        ownerManagement.MapPost("/invitations", CreateInvitation)

            .RequireRateLimiting(AuthRateLimitPolicies.Invitations);
        ownerManagement.MapGet("/invitations", ListInvitations)
            .RequireRateLimiting(AuthRateLimitPolicies.Invitations);
        ownerManagement.MapPost("/invitations/{id:guid}/revoke", RevokeInvitation)

            .RequireRateLimiting(AuthRateLimitPolicies.Invitations);
        ownerManagement.MapPost("/invitations/{id:guid}/resend", ResendInvitation)

            .RequireRateLimiting(AuthRateLimitPolicies.Invitations);

        owner.MapGet("/mfa", MfaStatus)
            .RequireRateLimiting(AuthRateLimitPolicies.Mfa);
        owner.MapGet("/mfa/setup", MfaSetup)
            .RequireRateLimiting(AuthRateLimitPolicies.Mfa);
        owner.MapPost("/mfa/setup", InitializeMfa)

            .RequireRateLimiting(AuthRateLimitPolicies.Mfa);
        owner.MapPost("/mfa/enable", EnableMfa)

            .RequireRateLimiting(AuthRateLimitPolicies.Mfa);
        owner.MapPost("/mfa/disable", DisableMfa)

            .RequireRateLimiting(AuthRateLimitPolicies.Mfa);
        owner.MapPost("/mfa/recovery-codes", RegenerateRecoveryCodes)

            .RequireRateLimiting(AuthRateLimitPolicies.Mfa);
        ownerManagement.MapPost("/mfa/reset/{userId:guid}", ResetOwnerMfa)

            .RequireRateLimiting(AuthRateLimitPolicies.Mfa);

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
        IOptions<VantigoAuthenticationOptions> options,
        SignInManager<ApplicationUser> signInManager)
    {
        var user = await userManager.GetUserAsync(principal);
        if (user is null)
        {
            return Error(StatusCodes.Status401Unauthorized, "unauthenticated", "Authentication is required.");
        }

        return TypedResults.Ok(new MfaStatusResponse(
            user.TwoFactorEnabled,
            options.Value.Owners.RequireMfa && !user.TwoFactorEnabled));
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

        var key = await userManager.GetAuthenticatorKeyAsync(user);
        if (string.IsNullOrWhiteSpace(key))
        {
            return TypedResults.Ok(new MfaSetupResponse(null, null, false));
        }

        var issuer = Uri.EscapeDataString(options.Value.Owners.MfaIssuer);
        var account = Uri.EscapeDataString(user.Email ?? user.UserName ?? user.Id.ToString());
        var uri = $"otpauth://totp/{issuer}:{account}?secret={key}&issuer={issuer}&digits=6";
        return TypedResults.Ok(new MfaSetupResponse(key, uri, true));
    }

    private static async Task<IResult> InitializeMfa(
        ClaimsPrincipal principal,
        UserManager<ApplicationUser> userManager,
        SignInManager<ApplicationUser> signInManager,
        IOptions<VantigoAuthenticationOptions> options)
    {
        var user = await userManager.GetUserAsync(principal);
        if (user is null)
        {
            return Error(StatusCodes.Status401Unauthorized, "unauthenticated", "Authentication is required.");
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
            principal.Claims.Where(claim => claim.Type is "amr" or ClaimTypes.AuthenticationMethod).ToArray());

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
        var existingClaims = await userManager.GetClaimsAsync(user);
        if (!existingClaims.Any(IsMfaClaim))
        {
            var claimResult = await userManager.AddClaimAsync(user, new Claim("amr", "mfa"));
            if (!claimResult.Succeeded)
            {
                return IdentityFailure(claimResult, "MFA could not be enabled.");
            }
        }

        await signInManager.SignInWithClaimsAsync(
            user,
            new Microsoft.AspNetCore.Authentication.AuthenticationProperties { IsPersistent = false },
            [new Claim("amr", "mfa"), new Claim(ClaimTypes.AuthenticationMethod, "mfa")]);
        return TypedResults.Ok(new MfaEnableResponse(true, codes?.ToArray() ?? []));
    }

    private static async Task<IResult> DisableMfa(
        MfaCodeRequest? request,
        ClaimsPrincipal principal,
        UserManager<ApplicationUser> userManager,
        IOptions<VantigoAuthenticationOptions> options,
        SignInManager<ApplicationUser> signInManager)
    {
        if (options.Value.Owners.RequireMfa)
        {
            return Error(StatusCodes.Status403Forbidden, "mfa_required", "Owner MFA cannot be disabled while MFA is required.");
        }

        var user = await userManager.GetUserAsync(principal);
        if (user is null)
        {
            return Error(StatusCodes.Status401Unauthorized, "unauthenticated", "Authentication is required.");
        }

        if (string.IsNullOrWhiteSpace(request?.Password) ||
            !await userManager.CheckPasswordAsync(user, request.Password))
        {
            return Error(StatusCodes.Status401Unauthorized, "reauthentication_required", "Current password is required.");
        }

        var result = await userManager.SetTwoFactorEnabledAsync(user, false);
        if (!result.Succeeded)
        {
            return IdentityFailure(result, "MFA could not be disabled.");
        }

        var existingClaims = (await userManager.GetClaimsAsync(user)).Where(IsMfaClaim).ToArray();
        if (existingClaims.Length > 0)
        {
            var removeClaimsResult = await userManager.RemoveClaimsAsync(user, existingClaims);
            if (!removeClaimsResult.Succeeded)
            {
                return IdentityFailure(removeClaimsResult, "MFA could not be disabled.");
            }
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
        if (caller is null || !await userManager.IsInRoleAsync(caller, AuthRoles.Owner) || !HasMfaClaim(principal))
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
        var targetClaims = (await userManager.GetClaimsAsync(target)).Where(IsMfaClaim).ToArray();
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

    private static bool HasMfaClaim(ClaimsPrincipal principal) => principal.Claims.Any(claim =>
        claim.Type == "amr" && string.Equals(claim.Value, "mfa", StringComparison.OrdinalIgnoreCase));

    private static bool IsMfaClaim(Claim claim) =>
        (claim.Type == "amr" || claim.Type == ClaimTypes.AuthenticationMethod) &&
        string.Equals(claim.Value, "mfa", StringComparison.OrdinalIgnoreCase);

    private static async Task<IResult> CreateInvitation(
        InvitationRequest? request,
        ClaimsPrincipal principal,
        AccountsDbContext dbContext,
        UserManager<ApplicationUser> userManager,
        IOptions<VantigoAuthenticationOptions> options,
        AppPublicUrls appPublicUrls,
        IApplicationEmailSender emailSender,
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

        invitation.AcceptedAt = DateTimeOffset.UtcNow;
        await dbContext.SaveChangesAsync(cancellationToken);
        await transaction.CommitAsync(cancellationToken);
        await signInManager.SignInAsync(user, isPersistent: false);

        var roles = await userManager.GetRolesAsync(user);
        return TypedResults.Created("/api/v1/identity/session", new AuthSuccessResponse(
            new AuthUserResponse(user.Id, user.DisplayName, user.Email!, roles.ToArray()),
            false,
            false,
            false));
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