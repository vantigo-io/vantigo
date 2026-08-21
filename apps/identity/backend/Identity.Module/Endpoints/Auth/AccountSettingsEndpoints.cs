using System.Buffers;
using System.Buffers.Binary;
using System.Data;
using System.Security.Claims;
using System.Text.Json;
using System.Text.Json.Nodes;

using Microsoft.AspNetCore.Http.Features;
using Microsoft.AspNetCore.Identity;
using Microsoft.AspNetCore.Mvc;
using Microsoft.AspNetCore.WebUtilities;
using Microsoft.EntityFrameworkCore;

using Vantigo.Identity.Authorization;
using Vantigo.Identity.Database.Accounts;
using Vantigo.Identity.Services;

namespace Vantigo.Identity.Endpoints.Auth;

/// <summary>
/// Self-service account settings. Every handler resolves the target exclusively
/// from the authenticated principal; there is intentionally no user-id route
/// parameter in this group.
/// </summary>
internal static class AccountSettingsEndpoints
{
    private const int MaxAvatarBytes = 5 * 1024 * 1024;
    private const int MaxAvatarRequestBytes = MaxAvatarBytes + 64 * 1024;
    private const int MaxPasskeys = 10;
    private const int MaxPasskeyNameLength = 100;
    private const int MaxCredentialIdChars = 1364;
    private const int MaxEnrollmentCeremoniesPerUser = 3;
    private const int MaxLoginCeremoniesPerAddress = 20;
    private static readonly TimeSpan CeremonyLifetime = TimeSpan.FromMinutes(5);
    private static readonly string[] AllowedAvatarContentTypes = ["image/png", "image/jpeg"];

    internal static IEndpointRouteBuilder MapAccountSettingsEndpoints(this IEndpointRouteBuilder app)
    {
        var account = app.MapGroup("/api/v1/identity/account")
            .WithTags("Personal account settings")
            .RequireAuthorization("ActiveAccount");

        account.MapGet("", GetAccount);
        account.MapPut("", UpdateProfile);
        account.MapPut("/profile", UpdateProfile);
        account.MapPatch("/profile", UpdateProfile);
        account.MapPost("/password", ChangePassword)
            .RequireRateLimiting(AuthRateLimitPolicies.PasswordRecovery);
        account.MapGet("/avatar", GetAvatar);
        account.MapPut("/avatar", UploadAvatar)
            .WithMetadata(new RequestSizeLimitAttribute(MaxAvatarRequestBytes),
                new RequestFormLimitsAttribute { MultipartBodyLengthLimit = MaxAvatarRequestBytes });
        account.MapPost("/avatar", UploadAvatar)
            .WithMetadata(new RequestSizeLimitAttribute(MaxAvatarRequestBytes),
                new RequestFormLimitsAttribute { MultipartBodyLengthLimit = MaxAvatarRequestBytes });
        account.MapDelete("/avatar", DeleteAvatar);

        account.MapGet("/passkeys", ListPasskeys);
        account.MapPost("/passkeys/begin", BeginPasskeyEnrollment)
            .RequireRateLimiting(AuthRateLimitPolicies.Mfa);
        account.MapPost("/passkeys/complete", CompletePasskeyEnrollment)
            .RequireRateLimiting(AuthRateLimitPolicies.Mfa);
        account.MapDelete("/passkeys/{credentialId}", RemovePasskey)
            .RequireRateLimiting(AuthRateLimitPolicies.Mfa);

        var publicPasskeys = app.MapGroup("/api/v1/identity/passkeys")
            .WithTags("Passkey sign-in");
        publicPasskeys.MapPost("/login/begin", BeginPasskeyLogin)
            .RequireRateLimiting(AuthRateLimitPolicies.PasskeyLogin);
        publicPasskeys.MapPost("/login/complete", CompletePasskeyLogin)
            .RequireRateLimiting(AuthRateLimitPolicies.PasskeyLogin);

        return app;
    }

    private static async Task<IResult> GetAccount(
        ClaimsPrincipal principal,
        UserManager<ApplicationUser> userManager,
        AccountsDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var user = await CurrentUser(principal, userManager);
        if (user is null)
        {
            return Error(StatusCodes.Status401Unauthorized, "unauthenticated", "Authentication is required.");
        }

        var hasAvatar = await dbContext.ProfileAvatars.AsNoTracking()
            .AnyAsync(avatar => avatar.UserId == user.Id, cancellationToken);
        return TypedResults.Ok(ToAccountResponse(user, hasAvatar));
    }

