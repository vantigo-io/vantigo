using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Options;

namespace Vantigo.Configuration;

/// <summary>
/// Configures the public origin (scheme + host) users reach the app on, via
/// <c>App:PublicOrigin</c> (e.g. <c>https://vantigo.example.com</c>).
/// </summary>
public sealed class AppPublicOriginOptions
{
    /// <summary>
    /// The configuration key holding the public origin.
    /// </summary>
    public const string ConfigurationKey = "App:PublicOrigin";

    /// <summary>
    /// The raw configured public origin.
    /// </summary>
    public string? PublicOrigin { get; set; }

    /// <summary>
    /// Returns the normalized origin (scheme + host, no trailing slash), or
    /// <c>null</c> when unset. Invalid values fail fast: generated email links
    /// silently pointing at the wrong place are worse than a startup error.
    /// </summary>
    /// <exception cref="InvalidOperationException">
    /// The value is not an absolute http(s) origin, or carries a path, query,
    /// fragment, or user info.
    /// </exception>
    public string? Normalized
    {
        get
        {
            var value = PublicOrigin?.Trim();
            if (string.IsNullOrEmpty(value))
            {
                return null;
            }

            if (!Uri.TryCreate(value, UriKind.Absolute, out var uri)
                || (uri.Scheme != Uri.UriSchemeHttp && uri.Scheme != Uri.UriSchemeHttps))
            {
                throw new InvalidOperationException(
                    $"{ConfigurationKey} must be an absolute http(s) origin like https://vantigo.example.com, but was '{PublicOrigin}'.");
            }

            if (uri.AbsolutePath != "/" || !string.IsNullOrEmpty(uri.Query)
                || !string.IsNullOrEmpty(uri.Fragment) || !string.IsNullOrEmpty(uri.UserInfo))
            {
                throw new InvalidOperationException(
                    $"{ConfigurationKey} must be an origin only (scheme + host, no path, query, fragment, or user info), but was '{PublicOrigin}'. The application base path is configured separately via {AppBasePathOptions.ConfigurationKey}.");
            }

            return uri.GetLeftPart(UriPartial.Authority);
        }
    }
}

/// <summary>
/// Fails startup when the public origin is not reachable over TLS outside
/// Development. Session cookies and the bearer links mailed by the invitation
/// and password-recovery workflows are only as confidential as the origin they
/// are issued for, so an http origin there is a credential leak waiting for a
/// misconfigured ingress rather than a cosmetic detail.
/// </summary>
internal sealed class AppPublicOriginOptionsValidator(
    IHostEnvironment environment,
    IOptions<TransportSecurityOptions> transportSecurity) : IValidateOptions<AppPublicOriginOptions>
{
    public ValidateOptionsResult Validate(string? name, AppPublicOriginOptions options)
    {
        string? normalized;
        try
        {
            normalized = options.Normalized;
        }
        catch (InvalidOperationException exception)
        {
            return ValidateOptionsResult.Fail(exception.Message);
        }

        if (normalized is null || environment.IsDevelopment() || transportSecurity.Value.AllowInsecureTransport)
        {
            return ValidateOptionsResult.Success;
        }

        if (!normalized.StartsWith($"{Uri.UriSchemeHttps}://", StringComparison.Ordinal))
        {
            return ValidateOptionsResult.Fail(
                $"Transport security error: {AppPublicOriginOptions.ConfigurationKey} must use https outside " +
                $"Development, but was '{options.PublicOrigin}'. Session cookies and the bearer links mailed for " +
                "invitations and password recovery are derived from this origin, so an http origin exposes them to " +
                $"anyone on the network path. Set {TransportSecurityOptions.ConfigurationSectionName}:" +
                $"{nameof(TransportSecurityOptions.AllowInsecureTransport)}=true to knowingly serve the application " +
                "over plaintext http, for local and evaluation use only.");
        }

        return ValidateOptionsResult.Success;
    }
}

public static class AppPublicOriginConfigurationExtensions
{
    /// <summary>
    /// Registers <see cref="AppPublicOriginOptions"/> from the
    /// <c>App:PublicOrigin</c> configuration value.
    /// </summary>
    public static IServiceCollection AddAppPublicOriginOptions(this IServiceCollection services, IConfiguration configuration)
    {
        services.AddOptions<AppPublicOriginOptions>()
            .Configure(options => options.PublicOrigin = configuration[AppPublicOriginOptions.ConfigurationKey])
            .ValidateOnStart();
        services.AddSingleton<IValidateOptions<AppPublicOriginOptions>, AppPublicOriginOptionsValidator>();
        return services;
    }
}