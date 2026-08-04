using System.Security.Claims;
using System.Security.Cryptography;
using System.Text;

using Microsoft.AspNetCore.Authentication;
using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;

using Npgsql;

using Vantigo.Customers.Api.Database.Accounts;

namespace Vantigo.Customers.Api.Endpoints.Auth;

internal static class WorkforceOidcEndpoints
{
    private const string SubjectClaim = "sub";
    private const string EmailClaim = "email";
    private const string EmailVerifiedClaim = "email_verified";
    private const string NameClaim = "name";
    private const string GivenNameClaim = "given_name";
    private const string FamilyNameClaim = "family_name";
    private const string PreferredUsernameClaim = "preferred_username";

    internal static IResult Providers(WorkforceOidcOptions options) =>
        TypedResults.Ok(new OidcProvidersResponse(
            options.Enabled ? new OidcProviderResponse(options.DisplayName) : null));

    internal static IResult Challenge(WorkforceOidcOptions options)
    {
        if (!options.Enabled)
        {
            return TypedResults.NotFound();
        }

        // The return URI is fixed. In particular, never accept a return URL from
        // the browser because that would turn this endpoint into an open redirect.
        return TypedResults.Challenge(
            new AuthenticationProperties { RedirectUri = WorkforceOidcOptions.CompletionPath },
            [WorkforceOidcOptions.Scheme]);
    }

    internal static async Task<IResult> Complete(
        HttpContext httpContext,
        WorkforceOidcOptions options,
        UserManager<ApplicationUser> userManager,
        RoleManager<IdentityRole<Guid>> roleManager,
        SignInManager<ApplicationUser> signInManager,
        AccountsDbContext dbContext,
        CancellationToken cancellationToken)
    {
        if (!options.Enabled)
        {
            return TypedResults.NotFound();
        }

        var external = await httpContext.AuthenticateAsync(IdentityConstants.ExternalScheme);
        if (!external.Succeeded || external.Principal is null)
        {
            return await Failure(httpContext, "oidc_external_identity_missing");
        }

        var identity = ReadIdentity(external.Principal, options.Authority);
        if (identity is null)
        {
            return await Failure(httpContext, "oidc_identity_invalid");
        }

        try
        {
            var existingUser = await userManager.FindByLoginAsync(identity.LoginProvider, identity.Subject);
            if (existingUser is not null)
            {
                // External claims never become local roles or MFA proof. Identity's
                // normal external-login path checks lockout/status and, when enabled,
                // starts its local two-factor continuation rather than bypassing it.
                var signInResult = await signInManager.ExternalLoginSignInAsync(
                    identity.LoginProvider,
                    identity.Subject,
                    isPersistent: false,
                    bypassTwoFactor: false);
                if (signInResult.Succeeded)
                {
                    return await Success(httpContext);
                }

                return await Failure(httpContext, signInResult.IsLockedOut
                    ? "account_locked"
                    : signInResult.RequiresTwoFactor
                        ? "local_mfa_required"
                        : "oidc_local_sign_in_failed");
            }

            // Email is informational for a new JIT account only. It is never used
            // to attach a provider identity to an existing local account.
            if (identity.Email is not null && await userManager.FindByEmailAsync(identity.Email) is not null)
            {
                return await Failure(httpContext, "oidc_email_conflict");
            }

            var created = await ProvisionNewUser(
                identity,
                options,
                userManager,
                roleManager,
                dbContext,
                cancellationToken);
            if (created is null)
            {
                // A concurrent request may have committed this exact login while
                // this request was provisioning. Re-read the unique login after the
                // transaction has been rolled back, then use that local account.
                var racedUser = await userManager.FindByLoginAsync(identity.LoginProvider, identity.Subject);
                if (racedUser is null)
                {
                    return await Failure(httpContext, "oidc_sign_in_unavailable");
                }

                var racedSignIn = await signInManager.ExternalLoginSignInAsync(
                    identity.LoginProvider,
                    identity.Subject,
                    isPersistent: false,
                    bypassTwoFactor: false);
                return racedSignIn.Succeeded
                    ? await Success(httpContext)
                    : await Failure(httpContext, racedSignIn.IsLockedOut
                        ? "account_locked"
                        : racedSignIn.RequiresTwoFactor
                            ? "local_mfa_required"
                            : "oidc_local_sign_in_failed");
            }

            await signInManager.SignInAsync(created, isPersistent: false);
            return await Success(httpContext);
        }
        catch (Exception exception) when (IsSafeProvisioningConflict(exception))
        {
            // Do not expose provider or database details in a browser redirect. The
            // only recoverable race is handled by the re-read above; other expected
            // transaction conflicts get one generic machine-readable error.
            return await Failure(httpContext, "oidc_sign_in_unavailable");
        }
    }