    private static async Task<IResult> UpdateProfile(
        AccountProfileRequest? request,
        ClaimsPrincipal principal,
        UserManager<ApplicationUser> userManager,
        SignInManager<ApplicationUser> signInManager,
        AccountsDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var errors = ValidateProfile(request);
        if (errors.Count > 0)
        {
            return Error(StatusCodes.Status400BadRequest, "invalid_request", "The profile request is invalid.", errors);
        }

        var user = await CurrentUser(principal, userManager);
        if (user is null)
        {
            return Error(StatusCodes.Status401Unauthorized, "unauthenticated", "Authentication is required.");
        }

        user.DisplayName = request!.DisplayName!.Trim();
        user.PreferredLanguage = NormalizeLanguage(request.PreferredLanguage);
        var update = await userManager.UpdateAsync(user);
        if (!update.Succeeded)
        {
            return IdentityFailure(update, "The profile could not be updated.");
        }

        var stamp = await userManager.UpdateSecurityStampAsync(user);
        if (!stamp.Succeeded)
        {
            return IdentityFailure(stamp, "The profile sessions could not be refreshed.");
        }

        await RefreshCookie(signInManager, user, principal);
        var hasAvatar = await dbContext.ProfileAvatars.AsNoTracking()
            .AnyAsync(avatar => avatar.UserId == user.Id, cancellationToken);
        return TypedResults.Ok(ToAccountResponse(user, hasAvatar));
    }

    private static async Task<IResult> ChangePassword(
        AccountPasswordRequest? request,
        ClaimsPrincipal principal,
        UserManager<ApplicationUser> userManager,
        SignInManager<ApplicationUser> signInManager)
    {
        var errors = ValidatePassword(request);
        if (errors.Count > 0)
        {
            return Error(StatusCodes.Status400BadRequest, "invalid_request", "The password request is invalid.", errors);
        }

        var user = await CurrentUser(principal, userManager);
        if (user is null)
        {
            return Error(StatusCodes.Status401Unauthorized, "unauthenticated", "Authentication is required.");
        }

        var reauthentication = await RequireLocalPassword(user, request!.CurrentPassword, userManager);
        if (reauthentication is not null)
        {
            return reauthentication;
        }

        var result = await userManager.ChangePasswordAsync(user, request.CurrentPassword!, request.NewPassword!);
        if (!result.Succeeded)
        {
            return IdentityFailure(result, "The password could not be changed.");
        }

        // ChangePasswordAsync updates the stamp. Explicitly refresh the current
        // cookie after that mutation so this browser remains signed in while all
        // other application cookies fail validation immediately.
        await RefreshCookie(signInManager, user, principal);
        return TypedResults.Ok(new PasswordResetResponse(true));
    }

    private static async Task<IResult> UploadAvatar(
        HttpContext httpContext,
        ClaimsPrincipal principal,
        UserManager<ApplicationUser> userManager,
        AccountsDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var user = await CurrentUser(principal, userManager);
        if (user is null)
        {
            return Error(StatusCodes.Status401Unauthorized, "unauthenticated", "Authentication is required.");
        }

        if (!httpContext.Request.HasFormContentType)
        {
            return Error(StatusCodes.Status400BadRequest, "invalid_avatar", "An avatar multipart upload is required.");
        }

        if (httpContext.Request.ContentLength is > MaxAvatarRequestBytes)
        {
            return Error(StatusCodes.Status400BadRequest, "invalid_avatar", "The avatar exceeds the 5 MB limit.");
        }

        var formOptions = new FormOptions
        {
            MultipartBodyLengthLimit = MaxAvatarRequestBytes,
            MemoryBufferThreshold = 64 * 1024,
        };
        var form = await httpContext.Request.ReadFormAsync(formOptions, cancellationToken);
        var file = form.Files.GetFile("avatar") ?? form.Files.SingleOrDefault();
        if (file is null || file.Length <= 0 || file.Length > MaxAvatarBytes)
        {
            return Error(StatusCodes.Status400BadRequest, "invalid_avatar", "The avatar is empty or exceeds the 5 MB limit.");
        }

        var bytes = await ReadCappedAsync(file, cancellationToken);
        if (bytes is null)
        {
            return Error(StatusCodes.Status400BadRequest, "invalid_avatar", "The avatar exceeds the 5 MB limit.");
        }

        var detectedType = DetectImageType(bytes);
        if (detectedType is null || !AllowedAvatarContentTypes.Contains(file.ContentType, StringComparer.OrdinalIgnoreCase) ||
            !string.Equals(file.ContentType, detectedType, StringComparison.OrdinalIgnoreCase))
        {
            return Error(StatusCodes.Status400BadRequest, "invalid_avatar", "The avatar must be a correctly detected PNG or JPEG image.");
        }

        var avatar = await dbContext.ProfileAvatars.SingleOrDefaultAsync(item => item.UserId == user.Id, cancellationToken);
        if (avatar is null)
        {
            dbContext.ProfileAvatars.Add(new ProfileAvatar
            {
                UserId = user.Id,
                Data = bytes,
                ContentType = detectedType,
                UpdatedAt = DateTimeOffset.UtcNow,
                Version = 1,
            });
        }
        else
        {
            avatar.Data = bytes;
            avatar.ContentType = detectedType;
            avatar.UpdatedAt = DateTimeOffset.UtcNow;
            avatar.Version++;
        }

        await dbContext.SaveChangesAsync(cancellationToken);
        var result = IdentityResult.Success;
        return result.Succeeded
            ? TypedResults.Ok(new AvatarResponse(true, "/api/v1/identity/account/avatar"))
            : IdentityFailure(result, "The avatar could not be updated.");
    }

