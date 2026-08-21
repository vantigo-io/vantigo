using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Options;

namespace Vantigo.Configuration;

/// <summary>
/// Shared authentication configuration.
/// </summary>
public sealed class VantigoAuthenticationOptions
{
    public SystemAdminAuthenticationOptions SystemAdmin { get; set; } = new();

    public OwnerAuthenticationOptions Owners { get; set; } = new();

    public BootstrapSecretOptions Bootstrap { get; set; } = new();

    public InvitationOptions Invitations { get; set; } = new();

    public PasswordResetOptions PasswordReset { get; set; } = new();

    public OidcOptions Oidc { get; set; } = new();

    public ScimOptions Scim { get; set; } = new();

    public SessionAuthenticationOptions Sessions { get; set; } = new();
}

/// <summary>
/// Bounds on the application cookie session. The sliding idle window is what the
/// cookie itself enforces; the absolute lifetime is enforced on every cookie
/// validation so an actively used session cannot renew indefinitely. Privileged
/// sessions (Owner, SystemAdmin) get the tighter pair of bounds.
/// </summary>
public sealed class SessionAuthenticationOptions
{
    /// <summary>Idle window for a standard session. Also the cookie's expiry span.</summary>
    public TimeSpan IdleTimeout { get; set; } = TimeSpan.FromHours(8);

    /// <summary>Idle window for an Owner or SystemAdmin session.</summary>
    public TimeSpan PrivilegedIdleTimeout { get; set; } = TimeSpan.FromHours(2);

    /// <summary>Hard cap on a standard session, measured from sign-in.</summary>
    public TimeSpan AbsoluteLifetime { get; set; } = TimeSpan.FromHours(24);

    /// <summary>Hard cap on an Owner or SystemAdmin session, measured from sign-in.</summary>
    public TimeSpan PrivilegedAbsoluteLifetime { get; set; } = TimeSpan.FromHours(8);

    /// <summary>
    /// How long a user's revocation state (security stamp and effective disabled
    /// state) may be served from memory before it is re-read. A cached decision is
    /// only ever used to admit a request: every negative outcome is re-read from the
    /// database before the session is rejected, and an explicit revocation evicts the
    /// entry, so this is an upper bound on incidental propagation delay only.
    /// </summary>
    public TimeSpan RevocationCacheDuration { get; set; } = TimeSpan.FromSeconds(30);

    /// <summary>
    /// How often ASP.NET Core Identity rebuilds the cookie principal from the
    /// database. Zero rebuilds it on every request, which costs a database round trip
    /// and a Set-Cookie per request; revocation does not depend on it because
    /// <see cref="RevocationCacheDuration"/> governs the security-stamp check.
    /// </summary>
    public TimeSpan PrincipalRefreshInterval { get; set; } = TimeSpan.FromMinutes(15);
}

public sealed class SystemAdminAuthenticationOptions
{
    /// <summary>
    /// Email of the existing account that should receive the protected global
    /// SystemAdmin role during application startup.
    /// </summary>
    public string? Email { get; set; }
}

public sealed class OwnerAuthenticationOptions
{
    public bool RequireMfa { get; set; }
    public string MfaIssuer { get; set; } = "Vantigo";
}

/// <summary>
/// Outside Development, <see cref="Secret"/> must be explicitly configured
/// (for example, via Key Vault); it is never generated or logged there.
/// </summary>
public sealed class BootstrapSecretOptions
{
    public string? Secret { get; set; }
}

public sealed class InvitationOptions
{
    public TimeSpan? Lifetime { get; set; }
    public string? AcceptUrl { get; set; }
}

public sealed class PasswordResetOptions
{
    public string? ResetUrl { get; set; }
}

public sealed class OidcOptions
{
    public bool Enabled { get; set; }

    public string? Provider { get; set; }

    public string? Authority { get; set; }

    public string? ClientId { get; set; }

    public string? ClientAuthentication { get; set; }

    public string? ClientSecret { get; set; }

    public string? WorkloadIdentityTokenFile { get; set; }