    private static async Task<ApplicationUser?> ProvisionNewUser(
        ExternalIdentity identity,
        WorkforceOidcOptions options,
        UserManager<ApplicationUser> userManager,
        RoleManager<IdentityRole<Guid>> roleManager,
        AccountsDbContext dbContext,
        CancellationToken cancellationToken)
    {
        await using var transaction = await dbContext.Database.BeginTransactionAsync(
            System.Data.IsolationLevel.Serializable, cancellationToken);

        var existingEmail = identity.Email is null
            ? null
            : await userManager.FindByEmailAsync(identity.Email);
        if (existingEmail is not null)
        {
            await transaction.RollbackAsync(cancellationToken);
            return null;
        }

        var userRole = await roleManager.FindByNameAsync(AuthRoles.User);
        if (userRole is null)
        {
            var roleResult = await roleManager.CreateAsync(new IdentityRole<Guid>(AuthRoles.User));
            if (!roleResult.Succeeded)
            {
                await transaction.RollbackAsync(cancellationToken);
                throw new InvalidOperationException("The local User role could not be created.");
            }
        }

        var user = new ApplicationUser
        {
            UserName = StableUserName(identity.LoginProvider, identity.Subject),
            // Identity is configured with unique email addresses. Provider users
            // without an email receive a deterministic reserved .invalid address;
            // it is never treated as deliverable or used for email linking.
            Email = identity.Email ?? WorkforceOidcOptions.CreateOpaqueEmail(identity.LoginProvider, identity.Subject),
            EmailConfirmed = identity.EmailVerified,
            DisplayName = identity.DisplayName,
        };
        var createResult = await userManager.CreateAsync(user);
        if (!createResult.Succeeded)
        {
            await transaction.RollbackAsync(cancellationToken);
            return null;
        }

        var roleAssignment = await userManager.AddToRoleAsync(user, AuthRoles.User);
        if (!roleAssignment.Succeeded)
        {
            await transaction.RollbackAsync(cancellationToken);
            return null;
        }

        var loginResult = await userManager.AddLoginAsync(user, new UserLoginInfo(
            identity.LoginProvider,
            identity.Subject,
            options.DisplayName));
        if (!loginResult.Succeeded)
        {
            // AddLoginAsync is protected by the composite primary key
            // (normalized issuer, case-sensitive sub). Roll back this new user
            // and let the caller re-read the login so a concurrent JIT request
            // never assigns the same provider identity twice.
            await transaction.RollbackAsync(cancellationToken);
            return null;
        }

        await transaction.CommitAsync(cancellationToken);
        return user;
    }