    private static async Task<IResult> GetAvatar(
        ClaimsPrincipal principal,
        UserManager<ApplicationUser> userManager,
        AccountsDbContext dbContext,
        CancellationToken cancellationToken,
        HttpResponse response)
    {
        var user = await CurrentUser(principal, userManager);
        if (user is null)
        {
            return Error(StatusCodes.Status401Unauthorized, "unauthenticated", "Authentication is required.");
        }

        var avatar = await dbContext.ProfileAvatars.AsNoTracking()
            .Where(item => item.UserId == user.Id)
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

    private static async Task<IResult> DeleteAvatar(
        ClaimsPrincipal principal,
        UserManager<ApplicationUser> userManager,
        AccountsDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var user = await CurrentUser(principal, userManager);
        if (user is null)
        {
            return Error(StatusCodes.Status401Unauthorized, "unauthenticated", "Authentication is required.");
        }

        await dbContext.ProfileAvatars
            .Where(item => item.UserId == user.Id)
            .ExecuteDeleteAsync(cancellationToken);
        return TypedResults.NoContent();
    }

    private static async Task<IResult> ListPasskeys(
        ClaimsPrincipal principal,
        UserManager<ApplicationUser> userManager)
    {
        var user = await CurrentUser(principal, userManager);
        if (user is null)
        {
            return Error(StatusCodes.Status401Unauthorized, "unauthenticated", "Authentication is required.");
        }

        if (!userManager.SupportsUserPasskey)
        {
            return Error(StatusCodes.Status501NotImplemented, "passkeys_unavailable", "Passkeys are not available.");
        }

        var passkeys = await userManager.GetPasskeysAsync(user);
        return TypedResults.Ok(passkeys.Select(passkey => new PasskeyResponse(
            WebEncoders.Base64UrlEncode(passkey.CredentialId),
            passkey.Name ?? "Passkey",
            passkey.CreatedAt,
            passkey.Transports ?? [],
            passkey.IsUserVerified,
            passkey.IsBackupEligible,
            passkey.IsBackedUp)).ToArray());
    }

    private static async Task<IResult> BeginPasskeyEnrollment(
        PasskeyBeginRequest? request,
        ClaimsPrincipal principal,
        UserManager<ApplicationUser> userManager,
        IPasskeyHandler<ApplicationUser> passkeyHandler,
        AccountsDbContext dbContext,
        HttpContext httpContext,
        CancellationToken cancellationToken)
    {
        if (!ValidatePasskeyName(request?.Name, out var nameError))
        {
            return Error(StatusCodes.Status400BadRequest, "invalid_request", nameError!);
        }

        var user = await CurrentUser(principal, userManager);
        if (user is null)
        {
            return Error(StatusCodes.Status401Unauthorized, "unauthenticated", "Authentication is required.");
        }

        var reauthentication = await RequireLocalPassword(user, request!.CurrentPassword, userManager);
        if (reauthentication is not null)
        {
            return reauthentication;
        }

        if (!userManager.SupportsUserPasskey)
        {
            return Error(StatusCodes.Status501NotImplemented, "passkeys_unavailable", "Passkeys are not available.");
        }

        var existing = await userManager.GetPasskeysAsync(user);
        if (existing.Count >= MaxPasskeys)
        {
            return Error(StatusCodes.Status409Conflict, "passkey_limit_reached", "The maximum number of passkeys has been reached.");
        }

        await PurgeExpiredCeremonies(dbContext, cancellationToken);
        var enrollmentCeremonies = await dbContext.PasskeyCeremonies.CountAsync(item =>
            item.UserId == user.Id && item.Kind == "enrollment" && !item.Consumed &&
            item.ExpiresAt > DateTimeOffset.UtcNow, cancellationToken);
        if (enrollmentCeremonies >= MaxEnrollmentCeremoniesPerUser)
        {
            return Error(StatusCodes.Status429TooManyRequests, "rate_limited", "Too many passkey ceremonies are active.");
        }

        var options = await passkeyHandler.MakeCreationOptionsAsync(ToPasskeyUser(user), httpContext);
        if (string.IsNullOrWhiteSpace(options.AttestationState) || options.AttestationState.Length > 20_000)
        {
            return Error(StatusCodes.Status500InternalServerError, "passkey_configuration", "Passkey options could not be generated.");
        }
        var ceremony = new PasskeyCeremony
        {
            UserId = user.Id,
            Kind = "enrollment",
            State = options.AttestationState,
            CredentialName = request.Name!.Trim(),
            ExpiresAt = DateTimeOffset.UtcNow.Add(CeremonyLifetime),
        };
        dbContext.PasskeyCeremonies.Add(ceremony);
        await dbContext.SaveChangesAsync(cancellationToken);
        return PasskeyOptionsResult(ceremony.Id, options.CreationOptionsJson);
    }

    private static async Task<IResult> CompletePasskeyEnrollment(
        PasskeyCompleteRequest? request,
        ClaimsPrincipal principal,
        UserManager<ApplicationUser> userManager,
        IPasskeyHandler<ApplicationUser> passkeyHandler,
        AccountsDbContext dbContext,
        HttpContext httpContext,
        CancellationToken cancellationToken)
    {
        if (request is null || request.CeremonyId == Guid.Empty || string.IsNullOrWhiteSpace(request.CredentialJson) ||
            request.CredentialJson.Length > 100_000)
        {
            return Error(StatusCodes.Status400BadRequest, "invalid_request", "The passkey response is invalid.");
        }

        var user = await CurrentUser(principal, userManager);
        if (user is null)
        {
            return Error(StatusCodes.Status401Unauthorized, "unauthenticated", "Authentication is required.");
        }

        var reauthentication = await RequireLocalPassword(user, request.CurrentPassword, userManager);
        if (reauthentication is not null)
        {
            return reauthentication;
        }

        var ceremony = await ConsumeCeremony(dbContext, request.CeremonyId, user.Id, "enrollment", cancellationToken);
        if (ceremony is null)
        {
            return Error(StatusCodes.Status409Conflict, "passkey_ceremony_invalid", "The passkey ceremony is expired or already used.");
        }

        PasskeyAttestationResult result;
        try
        {
            result = await passkeyHandler.PerformAttestationAsync(new PasskeyAttestationContext
            {
                HttpContext = httpContext,
                CredentialJson = request.CredentialJson,
                AttestationState = ceremony.State,
            });
        }
        catch (PasskeyException)
        {
            return Error(StatusCodes.Status400BadRequest, "invalid_passkey", "The passkey response is invalid.");
        }

        if (!result.Succeeded || result.Passkey is null || !result.Passkey.IsUserVerified)
        {
            return Error(StatusCodes.Status400BadRequest, "invalid_passkey", "A user-verified passkey is required.");
        }

        if ((await userManager.GetPasskeysAsync(user)).Count >= MaxPasskeys)
        {
            return Error(StatusCodes.Status409Conflict, "passkey_limit_reached", "The maximum number of passkeys has been reached.");
        }

        result.Passkey.Name = ceremony.CredentialName!;
        var add = await userManager.AddOrUpdatePasskeyAsync(user, result.Passkey);
        if (!add.Succeeded)
        {
            return IdentityFailure(add, "The passkey could not be enrolled.");
        }

        return TypedResults.Ok(new PasskeyMutationResponse(true));
    }

    private static async Task<IResult> RemovePasskey(
        string credentialId,
        [Microsoft.AspNetCore.Mvc.FromBody] PasskeyRemoveRequest? request,
        ClaimsPrincipal principal,
        UserManager<ApplicationUser> userManager)
    {
        var user = await CurrentUser(principal, userManager);
        if (user is null)
        {
            return Error(StatusCodes.Status401Unauthorized, "unauthenticated", "Authentication is required.");
        }

        var reauthentication = await RequireLocalPassword(user, request?.CurrentPassword, userManager);
        if (reauthentication is not null)
        {
            return reauthentication;
        }

        if (!TryDecodeCredentialId(credentialId, out var bytes))
        {
            return Error(StatusCodes.Status400BadRequest, "invalid_request", "The passkey identifier is invalid.");
        }

        var passkey = await userManager.GetPasskeyAsync(user, bytes);
        if (passkey is null)
        {
            return TypedResults.NotFound();
        }

        var result = await userManager.RemovePasskeyAsync(user, bytes);
        return result.Succeeded
            ? TypedResults.NoContent()
            : IdentityFailure(result, "The passkey could not be removed.");
    }

    private static async Task<IResult> BeginPasskeyLogin(
        PasskeyLoginBeginRequest? request,
        UserManager<ApplicationUser> userManager,
        IPasskeyHandler<ApplicationUser> passkeyHandler,
        AccountsDbContext dbContext,
        HttpContext httpContext,
        CancellationToken cancellationToken)
    {
        if (string.IsNullOrWhiteSpace(request?.Email) || request.Email.Length > 256)
        {
            return Error(StatusCodes.Status400BadRequest, "invalid_request", "Email is required.");
        }

        await PurgeExpiredCeremonies(dbContext, cancellationToken);
        var clientAddress = httpContext.Connection.RemoteIpAddress?.ToString() ?? "unknown";
        var activeLoginCeremonies = await dbContext.PasskeyCeremonies.CountAsync(item =>
            item.Kind == "login" && item.ClientAddress == clientAddress && !item.Consumed &&
            item.ExpiresAt > DateTimeOffset.UtcNow, cancellationToken);
        if (activeLoginCeremonies >= MaxLoginCeremoniesPerAddress)
        {
            return Error(StatusCodes.Status429TooManyRequests, "rate_limited", "Too many passkey ceremonies are active.");
        }

        var candidate = await userManager.FindByEmailAsync(request.Email.Trim());
        var canUseCandidate = candidate is not null && !candidate.IsDisabled &&
            !AuthAccountState.IsLockedOut(candidate.LockoutEnd, DateTimeOffset.UtcNow) &&
            userManager.SupportsUserPasskey && (await userManager.GetPasskeysAsync(candidate)).Count > 0;
        var ceremonyUser = canUseCandidate
            ? candidate!
            : new ApplicationUser
            {
                UserName = request.Email.Trim(),
                Email = request.Email.Trim(),
                DisplayName = "Account",
            };

        // The same native Identity request-options shape is returned for
        // unknown, disabled, locked, and no-passkey accounts. A placeholder
        // user has no stored credentials, so its ceremony cannot complete.
        var options = await passkeyHandler.MakeRequestOptionsAsync(ceremonyUser, httpContext);
        if (string.IsNullOrWhiteSpace(options.AssertionState) || options.AssertionState.Length > 20_000)
        {
            return Error(StatusCodes.Status500InternalServerError, "passkey_configuration", "Passkey options could not be generated.");
        }
        var ceremony = new PasskeyCeremony
        {
            UserId = canUseCandidate ? candidate!.Id : null,
            Kind = "login",
            State = options.AssertionState,
            ClientAddress = clientAddress,
            ExpiresAt = DateTimeOffset.UtcNow.Add(CeremonyLifetime),
        };
        dbContext.PasskeyCeremonies.Add(ceremony);
        await dbContext.SaveChangesAsync(cancellationToken);
        var publicOptions = NormalizeLoginOptions(options.RequestOptionsJson);
        return publicOptions is null
            ? Error(StatusCodes.Status500InternalServerError, "passkey_configuration", "Passkey options could not be generated.")
            : PasskeyOptionsResult(ceremony.Id, publicOptions);
    }

    private static async Task<IResult> CompletePasskeyLogin(
        PasskeyCompleteRequest? request,
        UserManager<ApplicationUser> userManager,
        IPasskeyHandler<ApplicationUser> passkeyHandler,
        AccountsDbContext dbContext,
        SignInManager<ApplicationUser> signInManager,
        HttpContext httpContext,
        CancellationToken cancellationToken)
    {
        if (request is null || request.CeremonyId == Guid.Empty || string.IsNullOrWhiteSpace(request.CredentialJson) ||
            request.CredentialJson.Length > 100_000)
        {
            return Error(StatusCodes.Status400BadRequest, "invalid_request", "The passkey response is invalid.");
        }

        var ceremony = await ConsumeCeremony(dbContext, request.CeremonyId, null, "login", cancellationToken);
        if (ceremony is null)
        {
            return Error(StatusCodes.Status409Conflict, "passkey_ceremony_invalid", "The passkey ceremony is expired or already used.");
        }

        var user = ceremony.UserId is Guid userId
            ? await userManager.FindByIdAsync(userId.ToString())
            : null;
        if (user is null || user.IsDisabled || AuthAccountState.IsLockedOut(user.LockoutEnd, DateTimeOffset.UtcNow))
        {
            return Error(StatusCodes.Status401Unauthorized, "invalid_credentials", "The passkey sign-in failed.");
        }

        PasskeyAssertionResult<ApplicationUser> result;
        try
        {
            result = await passkeyHandler.PerformAssertionAsync(new PasskeyAssertionContext
            {
                HttpContext = httpContext,
                CredentialJson = request.CredentialJson,
                AssertionState = ceremony.State,
            });
        }
        catch (PasskeyException)
        {
            return Error(StatusCodes.Status401Unauthorized, "invalid_credentials", "The passkey sign-in failed.");
        }

        if (!result.Succeeded || result.User?.Id != user.Id || result.Passkey is null || !result.Passkey.IsUserVerified)
        {
            return Error(StatusCodes.Status401Unauthorized, "invalid_credentials", "The passkey sign-in failed.");
        }

        var update = await userManager.AddOrUpdatePasskeyAsync(user, result.Passkey);
        if (!update.Succeeded)
        {
            return IdentityFailure(update, "The passkey sign-in could not be completed.");
        }

        await signInManager.SignOutAsync();
        // A passkey assertion is a credential sign-in: it starts a new absolute
        // session lifetime instead of inheriting one already on this browser.
        SessionValidationService.BeginFreshSession(httpContext);
        await signInManager.SignInWithClaimsAsync(user,
            new Microsoft.AspNetCore.Authentication.AuthenticationProperties { IsPersistent = false },
            MfaClaims.Issue());
        var roles = await userManager.GetRolesAsync(user);
        return TypedResults.Ok(new AuthSuccessResponse(
            new AuthUserResponse(user.Id, user.DisplayName, user.Email, AuthRoleOrdering.Ordered(roles)),
            false, user.TwoFactorEnabled, false));
    }

    private static async Task<PasskeyCeremony?> ConsumeCeremony(
        AccountsDbContext dbContext,
        Guid id,
        Guid? userId,
        string kind,
        CancellationToken cancellationToken)
    {
        var now = DateTimeOffset.UtcNow;
        var query = dbContext.PasskeyCeremonies.Where(ceremony => ceremony.Id == id &&
            ceremony.Kind == kind && !ceremony.Consumed && ceremony.ExpiresAt > now);
        if (userId is not null)
        {
            query = query.Where(ceremony => ceremony.UserId == userId.Value);
        }

        var ceremony = await query.SingleOrDefaultAsync(cancellationToken);
        if (ceremony is null)
        {
            return null;
        }

        await using var transaction = await dbContext.Database.BeginTransactionAsync(
            IsolationLevel.ReadCommitted, cancellationToken);
        var consumed = await dbContext.PasskeyCeremonies
            .Where(item => item.Id == ceremony.Id && !item.Consumed && item.ExpiresAt > now)
            .ExecuteUpdateAsync(setters => setters.SetProperty(item => item.Consumed, true), cancellationToken);
        if (consumed != 1)
        {
            return null;
        }

        var removed = await dbContext.PasskeyCeremonies
            .Where(item => item.Id == ceremony.Id && item.Consumed)
            .ExecuteDeleteAsync(cancellationToken);
        if (removed != 1)
        {
            return null;
        }

        await transaction.CommitAsync(cancellationToken);

        ceremony.Consumed = true;
        return ceremony;
    }

    private static Task<int> PurgeExpiredCeremonies(
        AccountsDbContext dbContext,
        CancellationToken cancellationToken) =>
        dbContext.PasskeyCeremonies
            .Where(item => item.ExpiresAt <= DateTimeOffset.UtcNow || item.Consumed)
            .ExecuteDeleteAsync(cancellationToken);

    private static async Task<ApplicationUser?> CurrentUser(
        ClaimsPrincipal principal,
        UserManager<ApplicationUser> userManager) =>
        await userManager.GetUserAsync(principal);

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

        if (string.IsNullOrEmpty(password) || !await userManager.CheckPasswordAsync(user, password))
        {
            return Error(StatusCodes.Status400BadRequest, "reauthentication_required",
                "The current password is invalid.");
        }

        return null;
    }

