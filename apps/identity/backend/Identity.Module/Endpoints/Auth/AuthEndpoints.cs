using System.Security.Claims;
using System.Security.Cryptography;
using System.Text;

using Microsoft.AspNetCore.Antiforgery;
using Microsoft.AspNetCore.Authentication;
using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Infrastructure;
using Microsoft.Extensions.Options;

using Npgsql;

using Vantigo.Configuration;
using Vantigo.Contracts.Authorization;
using Vantigo.Identity.Authorization;
using Vantigo.Identity.Database.Accounts;
using Vantigo.Identity.Services;

namespace Vantigo.Identity.Endpoints.Auth;

public static class AuthEndpoints
{
    public static IEndpointRouteBuilder MapVantigoIdentityEndpoints(this IEndpointRouteBuilder app)
    {
        var group = app.MapGroup("/api/v1/identity").WithTags("Authentication");
        group.MapGet("/antiforgery", AntiforgeryToken)
            .WithSummary("Get the CSRF token for same-origin state-changing requests");

        group.MapGet("/bootstrap-status", BootstrapStatus)
            .WithSummary("Get whether one-time local Owner bootstrap is available");

        group.MapGet("/owner/system-status", SystemStatus)
            .WithSummary("Get non-sensitive static identity and account status")
            .RequireAuthorization(AuthPolicies.Owner);

        group.MapGet("/providers", WorkforceOidcEndpoints.Providers)
            .WithSummary("Get the configured workforce sign-in providers");

        group.MapGet("/oidc/challenge", WorkforceOidcEndpoints.Challenge)
            .WithSummary("Start workforce OpenID Connect sign-in");

        group.MapGet("/oidc/complete", WorkforceOidcEndpoints.Complete)
            .WithSummary("Complete workforce OpenID Connect sign-in");

        group.MapPost("/bootstrap", Bootstrap)
            .WithSummary("Create the single local Owner account")

            .RequireRateLimiting(AuthRateLimitPolicies.Bootstrap);

        group.MapPost("/login", Login)
            .WithSummary("Sign in with the application cookie")

            .RequireRateLimiting(AuthRateLimitPolicies.Login);

        group.MapPost("/login/2fa", CompleteTwoFactorLogin)
            .WithSummary("Complete an Owner authenticator or recovery-code login")

            .RequireRateLimiting(AuthRateLimitPolicies.Mfa);

        group.MapPost("/logout", Logout)
            .WithSummary("Sign out of the application cookie")
            .RequireAuthorization()
            ;

        group.MapGet("/session", Session)
            .WithSummary("Get the authenticated browser session")
            .RequireAuthorization();

        group.MapPost("/session/tenant", SwitchTenant)
            .WithSummary("Switch the authenticated session's active tenant")
            .RequireAuthorization();

        app.MapAccountAuthEndpoints();
        app.MapAccountSettingsEndpoints();
        AuthorizationManagementEndpoints.MapAuthorizationManagementEndpoints(app);
        TenantCapabilitiesEndpoints.MapTenantCapabilitiesEndpoints(app);
        SystemMaintenanceEndpoints.Map(app);
        SessionEndpoints.Map(app);
        ScimProtocolEndpoints.Map(app);

        return app;
    }

    private static IResult AntiforgeryToken(HttpContext httpContext, IAntiforgery antiforgery)
    {
        var tokens = antiforgery.GetAndStoreTokens(httpContext);
        return TypedResults.Ok(new AntiforgeryResponse(tokens.RequestToken!));
    }

    private static async Task<IResult> BootstrapStatus(
        BootstrapSecretProvider bootstrapSecret,
        AccountsDbContext dbContext,
        RoleManager<IdentityRole<Guid>> roleManager,
        IPermissionCatalog permissionCatalog,
        CancellationToken cancellationToken)
    {
        // This endpoint is an anonymous UI hint only. The POST endpoint remains
        // authoritative and performs the serializable, one-time bootstrap race
        // protection before creating the Owner.
        var available = !string.IsNullOrEmpty(bootstrapSecret.Secret) &&
            !await IsBootstrapConsumed(dbContext, roleManager, cancellationToken);
        return TypedResults.Ok(new BootstrapStatusResponse(available));
    }