    private static ExternalIdentity? ReadIdentity(ClaimsPrincipal principal, string configuredAuthority)
    {
        var issuer = principal.FindFirst(WorkforceOidcOptions.ValidatedIssuerClaim)?.Value;
        var subject = principal.FindFirst(SubjectClaim)?.Value;
        if (!WorkforceOidcOptions.TryNormalizeIssuer(issuer, out var normalizedIssuer) ||
            !WorkforceOidcOptions.TryNormalizeIssuer(configuredAuthority, out var configuredIssuer) ||
            !string.Equals(normalizedIssuer, configuredIssuer, StringComparison.Ordinal) ||
            string.IsNullOrWhiteSpace(subject) ||
            subject.Any(char.IsWhiteSpace) ||
            subject.Length > 512)
        {
            return null;
        }

        // OIDC issuer comparison is case-insensitive for scheme/host but the path
        // remains case-sensitive. Keeping this canonical form in LoginProvider and
        // keeping sub untouched gives each deployment a durable issuer+case-sensitive
        // subject key without storing provider tokens or copying arbitrary claims.
        var email = ReadEmail(principal.FindFirst(EmailClaim)?.Value);
        var displayName = ReadDisplayName(principal, subject);
        var emailVerified = string.Equals(
            principal.FindFirst(EmailVerifiedClaim)?.Value,
            "true",
            StringComparison.OrdinalIgnoreCase);
        return new ExternalIdentity(normalizedIssuer!, subject, email, emailVerified, displayName);
    }

    private static string? ReadEmail(string? value)
    {
        if (string.IsNullOrWhiteSpace(value) || value.Length > 256 || value.Any(char.IsWhiteSpace) ||
            value.Any(char.IsControl) || value.Count(character => character == '@') != 1)
        {
            return null;
        }

        var at = value.IndexOf('@');
        return at > 0 && at < value.Length - 1 ? value.Trim() : null;
    }

    private static string ReadDisplayName(ClaimsPrincipal principal, string subject)
    {
        var directName = CleanDisplayName(principal.FindFirst(NameClaim)?.Value);
        if (directName is not null)
        {
            return directName;
        }

        var given = CleanDisplayName(principal.FindFirst(GivenNameClaim)?.Value);
        var family = CleanDisplayName(principal.FindFirst(FamilyNameClaim)?.Value);
        var combined = CleanDisplayName(string.Join(' ', new[] { given, family }.Where(value => value is not null)));
        if (combined is not null)
        {
            return combined;
        }

        return CleanDisplayName(principal.FindFirst(PreferredUsernameClaim)?.Value) ??
            (subject.Length > 200 ? subject[..200] : subject);
    }

    private static string? CleanDisplayName(string? value)
    {
        if (string.IsNullOrWhiteSpace(value) || value.Any(char.IsControl))
        {
            return null;
        }

        var normalized = string.Join(' ', value.Split((char[]?)null, StringSplitOptions.RemoveEmptyEntries));
        return normalized.Length is 0 or > 200 ? null : normalized;
    }

    private static string StableUserName(string issuer, string subject)
    {
        var bytes = SHA256.HashData(Encoding.UTF8.GetBytes($"{issuer}\0{subject}"));
        return $"oidc-{Convert.ToHexString(bytes).ToLowerInvariant()}";
    }

    private static async Task<IResult> Success(HttpContext context)
    {
        await context.SignOutAsync(IdentityConstants.ExternalScheme);
        return TypedResults.Redirect("/");
    }

    private static async Task<IResult> Failure(HttpContext context, string code)
    {
        await context.SignOutAsync(IdentityConstants.ExternalScheme);
        return TypedResults.Redirect($"/sign-in?error={code}");
    }

    private static bool IsSafeProvisioningConflict(Exception exception)
    {
        for (var current = exception; current is not null; current = current.InnerException)
        {
            if (current is PostgresException postgres &&
                (postgres.SqlState is PostgresErrorCodes.SerializationFailure or PostgresErrorCodes.DeadlockDetected ||
                 postgres.SqlState == PostgresErrorCodes.UniqueViolation &&
                 postgres.ConstraintName is "pk_user_logins" or "ux_users_normalized_user_name" or "ux_roles_normalized_name"))
            {
                return true;
            }

            if (current is DbUpdateException)
            {
                continue;
            }
        }

        return false;
    }

    private sealed record ExternalIdentity(
        string LoginProvider,
        string Subject,
        string? Email,
        bool EmailVerified,
        string DisplayName);
}