    private static async Task RefreshCookie(
        SignInManager<ApplicationUser> signInManager,
        ApplicationUser user,
        ClaimsPrincipal principal)
    {
        var claims = principal.Claims.Where(MfaClaims.Is).ToArray();
        await signInManager.SignInWithClaimsAsync(user,
            new Microsoft.AspNetCore.Authentication.AuthenticationProperties { IsPersistent = false }, claims);
    }

    private static AccountResponse ToAccountResponse(ApplicationUser user, bool hasAvatar) =>
        new(user.Id, user.DisplayName, user.Email, user.PreferredLanguage,
            hasAvatar ? "/api/v1/identity/account/avatar" : null);

    private static PasskeyUserEntity ToPasskeyUser(ApplicationUser user) => new()
    {
        Id = user.Id.ToString(),
        Name = user.UserName ?? user.Id.ToString(),
        DisplayName = user.DisplayName,
    };

    private static IResult PasskeyOptionsResult(Guid ceremonyId, string optionsJson)
    {
        try
        {
            using var document = JsonDocument.Parse(optionsJson);
            return TypedResults.Ok(new PasskeyOptionsResponse(ceremonyId, document.RootElement.Clone()));
        }
        catch (JsonException)
        {
            return Error(StatusCodes.Status500InternalServerError, "passkey_configuration", "Passkey options could not be generated.");
        }
    }