    private static async Task<IResult> SystemStatus(
        AccountsDbContext dbContext,
        WorkforceOidcOptions oidc,
        StaticScimOptions scim,
        OperationalEventService operationalEvents,
        CancellationToken cancellationToken)
    {
        // Status counts are aggregate-only and contain no account or invitation
        // projections. Total is every persisted local account; disabled is the
        // explicit persistent IsDisabled flag; active is the currently available
        // account count, excluding explicit disablement, current lockout, and
        // static-SCIM upstream inactivity. A static-SCIM inactive Owner remains
        // available as the break-glass account, matching ScimLifecycleService.
        var now = DateTimeOffset.UtcNow;
        var total = await dbContext.Users.CountAsync(cancellationToken);
        var disabled = await dbContext.Users.CountAsync(user => user.IsDisabled, cancellationToken);
        var ownerRoleId = await dbContext.Roles.AsNoTracking()
            .Where(role => role.Name == AuthRoles.Owner)
            .Select(role => (Guid?)role.Id)
            .SingleOrDefaultAsync(cancellationToken);
        var activeQuery = dbContext.Users.AsNoTracking()
            .Where(user => !user.IsDisabled &&
                (!user.LockoutEnd.HasValue || user.LockoutEnd <= now));
        if (scim.Enabled)
        {
            activeQuery = activeQuery.Where(user => !dbContext.ScimUserMappings.Any(mapping =>
                mapping.UserId == user.Id &&
                mapping.ScimConnectionId == ScimConnection.StaticId &&
                !mapping.UpstreamActive &&
                (!ownerRoleId.HasValue || !dbContext.UserRoles.Any(assignment =>
                    assignment.UserId == user.Id && assignment.RoleId == ownerRoleId.Value))));
        }

        var active = await activeQuery.CountAsync(cancellationToken);
        var pendingInvitations = await dbContext.Invitations.AsNoTracking().CountAsync(invitation =>
            invitation.AcceptedAt == null && invitation.RevokedAt == null && invitation.ExpiresAt > now,
            cancellationToken);
        return TypedResults.Ok(new IdentitySystemStatusResponse(
            total,
            active,
            disabled,
            pendingInvitations,
            oidc.Enabled,
            oidc.Enabled ? oidc.Provider : null,
            scim.Enabled,
            await operationalEvents.LastAsync(OperationalEventKinds.StaticOidcSignInSucceeded, cancellationToken),
            await operationalEvents.LastAsync(OperationalEventKinds.AuthenticatedScimRequest, cancellationToken)));
    }