    public string[]? AllowedDomains { get; set; }

    public string? DisplayName { get; set; }

    // Retained only to reject old/custom callback configuration explicitly. The
    // effective callback is always WorkforceOidcOptions.DefaultCallbackPath.
    public string? CallbackPath { get; set; }
}

public sealed class ScimOptions
{
    public bool Enabled { get; set; }

    public string? BearerToken { get; set; }

    public string? BearerTokenFile { get; set; }

    public string? PreviousBearerToken { get; set; }

    public string? PreviousBearerTokenFile { get; set; }

    public DateTimeOffset? PreviousBearerTokenExpiresAtUtc { get; set; }

}

public static class VantigoAuthenticationConfigurationExtensions
{
    public static IServiceCollection AddVantigoAuthenticationOptions(this IServiceCollection services, IConfiguration configuration)
    {
        services.AddOptions<VantigoAuthenticationOptions>()
            .Bind(configuration.GetSection("Authentication"))
            .ValidateOnStart();
        services.AddSingleton<IValidateOptions<VantigoAuthenticationOptions>, BootstrapSecretOptionsValidator>();
        services.AddSingleton<IValidateOptions<VantigoAuthenticationOptions>, SessionAuthenticationOptionsValidator>();
        return services;
    }

    /// <summary>
    /// Fails startup outside Development when no bootstrap secret is configured.
    /// A missing secret is otherwise generated per-process and logged, which is
    /// acceptable only for local Development.
    /// </summary>
    private sealed class BootstrapSecretOptionsValidator(IHostEnvironment environment) : IValidateOptions<VantigoAuthenticationOptions>
    {
        public ValidateOptionsResult Validate(string? name, VantigoAuthenticationOptions options)
        {
            if (environment.IsDevelopment() || !string.IsNullOrWhiteSpace(options.Bootstrap.Secret))
                return ValidateOptionsResult.Success;

            return ValidateOptionsResult.Fail(
                "Authentication configuration error: Bootstrap:Secret is required outside Development. " +
                "Configure an explicit high-entropy secret (for example, via Key Vault) before starting; " +
                "it is never generated or logged outside Development.");
        }
    }

    /// <summary>
    /// Fails startup on session bounds that would either never expire a session or
    /// expire it immediately. Misconfiguring these locks every user out, so they are
    /// checked before the host accepts traffic rather than at first sign-in.
    /// </summary>
    private sealed class SessionAuthenticationOptionsValidator : IValidateOptions<VantigoAuthenticationOptions>
    {
        public ValidateOptionsResult Validate(string? name, VantigoAuthenticationOptions options)
        {
            SessionAuthenticationOptions sessions = options.Sessions;
            List<string> failures = [];
            RequirePositive(failures, "Sessions:IdleTimeout", sessions.IdleTimeout);
            RequirePositive(failures, "Sessions:PrivilegedIdleTimeout", sessions.PrivilegedIdleTimeout);
            RequirePositive(failures, "Sessions:AbsoluteLifetime", sessions.AbsoluteLifetime);
            RequirePositive(failures, "Sessions:PrivilegedAbsoluteLifetime", sessions.PrivilegedAbsoluteLifetime);
            RequireNonNegative(failures, "Sessions:RevocationCacheDuration", sessions.RevocationCacheDuration);
            RequireNonNegative(failures, "Sessions:PrincipalRefreshInterval", sessions.PrincipalRefreshInterval);

            return failures.Count == 0
                ? ValidateOptionsResult.Success
                : ValidateOptionsResult.Fail(failures);
        }

        private static void RequirePositive(List<string> failures, string key, TimeSpan value)
        {
            if (value <= TimeSpan.Zero)
            {
                failures.Add($"Authentication configuration error: {key} must be greater than zero.");
            }
        }

        private static void RequireNonNegative(List<string> failures, string key, TimeSpan value)
        {
            if (value < TimeSpan.Zero)
            {
                failures.Add($"Authentication configuration error: {key} must not be negative.");
            }
        }
    }
}