    private static string? NormalizeLoginOptions(string optionsJson)
    {
        try
        {
            if (JsonNode.Parse(optionsJson) is not JsonObject options)
            {
                return null;
            }

            // Use discoverable/usernameless assertion semantics. The native
            // handler still binds the server ceremony to a candidate user when
            // one exists, but the public response never reveals credential
            // descriptors (or their presence).
            options.Remove("allowCredentials");
            return options.ToJsonString();
        }
        catch (JsonException)
        {
            return null;
        }
    }

    private static bool ValidatePasskeyName(string? name, out string? error)
    {
        if (string.IsNullOrWhiteSpace(name) || name.Trim().Length > MaxPasskeyNameLength || name.Any(char.IsControl))
        {
            error = "A passkey name is required and must be at most 100 characters.";
            return false;
        }

        error = null;
        return true;
    }

    private static bool TryDecodeCredentialId(string value, out byte[] bytes)
    {
        bytes = [];
        if (string.IsNullOrWhiteSpace(value) || value.Length > MaxCredentialIdChars)
        {
            return false;
        }

        try
        {
            bytes = WebEncoders.Base64UrlDecode(value);
            return bytes.Length is > 0 and <= 1023;
        }
        catch (FormatException)
        {
            return false;
        }
    }

    private static string? DetectImageType(byte[] bytes)
    {
        if (IsPng(bytes)) return "image/png";
        if (IsJpeg(bytes)) return "image/jpeg";
        return null;
    }

