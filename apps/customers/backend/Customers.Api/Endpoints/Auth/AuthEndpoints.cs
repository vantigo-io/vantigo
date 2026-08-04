using System.Security.Claims;
using System.Security.Cryptography;
using System.Text;

using Microsoft.AspNetCore.Antiforgery;
using Microsoft.AspNetCore.Authentication;
using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;

using Npgsql;

using Vantigo.Customers.Api.Database.Accounts;
using Vantigo.Customers.Api.Services;

namespace Vantigo.Customers.Api.Endpoints.Auth;

internal static class AuthEndpoints
{
    internal static IEndpointRouteBuilder MapAuthEndpoints(
        this IEndpointRouteBuilder app,
        WorkforceOidcOptions workforceOidc)
    {
        var group = app.MapGroup("/auth").WithTags("Authentication");

        group.MapGet("/antiforgery", AntiforgeryToken)
            .WithSummary("Get the CSRF token for same-origin state-changing requests");

        group.MapGet("/bootstrap-status", BootstrapStatus)
            .WithSummary("Get whether one-time local Owner bootstrap is available");

        group.MapGet("/providers", WorkforceOidcEndpoints.Providers)
            .WithSummary("Get the configured workforce sign-in providers");

        if (workforceOidc.Enabled)
        {
            group.MapGet("/oidc/challenge", WorkforceOidcEndpoints.Challenge)
                .WithSummary("Start workforce OpenID Connect sign-in");
        }

        group.MapGet("/oidc/complete", WorkforceOidcEndpoints.Complete)
            .WithSummary("Complete workforce OpenID Connect sign-in");

        group.MapPost("/bootstrap", Bootstrap)
            .WithSummary("Create the single local Owner account")
            .RequireAntiforgery()
            .RequireRateLimiting(AuthRateLimitPolicies.Bootstrap);

        group.MapPost("/login", Login)
            .WithSummary("Sign in with the application cookie")
            .RequireAntiforgery()
            .RequireRateLimiting(AuthRateLimitPolicies.Login);

        group.MapPost("/login/2fa", CompleteTwoFactorLogin)
            .WithSummary("Complete an Owner authenticator or recovery-code login")
            .RequireAntiforgery()
            .RequireRateLimiting(AuthRateLimitPolicies.Mfa);

        group.MapPost("/logout", Logout)
            .WithSummary("Sign out of the application cookie")
            .RequireAuthorization()
            .RequireAntiforgery();

        group.MapGet("/session", Session)
            .WithSummary("Get the authenticated browser session")
            .RequireAuthorization();

        app.MapAccountAuthEndpoints();

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
        CancellationToken cancellationToken)
    {
        // This endpoint is an anonymous UI hint only. The POST endpoint remains
        // authoritative and performs the serializable, one-time bootstrap race
        // protection before creating the Owner.
        var available = !string.IsNullOrEmpty(bootstrapSecret.Secret) &&
            !await IsBootstrapConsumed(dbContext, roleManager, cancellationToken);
        return TypedResults.Ok(new BootstrapStatusResponse(available));
    }

