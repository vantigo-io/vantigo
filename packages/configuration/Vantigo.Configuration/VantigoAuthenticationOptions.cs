using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;

namespace Vantigo.Configuration;

/// <summary>
/// Shared authentication configuration.
/// </summary>
public sealed class VantigoAuthenticationOptions
{
    public OwnerAuthenticationOptions Owners { get; set; } = new();

    public BootstrapSecretOptions Bootstrap { get; set; } = new();

    public InvitationOptions Invitations { get; set; } = new();

    public PasswordResetOptions PasswordReset { get; set; } = new();

    public OidcOptions Oidc { get; set; } = new();
}

public sealed class OwnerAuthenticationOptions
{
    public bool RequireMfa { get; set; }
    public string MfaIssuer { get; set; } = "Vantigo";
}

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
    public string? Authority { get; set; }
    public string? ClientId { get; set; }
    public string? ClientSecret { get; set; }
    public string? DisplayName { get; set; }
    public string? CallbackPath { get; set; }
}

public static class VantigoAuthenticationConfigurationExtensions
{
    public static IServiceCollection AddVantigoAuthenticationOptions(this IServiceCollection services, IConfiguration configuration)
    {
        services.Configure<VantigoAuthenticationOptions>(configuration.GetSection("Authentication"));
        return services;
    }
}