    private static async Task<byte[]?> ReadCappedAsync(IFormFile file, CancellationToken cancellationToken)
    {
        await using var stream = file.OpenReadStream();
        await using var buffer = new MemoryStream(Math.Min((int)file.Length, MaxAvatarBytes));
        var rented = ArrayPool<byte>.Shared.Rent(64 * 1024);
        try
        {
            var total = 0;
            while (true)
            {
                var read = await stream.ReadAsync(rented.AsMemory(0, rented.Length), cancellationToken);
                if (read == 0)
                {
                    return buffer.ToArray();
                }

                total += read;
                if (total > MaxAvatarBytes)
                {
                    return null;
                }

                await buffer.WriteAsync(rented.AsMemory(0, read), cancellationToken);
            }
        }
        finally
        {
            ArrayPool<byte>.Shared.Return(rented);
        }
    }

    private static bool IsPng(byte[] bytes)
    {
        ReadOnlySpan<byte> signature = [137, 80, 78, 71, 13, 10, 26, 10];
        if (!bytes.AsSpan().StartsWith(signature) || bytes.Length < 33) return false;
        var offset = 8;
        var sawHeader = false;
        var sawEnd = false;
        while (offset + 12 <= bytes.Length)
        {
            var length = BinaryPrimitives.ReadUInt32BigEndian(bytes.AsSpan(offset, 4));
            if (length > int.MaxValue || offset + 12L + length > bytes.Length) return false;
            var type = bytes.AsSpan(offset + 4, 4);
            var data = bytes.AsSpan(offset + 8, (int)length);
            var crc = BinaryPrimitives.ReadUInt32BigEndian(bytes.AsSpan(offset + 8 + (int)length, 4));
            if (PngCrc(type, data) != crc) return false;
            if (!sawHeader)
            {
                if (!type.SequenceEqual("IHDR"u8) || length != 13) return false;
                var width = BinaryPrimitives.ReadUInt32BigEndian(data[..4]);
                var height = BinaryPrimitives.ReadUInt32BigEndian(data.Slice(4, 4));
                if (width == 0 || height == 0 || width > 4096 || height > 4096) return false;
                sawHeader = true;
            }
            if (type.SequenceEqual("IEND"u8))
            {
                sawEnd = length == 0 && offset + 12 + length == bytes.Length;
                break;
            }
            offset += 12 + (int)length;
        }
        return sawHeader && sawEnd;
    }

