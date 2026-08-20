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
}