    private static async Task<IResult> Bootstrap(
        BootstrapRequest? request,
        BootstrapSecretProvider bootstrapSecret,
        AccountsDbContext dbContext,
        UserManager<ApplicationUser> userManager,
        RoleManager<IdentityRole<Guid>> roleManager,
        IPermissionCatalog permissionCatalog,
        SignInManager<ApplicationUser> signInManager,
        AuthorizationAuditWriter auditWriter,
        TenantMembershipService tenantMembershipService,
        HttpContext httpContext,
        IOptions<VantigoAuthenticationOptions> authenticationOptions,
        CancellationToken cancellationToken)
    {
        var errors = ValidateBootstrapRequest(request);
        if (errors.Count > 0)
        {
            return Error(StatusCodes.Status400BadRequest, "invalid_request", "The bootstrap request is invalid.", errors);
        }

        if (!SecretMatches(bootstrapSecret.Secret, request!.Secret!))
        {
            return Error(StatusCodes.Status401Unauthorized, "invalid_secret", "The bootstrap secret is invalid.");
        }

        try
        {
            await using var transaction = await dbContext.Database.BeginTransactionAsync(
                System.Data.IsolationLevel.Serializable, cancellationToken);
            await AuthAccountState.AcquireOwnerMutationLock(dbContext, cancellationToken);

            if (await IsBootstrapConsumed(dbContext, roleManager, cancellationToken))
            {
                return Error(StatusCodes.Status409Conflict, "bootstrap_unavailable", "The local Owner has already been created.");
            }

            await dbContext.EnsureBuiltInRolesAsync(roleManager, permissionCatalog, cancellationToken);
            var ownerRole = await roleManager.FindByNameAsync(AuthRoles.Owner);
            if (ownerRole is null)
                return Error(StatusCodes.Status500InternalServerError, "identity_configuration", "Protected roles could not be initialized.");

            var user = new ApplicationUser
            {
                UserName = request.Email!.Trim(),
                Email = request.Email.Trim(),
                DisplayName = request.DisplayName!.Trim(),
            };
            var userResult = await userManager.CreateAsync(user, request.Password!);
            if (!userResult.Succeeded)
            {
                return IdentityFailure(userResult, "The Owner account could not be created.");
            }

            var addRoleResult = await userManager.AddToRoleAsync(user, AuthRoles.Owner);
            if (!addRoleResult.Succeeded)
            {
                return IdentityFailure(addRoleResult, "The Owner account could not be assigned its role.");
            }

            var configuredSystemAdminEmail = authenticationOptions.Value.SystemAdmin.Email?.Trim();
            var isConfiguredSystemAdmin = string.Equals(
                configuredSystemAdminEmail, request.Email.Trim(), StringComparison.OrdinalIgnoreCase);
            if (isConfiguredSystemAdmin)
            {
                var systemAdminRoleResult = await userManager.AddToRoleAsync(user, AuthRoles.SystemAdmin);
                if (!systemAdminRoleResult.Succeeded)
                {
                    return IdentityFailure(systemAdminRoleResult,
                        "The configured SystemAdmin account could not be assigned its role.");
                }
            }

            await tenantMembershipService.EnsureDefaultMembershipAsync(user.Id, cancellationToken);

            dbContext.BootstrapStates.Add(new BootstrapState
            {
                Id = 1,
                CompletedAt = DateTimeOffset.UtcNow,
            });
            await auditWriter.WriteAsync(dbContext, httpContext, null, user.Id, ownerRole.Id,
                "bootstrap.owner-created", new
                {
                    UserId = (Guid?)null,
                    Roles = Array.Empty<string>(),
                    PermissionKeys = Array.Empty<string>(),
                }, new
                {
                    UserId = user.Id,
                    Roles = isConfiguredSystemAdmin
                        ? new[] { AuthRoles.Owner, AuthRoles.SystemAdmin }
                        : new[] { AuthRoles.Owner },
                    PermissionKeys = new[] { "*" },
                }, cancellationToken);
            await transaction.CommitAsync(cancellationToken);

            // The database is authoritative: only issue the application cookie
            // after the owner, role assignment, and one-time marker are committed.
            await tenantMembershipService.SignInWithActiveTenantAsync(signInManager, user, isPersistent: false,
                cancellationToken: cancellationToken);

            return TypedResults.Created("/api/v1/identity/session", new
            {
                user = new AuthUserResponse(user.Id, user.DisplayName, PublicEmail(user.Email),
                    isConfiguredSystemAdmin ? [AuthRoles.Owner, AuthRoles.SystemAdmin] : [AuthRoles.Owner]),
            });
        }
        catch (Exception exception) when (IsExpectedBootstrapConflict(exception))
        {
            // Only serialization/deadlock or the known singleton/Identity unique
            // constraints represent an expected bootstrap race. Other database
            // failures must surface as failures rather than false conflict responses.
            return Error(StatusCodes.Status409Conflict, "bootstrap_unavailable", "The local Owner has already been created.");
        }
    }