    private static uint PngCrc(ReadOnlySpan<byte> type, ReadOnlySpan<byte> data)
    {
        uint crc = 0xffffffff;
        foreach (var value in type)
        {
            crc ^= value;
            for (var bit = 0; bit < 8; bit++) crc = (crc & 1) != 0 ? (crc >> 1) ^ 0xedb88320 : crc >> 1;
        }
        foreach (var value in data)
        {
            crc ^= value;
            for (var bit = 0; bit < 8; bit++) crc = (crc & 1) != 0 ? (crc >> 1) ^ 0xedb88320 : crc >> 1;
        }
        return ~crc;
    }

    private static bool IsJpeg(byte[] bytes)
    {
        if (bytes.Length < 4 || bytes[0] != 0xff || bytes[1] != 0xd8) return false;
        var index = 2;
        var sawFrame = false;
        while (index + 3 < bytes.Length)
        {
            if (bytes[index++] != 0xff) return false;
            while (index < bytes.Length && bytes[index] == 0xff) index++;
            if (index >= bytes.Length) return false;
            var marker = bytes[index++];
            if (marker == 0xd9) return sawFrame && index == bytes.Length;
            if (marker == 0xda)
            {
                // Scan data until the next marker and require a complete EOI.
                for (; index + 1 < bytes.Length; index++)
                    if (bytes[index] == 0xff && bytes[index + 1] == 0xd9)
                        return sawFrame && index + 2 == bytes.Length;
                return false;
            }
            if (marker is 0xd8 or >= 0xd0 and <= 0xd7) continue;
            if (index + 2 > bytes.Length) return false;
            var length = BinaryPrimitives.ReadUInt16BigEndian(bytes.AsSpan(index, 2));
            if (length < 2 || index + length > bytes.Length) return false;
            if (marker is >= 0xc0 and <= 0xc3 or >= 0xc5 and <= 0xc7 or >= 0xc9 and <= 0xcb or >= 0xcd and <= 0xcf)
            {
                if (length < 8) return false;
                var height = BinaryPrimitives.ReadUInt16BigEndian(bytes.AsSpan(index + 3, 2));
                var width = BinaryPrimitives.ReadUInt16BigEndian(bytes.AsSpan(index + 5, 2));
                if (width == 0 || height == 0 || width > 4096 || height > 4096) return false;
                sawFrame = true;
            }
            index += length;
        }
        return false;
    }

