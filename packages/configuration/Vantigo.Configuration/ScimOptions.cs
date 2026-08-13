using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Options;

namespace Vantigo.Configuration;

/// <summary>Startup-bound credentials for the deterministic static SCIM scope.</summary>
public sealed record StaticScimOptions(
    bool Enabled,
    string? BearerToken,
    string? PreviousBearerToken,
    DateTimeOffset? PreviousBearerTokenExpiresAtUtc,
    string EndpointPath)
{
    public const string DefaultEndpointPath = "/api/v1/identity/scim/v2";
    public static readonly TimeSpan MaximumPreviousTokenOverlap = TimeSpan.FromHours(24);
    public static StaticScimOptions Disabled { get; } = new(false, null, null, null, DefaultEndpointPath);

    public static StaticScimOptions FromAuthenticationOptions(ScimOptions options)
    {
        if (!options.Enabled)
        {
            if (HasAnyConfiguration(options))
                throw new InvalidOperationException("Authentication:Scim contains credentials but Enabled is false.");
            return Disabled;
        }

        var current = ResolveSecret(options.BearerToken, options.BearerTokenFile, "BearerToken");
        var previous = ResolveSecret(options.PreviousBearerToken, options.PreviousBearerTokenFile, "PreviousBearerToken", optional: true);
        var expiry = options.PreviousBearerTokenExpiresAtUtc;
        if (previous is not null && expiry is null)
            throw new InvalidOperationException("Authentication:Scim previous bearer token requires PreviousBearerTokenExpiresAtUtc.");
        if (previous is not null && expiry <= DateTimeOffset.UtcNow)
            throw new InvalidOperationException("Authentication:Scim PreviousBearerTokenExpiresAtUtc must be in the future.");
        if (previous is not null && expiry!.Value - DateTimeOffset.UtcNow > MaximumPreviousTokenOverlap)
            throw new InvalidOperationException("Authentication:Scim previous bearer token expiry must be no more than 24 hours from startup.");
        if (current is null)
            throw new InvalidOperationException("Authentication:Scim requires a bearer token or readable token file when enabled.");
        if (previous is not null && string.Equals(current, previous, StringComparison.Ordinal))
            throw new InvalidOperationException("Authentication:Scim previous bearer token must differ from the current token.");
        if (previous is null && expiry is not null)
            throw new InvalidOperationException("Authentication:Scim previous token expiry requires a previous bearer token.");
        return new(true, current, previous, expiry, DefaultEndpointPath);
    }

    private static string? ResolveSecret(string? direct, string? file, string name, bool optional = false)
    {
        if (direct is not null && file is not null)
            throw new InvalidOperationException($"Authentication:Scim {name} and its file cannot both be configured.");
        if (direct is not null)
        {
            var normalized = direct.Trim();
            return normalized.Length == 0 || normalized.Any(char.IsWhiteSpace) ? null : normalized;
        }
        if (file is null)
            return optional ? null : throw new InvalidOperationException($"Authentication:Scim {name} is required.");
        if (!Path.IsPathRooted(file) || !File.Exists(file))
            throw new InvalidOperationException($"Authentication:Scim {name}File must be an absolute readable file.");
        var value = File.ReadAllText(file).Trim();
        return value.Length == 0 || value.Any(char.IsWhiteSpace) ? null : value;
    }

    private static bool HasAnyConfiguration(ScimOptions options) =>
        options.BearerToken is not null || options.BearerTokenFile is not null ||
        options.PreviousBearerToken is not null || options.PreviousBearerTokenFile is not null ||
        options.PreviousBearerTokenExpiresAtUtc is not null;
}

public sealed class StaticScimOptionsResolver(IOptions<VantigoAuthenticationOptions> options)
{
    public StaticScimOptions Value { get; } = StaticScimOptions.FromAuthenticationOptions(options.Value.Scim);
}

public static class ScimConfigurationExtensions
{
    public static IServiceCollection AddScimOptions(this IServiceCollection services)
    {
        services.AddSingleton<StaticScimOptionsResolver>();
        services.AddSingleton(serviceProvider => serviceProvider.GetRequiredService<StaticScimOptionsResolver>().Value);
        return services;
    }
}