    private static async Task<IResult> Login(
        LoginRequest? request,
        HttpContext httpContext,
        SignInManager<ApplicationUser> signInManager,
        UserManager<ApplicationUser> userManager,
        ScimLifecycleService lifecycleService,
        TenantMembershipService tenantMembershipService,
        LoginAttemptThrottle loginThrottle,
        IOptions<VantigoAuthenticationOptions> options,
        CancellationToken cancellationToken)
    {
        var errors = new Dictionary<string, string[]>();
        if (string.IsNullOrWhiteSpace(request?.Email))
        {
            errors["email"] = ["Email is required."];
        }

        if (string.IsNullOrWhiteSpace(request?.Password))
        {
            errors["password"] = ["Password is required."];
        }

        if (errors.Count > 0)
        {
            return Error(StatusCodes.Status400BadRequest, "invalid_request", "The login request is invalid.", errors);
        }

        // Account-scoped throttle in front of any credential work. The IP-only
        // rate-limit policy is a broad backstop; this stops focused stuffing
        // against a single account, including accounts that do not exist.
        var clientAddress = httpContext.Connection.RemoteIpAddress?.ToString() ?? "unknown";
        if (loginThrottle.IsBlocked(request!.Email!, clientAddress))
        {
            return Error(StatusCodes.Status429TooManyRequests, "rate_limited", "Too many authentication attempts. Please try again later.");
        }

        var user = await userManager.FindByEmailAsync(request.Email!.Trim());
        if (user is not null && await lifecycleService.IsEffectivelyDisabledAsync(user.Id, cancellationToken))
        {
            return Error(StatusCodes.Status429TooManyRequests, "account_locked", "The account is temporarily unavailable. Please try again later.");
        }

        var passwordResult = user is null
            ? SignInResult.Failed
            : await signInManager.CheckPasswordSignInAsync(user, request.Password!, lockoutOnFailure: true);
        if (passwordResult.IsLockedOut)
        {
            return Error(StatusCodes.Status429TooManyRequests, "account_locked", "The account is temporarily locked. Please try again later.");
        }
        if (user is null || !passwordResult.Succeeded)
        {
            loginThrottle.RecordFailure(request.Email!, clientAddress);
            return Error(StatusCodes.Status401Unauthorized, "invalid_credentials", "Invalid email or password.");
        }

        loginThrottle.RecordSuccess(request.Email!, clientAddress);

        if (user.TwoFactorEnabled)
        {
            await signInManager.SignOutAsync();
            var principal = new ClaimsPrincipal(new ClaimsIdentity(
                // SignInManager.GetTwoFactorAuthenticationUserAsync reads the
                // pending user's id from ClaimTypes.Name, which is the claim
                // used by Identity's built-in two-factor continuation flow.
                [new Claim(ClaimTypes.Name, user.Id.ToString())],
                IdentityConstants.TwoFactorUserIdScheme));
            await httpContext.SignInAsync(IdentityConstants.TwoFactorUserIdScheme, principal);
            return TypedResults.Ok(new AuthSuccessResponse(null, true, true, false));
        }

        var cleanupResult = await RemoveHistoricalMfaClaims(user, userManager);
        if (!cleanupResult.Succeeded)
        {
            return IdentityFailure(cleanupResult, "The sign-in could not be completed.");
        }

        // Presenting credentials earns a new session: never inherit the absolute
        // lifetime of a session that happens to still be live on this browser.
        SessionValidationService.BeginFreshSession(httpContext);
        await tenantMembershipService.SignInWithActiveTenantAsync(signInManager, user, isPersistent: false,
            cancellationToken: cancellationToken);
        var roles = await userManager.GetRolesAsync(user);
        var response = new AuthUserResponse(user.Id, user.DisplayName, PublicEmail(user.Email), AuthRoleOrdering.Ordered(roles));
        return TypedResults.Ok(new AuthSuccessResponse(
            response,
            false,
            user.TwoFactorEnabled,
            roles.Contains(AuthRoles.Owner, StringComparer.Ordinal) &&
                options.Value.Owners.RequireMfa && !user.TwoFactorEnabled));
    }

    private static async Task<IResult> CompleteTwoFactorLogin(
        TwoFactorRequest? request,
        HttpContext httpContext,
        SignInManager<ApplicationUser> signInManager,
        UserManager<ApplicationUser> userManager,
        IOptions<VantigoAuthenticationOptions> options,
        TenantMembershipService tenantMembershipService,
        CancellationToken cancellationToken)
    {
        if (string.IsNullOrWhiteSpace(request?.Code))
        {
            return Error(StatusCodes.Status400BadRequest, "invalid_request", "A two-factor code is required.");
        }

        var user = await signInManager.GetTwoFactorAuthenticationUserAsync();
        if (user is null)
        {
            return Error(StatusCodes.Status401Unauthorized, "two_factor_session_expired", "The two-factor sign-in session has expired.");
        }

        if (user.IsDisabled || AuthAccountState.IsLockedOut(user.LockoutEnd, DateTimeOffset.UtcNow))
        {
            await signInManager.SignOutAsync();
            await httpContext.SignOutAsync(IdentityConstants.TwoFactorUserIdScheme);
            return Error(StatusCodes.Status429TooManyRequests, "account_locked", "The account is temporarily unavailable. Please try again later.");
        }

        var code = request.Code.Replace(" ", string.Empty, StringComparison.Ordinal);
        // Identity recovery codes are not required to contain punctuation. The
        // built-in authenticator provider emits six numeric digits, so only that
        // shape is routed to TOTP; every other opaque code is tried as a recovery
        // code, preserving Identity's one-use store semantics.
        var isAuthenticatorCode = code.Length == 6 && code.All(char.IsDigit);
        var result = isAuthenticatorCode
            ? await signInManager.TwoFactorAuthenticatorSignInAsync(code, request.RememberMe, true)
            : await signInManager.TwoFactorRecoveryCodeSignInAsync(code);
        if (!result.Succeeded)
        {
            return Error(result.IsLockedOut ? StatusCodes.Status429TooManyRequests : StatusCodes.Status401Unauthorized,
                result.IsLockedOut ? "account_locked" : "invalid_two_factor_code",
                result.IsLockedOut ? "The account is temporarily locked. Please try again later." : "The two-factor code is invalid.");
        }

        var cleanupResult = await RemoveHistoricalMfaClaims(user, userManager);
        if (!cleanupResult.Succeeded)
        {
            return IdentityFailure(cleanupResult, "The two-factor sign-in could not be completed.");
        }

        await signInManager.SignOutAsync();
        SessionValidationService.BeginFreshSession(httpContext);
        await tenantMembershipService.SignInWithActiveTenantAsync(signInManager, user, isPersistent: request.RememberMe,
            priorPrincipal: new ClaimsPrincipal(new ClaimsIdentity(MfaClaims.Issue())), cancellationToken: cancellationToken);
        var roles = await userManager.GetRolesAsync(user);
        var response = new AuthUserResponse(user.Id, user.DisplayName, PublicEmail(user.Email), AuthRoleOrdering.Ordered(roles));
        var requiresEnrollment = await userManager.IsInRoleAsync(user, AuthRoles.Owner) &&
            options.Value.Owners.RequireMfa && !user.TwoFactorEnabled;
        return TypedResults.Ok(new AuthSuccessResponse(response, false, true, requiresEnrollment));
    }