    private static string? NormalizeLanguage(string? value) =>
        string.IsNullOrWhiteSpace(value) || string.Equals(value.Trim(), "automatic", StringComparison.OrdinalIgnoreCase)
            ? null
            : value.Trim().ToLowerInvariant();

    private static Dictionary<string, string[]> ValidateProfile(AccountProfileRequest? request)
    {
        var errors = new Dictionary<string, string[]>();
        if (string.IsNullOrWhiteSpace(request?.DisplayName) || request.DisplayName.Trim().Length > 200)
            errors["displayName"] = ["Display name is required and must be at most 200 characters."];
        if (request?.PreferredLanguage is not null &&
            !string.IsNullOrWhiteSpace(request.PreferredLanguage) &&
            !string.Equals(request.PreferredLanguage.Trim(), "en", StringComparison.OrdinalIgnoreCase) &&
            !string.Equals(request.PreferredLanguage.Trim(), "nb", StringComparison.OrdinalIgnoreCase) &&
            !string.Equals(request.PreferredLanguage.Trim(), "automatic", StringComparison.OrdinalIgnoreCase))
            errors["preferredLanguage"] = ["Preferred language must be Automatic, en, or nb."];
        return errors;
    }

    private static Dictionary<string, string[]> ValidatePassword(AccountPasswordRequest? request)
    {
        var errors = new Dictionary<string, string[]>();
        if (string.IsNullOrEmpty(request?.CurrentPassword)) errors["currentPassword"] = ["Current password is required."];
        if (string.IsNullOrEmpty(request?.NewPassword)) errors["newPassword"] = ["New password is required."];
        else if (request.NewPassword.Length > 256) errors["newPassword"] = ["New password must be 256 characters or fewer."];
        return errors;
    }

    private static IResult IdentityFailure(IdentityResult result, string message) =>
        Error(StatusCodes.Status400BadRequest, "identity_validation_failed", message,
            result.Errors.GroupBy(error => error.Code, StringComparer.Ordinal)
                .ToDictionary(group => group.Key, group => group.Select(error => error.Description).ToArray(), StringComparer.Ordinal));

    private static IResult Error(int statusCode, string code, string message, IReadOnlyDictionary<string, string[]>? fields = null) =>
        TypedResults.Json(new AuthErrorResponse(new AuthError(code, message, fields)), statusCode: statusCode);
}

internal sealed record AccountProfileRequest(string? DisplayName, string? PreferredLanguage);
internal sealed record AccountPasswordRequest(string? CurrentPassword, string? NewPassword);
internal sealed record PasskeyBeginRequest(string? Name, string? CurrentPassword);
internal sealed record PasskeyCompleteRequest(Guid CeremonyId, string? CredentialJson, string? CurrentPassword = null);
internal sealed record PasskeyRemoveRequest(string? CurrentPassword);
internal sealed record PasskeyLoginBeginRequest(string? Email);
internal sealed record AccountResponse(Guid Id, string DisplayName, string? Email, string? PreferredLanguage, string? AvatarUrl);
internal sealed record AvatarResponse(bool Uploaded, string Url);
internal sealed record PasskeyResponse(string CredentialId, string Name, DateTimeOffset CreatedAt, string[] Transports, bool IsUserVerified, bool IsBackupEligible, bool IsBackedUp);
internal sealed record PasskeyOptionsResponse(Guid CeremonyId, JsonElement Options);
internal sealed record PasskeyMutationResponse(bool Success);