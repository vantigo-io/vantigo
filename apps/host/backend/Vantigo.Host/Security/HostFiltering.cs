using Microsoft.AspNetCore.HostFiltering;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Options;

using Vantigo.Configuration;

namespace Vantigo.Host.Security;

/// <summary>
/// Derives the accepted <c>Host</c> header values from the configured public
/// origin instead of trusting every host, so a request carrying someone else's
/// host header cannot reach the application at all.
/// </summary>
public static class HostFiltering
{
    /// <summary>The configuration key ASP.NET Core reads the host allowlist from.</summary>
    public const string AllowedHostsConfigurationKey = "AllowedHosts";

    /// <summary>
    /// Loopback names that stay allowed whatever the public origin is. Container
    /// and orchestrator health probes reach the application on loopback with the
    /// literal address in the <c>Host</c> header (the chiseled image has no shell,
    /// so the probe is an in-process request to
    /// <c>http://127.0.0.1:8080/health/ready</c>), and host filtering runs before
    /// routing, so it cannot see endpoint metadata and exempt those paths the way
    /// tenancy, antiforgery and rate limiting do. Keeping loopback permitted is
    /// what makes the probe work without teaching this middleware any paths.
    /// </summary>
    public static readonly string[] LoopbackHosts = ["localhost", "127.0.0.1", "[::1]"];

    /// <summary>
    /// Restricts host filtering to the configured <c>App:PublicOrigin</c> plus
    /// loopback. An explicit <c>AllowedHosts</c> setting other than <c>*</c> is
    /// left untouched, and so is the permissive default when no public origin is
    /// configured, because there is nothing to derive an allowlist from.
    /// </summary>
    public static IServiceCollection AddVantigoHostFiltering(this IServiceCollection services, IConfiguration configuration)
    {
        string? configured = configuration[AllowedHostsConfigurationKey]?.Trim();
        if (!string.IsNullOrEmpty(configured) && configured != "*")
        {
            return services;
        }

        services.AddOptions<HostFilteringOptions>()
            .Configure<IOptions<AppPublicOriginOptions>>((options, publicOrigin) =>
            {
                string[] allowedHosts = BuildAllowedHosts(publicOrigin.Value.Normalized);
                if (allowedHosts.Length > 0)
                {
                    options.AllowedHosts = allowedHosts;
                }
            });

        return services;
    }

    /// <summary>
    /// Returns the host allowlist for <paramref name="normalizedPublicOrigin"/>,
    /// or an empty array when no origin is configured.
    /// </summary>
    public static string[] BuildAllowedHosts(string? normalizedPublicOrigin)
    {
        if (string.IsNullOrEmpty(normalizedPublicOrigin) ||
            !Uri.TryCreate(normalizedPublicOrigin, UriKind.Absolute, out Uri? origin))
        {
            return [];
        }

        List<string> allowedHosts = [origin.Host];
        foreach (string loopback in LoopbackHosts)
        {
            if (!allowedHosts.Contains(loopback, StringComparer.OrdinalIgnoreCase))
            {
                allowedHosts.Add(loopback);
            }
        }

        return [.. allowedHosts];
    }
}