    private static async Task<IResult> Logout(SignInManager<ApplicationUser> signInManager)
    {
        await signInManager.SignOutAsync();
        return TypedResults.Ok(new LogoutResponse(true));
    }

    private static async Task<IResult> Session(
        ClaimsPrincipal principal,
        UserManager<ApplicationUser> userManager,
        IOptions<VantigoAuthenticationOptions> options,
        TenantMembershipService tenantMembershipService,
        CancellationToken cancellationToken)
    {
        var user = await userManager.GetUserAsync(principal);
        if (user is null)
        {
            return Error(StatusCodes.Status401Unauthorized, "unauthenticated", "The session is not authenticated.");
        }

        var roles = await userManager.GetRolesAsync(user);
        var tenants = await tenantMembershipService.GetTenantsAsync(user.Id, cancellationToken);
        var activeTenantId = await tenantMembershipService.ResolveActiveTenantIdAsync(user, principal, cancellationToken);
        var mfaAuthenticated = MfaClaims.Any(principal.Claims);
        var owner = roles.Contains(AuthRoles.Owner, StringComparer.Ordinal);
        var isSystemAdmin = roles.Contains(AuthRoles.SystemAdmin, StringComparer.Ordinal);
        return TypedResults.Ok(new AuthSessionResponse(
            new AuthUserResponse(user.Id, user.DisplayName, PublicEmail(user.Email), AuthRoleOrdering.Ordered(roles)),
            user.TwoFactorEnabled,
            owner && options.Value.Owners.RequireMfa && !user.TwoFactorEnabled,
            mfaAuthenticated,
            isSystemAdmin,
            tenants.Select(tenant => new TenantSessionResponse(tenant.Id, tenant.Name, tenant.Slug)).ToArray(),
            activeTenantId));
    }

    private static async Task<IResult> SwitchTenant(
        TenantSwitchRequest? request,
        ClaimsPrincipal principal,
        UserManager<ApplicationUser> userManager,
        SignInManager<ApplicationUser> signInManager,
        TenantMembershipService tenantMembershipService,
        CancellationToken cancellationToken)
    {
        if (request is null || request.TenantId == Guid.Empty)
            return Error(StatusCodes.Status400BadRequest, "invalid_request", "A tenant id is required.");

        var user = await userManager.GetUserAsync(principal);
        if (user is null) return Error(StatusCodes.Status401Unauthorized, "unauthenticated", "Authentication is required.");

        var tenants = await tenantMembershipService.GetTenantsAsync(user.Id, cancellationToken);
        if (!tenants.Any(tenant => tenant.Id == request.TenantId))
            return Error(StatusCodes.Status403Forbidden, "tenant_membership_required", "You are not a member of that tenant.");

        user.ActiveTenantId = request.TenantId;
        await userManager.UpdateAsync(user);
        await tenantMembershipService.SignInWithActiveTenantAsync(signInManager, user, principal, cancellationToken: cancellationToken);
        var roles = await userManager.GetRolesAsync(user);
        var activeTenants = tenants.Select(tenant => new TenantSessionResponse(tenant.Id, tenant.Name, tenant.Slug)).ToArray();
        var mfaAuthenticated = MfaClaims.Any(principal.Claims);
        var isSystemAdmin = roles.Contains(AuthRoles.SystemAdmin, StringComparer.Ordinal);
        return TypedResults.Ok(new AuthSessionResponse(
            new AuthUserResponse(user.Id, user.DisplayName, PublicEmail(user.Email), AuthRoleOrdering.Ordered(roles)),
            user.TwoFactorEnabled,
            false,
            mfaAuthenticated,
            isSystemAdmin,
            activeTenants,
            request.TenantId));
    }