    private static async Task<IResult> Bootstrap(
        BootstrapRequest? request,
        BootstrapSecretProvider bootstrapSecret,
        AccountsDbContext dbContext,
        UserManager<ApplicationUser> userManager,
        RoleManager<IdentityRole<Guid>> roleManager,
        SignInManager<ApplicationUser> signInManager,
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

            if (await IsBootstrapConsumed(dbContext, roleManager, cancellationToken))
            {
                return Error(StatusCodes.Status409Conflict, "bootstrap_unavailable", "The local Owner has already been created.");
            }

            var ownerRole = await roleManager.FindByNameAsync(AuthRoles.Owner);
            if (ownerRole is null)
            {
                ownerRole = new IdentityRole<Guid>(AuthRoles.Owner);
                var roleResult = await roleManager.CreateAsync(ownerRole);
                if (!roleResult.Succeeded)
                {
                    return IdentityFailure(roleResult, "Unable to create the Owner role.");
                }
            }

            if (await roleManager.FindByNameAsync(AuthRoles.User) is null)
            {
                var standardRoleResult = await roleManager.CreateAsync(new IdentityRole<Guid>(AuthRoles.User));
                if (!standardRoleResult.Succeeded)
                {
                    return IdentityFailure(standardRoleResult, "Unable to create the User role.");
                }
            }

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

            dbContext.BootstrapStates.Add(new BootstrapState
            {
                Id = 1,
                CompletedAt = DateTimeOffset.UtcNow,
            });
            await dbContext.SaveChangesAsync(cancellationToken);
            await transaction.CommitAsync(cancellationToken);

            // The database is authoritative: only issue the application cookie
            // after the owner, role assignment, and one-time marker are committed.
            await signInManager.SignInAsync(user, isPersistent: false);

            return TypedResults.Created("/auth/session", new
            {
                user = new AuthUserResponse(user.Id, user.DisplayName, PublicEmail(user.Email), [AuthRoles.Owner]),
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
        IConfiguration configuration,
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

        var user = await userManager.FindByEmailAsync(request!.Email!.Trim());
        var passwordResult = user is null
            ? SignInResult.Failed
            : await signInManager.CheckPasswordSignInAsync(user, request.Password!, lockoutOnFailure: true);
        if (passwordResult.IsLockedOut)
        {
            return Error(StatusCodes.Status429TooManyRequests, "account_locked", "The account is temporarily locked. Please try again later.");
        }
        if (user is null || !passwordResult.Succeeded)
        {
            return Error(StatusCodes.Status401Unauthorized, "invalid_credentials", "Invalid email or password.");
        }

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

        await signInManager.SignInAsync(user, isPersistent: false);
        var roles = await userManager.GetRolesAsync(user);
        var response = new AuthUserResponse(user.Id, user.DisplayName, PublicEmail(user.Email), roles.ToArray());
        return TypedResults.Ok(new AuthSuccessResponse(
            response,
            false,
            user.TwoFactorEnabled,
            roles.Contains(AuthRoles.Owner, StringComparer.Ordinal) &&
                configuration.GetValue<bool>("Authentication:Owners:RequireMfa") && !user.TwoFactorEnabled));
    }

    private static async Task<IResult> CompleteTwoFactorLogin(
        TwoFactorRequest? request,
        SignInManager<ApplicationUser> signInManager,
        UserManager<ApplicationUser> userManager,
        IConfiguration configuration,
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

        var existingClaims = await userManager.GetClaimsAsync(user);
        if (!existingClaims.Any(claim => claim.Type == "amr" &&
            string.Equals(claim.Value, "mfa", StringComparison.OrdinalIgnoreCase)))
        {
            var claimResult = await userManager.AddClaimAsync(user, new Claim("amr", "mfa"));
            if (!claimResult.Succeeded)
            {
                return IdentityFailure(claimResult, "The two-factor sign-in could not be completed.");
            }
        }

        await signInManager.SignOutAsync();
        await signInManager.SignInWithClaimsAsync(user, new AuthenticationProperties { IsPersistent = request.RememberMe },
            MfaClaims());
        var roles = await userManager.GetRolesAsync(user);
        var response = new AuthUserResponse(user.Id, user.DisplayName, PublicEmail(user.Email), roles.ToArray());
        var requiresEnrollment = userManager.IsInRoleAsync(user, AuthRoles.Owner).Result &&
            configuration.GetValue<bool>("Authentication:Owners:RequireMfa") && !user.TwoFactorEnabled;
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
        IConfiguration configuration)
    {
        var user = await userManager.GetUserAsync(principal);
        if (user is null)
        {
            return Error(StatusCodes.Status401Unauthorized, "unauthenticated", "The session is not authenticated.");
        }

        var roles = await userManager.GetRolesAsync(user);
        var mfaAuthenticated = principal.Claims.Any(claim =>
            (claim.Type == "amr" || claim.Type == ClaimTypes.AuthenticationMethod) &&
            string.Equals(claim.Value, "mfa", StringComparison.OrdinalIgnoreCase));
        var owner = roles.Contains(AuthRoles.Owner, StringComparer.Ordinal);
        return TypedResults.Ok(new AuthSessionResponse(
            new AuthUserResponse(user.Id, user.DisplayName, PublicEmail(user.Email), roles.ToArray()),
            user.TwoFactorEnabled,
            owner && configuration.GetValue<bool>("Authentication:Owners:RequireMfa") && !user.TwoFactorEnabled,
            mfaAuthenticated));
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

    private static IEnumerable<Claim> MfaClaims() =>
        [new Claim("amr", "mfa"), new Claim(ClaimTypes.AuthenticationMethod, "mfa")];

    private static IResult Error(
        int statusCode,
        string code,
        string message,
        IReadOnlyDictionary<string, string[]>? fields = null) =>
        TypedResults.Json(new AuthErrorResponse(new AuthError(code, message, fields)), statusCode: statusCode);

    private static string? PublicEmail(string? email) =>
        WorkforceOidcOptions.IsOpaqueEmail(email) ? null : email;
}