    private static async Task<bool> IsBootstrapConsumed(
        AccountsDbContext dbContext,
        RoleManager<IdentityRole<Guid>> roleManager,
        CancellationToken cancellationToken)
    {
        if (await dbContext.BootstrapStates.AnyAsync(state => state.Id == 1, cancellationToken))
        {
            return true;
        }

        var ownerRole = await roleManager.FindByNameAsync(AuthRoles.Owner);
        return ownerRole is not null && await dbContext.UserRoles.AnyAsync(
            userRole => userRole.RoleId == ownerRole.Id, cancellationToken);
    }

    private static Dictionary<string, string[]> ValidateBootstrapRequest(BootstrapRequest? request)
    {
        var errors = new Dictionary<string, string[]>();
        if (string.IsNullOrWhiteSpace(request?.Secret))
        {
            errors["secret"] = ["Secret is required."];
        }

        if (string.IsNullOrWhiteSpace(request?.Email) || !request.Email.Contains('@', StringComparison.Ordinal))
        {
            errors["email"] = ["A valid email is required."];
        }

        if (string.IsNullOrWhiteSpace(request?.DisplayName))
        {
            errors["displayName"] = ["Display name is required."];
        }

        if (string.IsNullOrWhiteSpace(request?.Password))
        {
            errors["password"] = ["Password is required."];
        }

        return errors;
    }

    private static bool SecretMatches(string configuredSecret, string suppliedSecret)
    {
        var configuredBytes = Encoding.UTF8.GetBytes(configuredSecret);
        var suppliedBytes = Encoding.UTF8.GetBytes(suppliedSecret);
        return configuredBytes.Length == suppliedBytes.Length &&
            CryptographicOperations.FixedTimeEquals(configuredBytes, suppliedBytes);
    }

    private static IResult IdentityFailure(IdentityResult result, string message)
    {
        var fields = result.Errors
            .GroupBy(error => error.Code, StringComparer.Ordinal)
            .ToDictionary(group => group.Key, group => group.Select(error => error.Description).ToArray(), StringComparer.Ordinal);
        return Error(StatusCodes.Status400BadRequest, "identity_validation_failed", message, fields);
    }

    private static bool IsExpectedBootstrapConflict(Exception exception)
    {
        for (var current = exception; current is not null; current = current.InnerException)
        {
            if (current is PostgresException postgres && IsExpectedBootstrapConflict(postgres))
            {
                return true;
            }
        }

        return false;
    }

    private static bool IsExpectedBootstrapConflict(PostgresException postgres) =>
        postgres.SqlState is PostgresErrorCodes.SerializationFailure or PostgresErrorCodes.DeadlockDetected ||
            postgres.SqlState == PostgresErrorCodes.UniqueViolation &&
            postgres.ConstraintName is "pk_bootstrap_states" or "ux_roles_normalized_name" or
                "ux_users_normalized_user_name" or "pk_user_roles";

    private static async Task<IdentityResult> RemoveHistoricalMfaClaims(
        ApplicationUser user,
        UserManager<ApplicationUser> userManager)
    {
        var claims = (await userManager.GetClaimsAsync(user)).Where(MfaClaims.Is).ToArray();
        if (claims.Length > 0)
        {
            return await userManager.RemoveClaimsAsync(user, claims);
        }

        return IdentityResult.Success;
    }

    private static IResult Error(
        int statusCode,
        string code,
        string message,
        IReadOnlyDictionary<string, string[]>? fields = null) =>
        TypedResults.Json(new AuthErrorResponse(new AuthError(code, message, fields)), statusCode: statusCode);

    private static string? PublicEmail(string? email) =>
        WorkforceOidcOptions.IsOpaqueEmail(email) ? null : email;
}