using System.Data.Common;

using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;

namespace Vantigo.Configuration;

/// <summary>
/// Transport-security policy read from the <c>Security</c> configuration section.
/// Outside Development the application fails closed on plaintext transport: the
/// public origin must be https, PostgreSQL must present a certificate the client
/// verifies, and SMTP must negotiate TLS.
/// </summary>
public sealed class TransportSecurityOptions
{
    public const string ConfigurationSectionName = "Security";

    /// <summary>
    /// Escape hatch that allows plaintext transport (an http public origin, a
    /// PostgreSQL connection without certificate-verified TLS, and plaintext
    /// SMTP) outside the Development environment. It exists for local stacks and
    /// evaluation deployments such as the bundled Docker Compose stack, where
    /// everything runs on a private bridge network and no certificate authority
    /// is available. Anything reachable from a network you do not control must
    /// leave this false: with it enabled, session cookies, bearer links in
    /// invitation and password-reset mails, and database credentials all travel
    /// in the clear.
    /// </summary>
    public bool AllowInsecureTransport { get; set; }

    /// <summary>
    /// Emits the browser content security policy as
    /// <c>Content-Security-Policy-Report-Only</c> instead of enforcing it. Use
    /// this to stage a policy change against a customized frontend before
    /// enforcing it; it disables the protection while set.
    /// </summary>
    public bool ContentSecurityPolicyReportOnly { get; set; }

    /// <summary>
    /// Returns an operator-facing error message when
    /// <paramref name="connectionString"/> does not require certificate-verified
    /// PostgreSQL TLS, or <c>null</c> when it does (or is empty).
    /// </summary>
    /// <remarks>
    /// Npgsql defaults to <c>SSL Mode=Prefer</c>, which silently falls back to
    /// plaintext when the server declines TLS and never authenticates the server
    /// even when it accepts. Only <c>VerifyCA</c> and <c>VerifyFull</c> validate
    /// the certificate chain, and <c>Trust Server Certificate=true</c> turns that
    /// validation back off.
    /// </remarks>
    public static string? DescribeInsecurePostgresTransport(string? connectionString, string configurationKey)
    {
        if (string.IsNullOrWhiteSpace(connectionString))
        {
            return null;
        }

        DbConnectionStringBuilder parsed = new();
        try
        {
            parsed.ConnectionString = connectionString;
        }
        catch (ArgumentException)
        {
            // A connection string this parser rejects is reported by the database
            // provider with a far better message than anything produced here.
            return null;
        }

        string? sslMode = null;
        bool trustServerCertificate = false;
        foreach (string key in parsed.Keys.Cast<string>())
        {
            string normalizedKey = NormalizeToken(key);
            string normalizedValue = NormalizeToken(parsed[key] as string ?? parsed[key]?.ToString());
            if (normalizedKey == "sslmode")
            {
                sslMode = normalizedValue;
            }
            else if (normalizedKey == "trustservercertificate")
            {
                trustServerCertificate = normalizedValue == "true";
            }
        }

        if (sslMode is not ("verifyca" or "verifyfull"))
        {
            return $"Transport security error: {configurationKey} must require certificate-verified PostgreSQL TLS " +
                "outside Development. Add 'SSL Mode=VerifyFull' (or VerifyCA when the server certificate does not " +
                "carry the connection host name) so the application refuses to talk to a database server it cannot " +
                $"authenticate; the Npgsql default of 'Prefer' silently accepts plaintext. Set " +
                $"{ConfigurationSectionName}:{nameof(AllowInsecureTransport)}=true to knowingly accept an " +
                "unauthenticated database connection, for local and evaluation use only.";
        }

        if (trustServerCertificate)
        {
            return $"Transport security error: {configurationKey} sets 'Trust Server Certificate=true', which " +
                "disables the certificate validation that VerifyCA and VerifyFull otherwise perform. Remove it and " +
                "point 'Root Certificate' at the issuing authority instead. Set " +
                $"{ConfigurationSectionName}:{nameof(AllowInsecureTransport)}=true to knowingly accept an " +
                "unauthenticated database connection, for local and evaluation use only.";
        }

        return null;
    }

    private static string NormalizeToken(string? value)
    {
        if (string.IsNullOrEmpty(value))
        {
            return string.Empty;
        }

        Span<char> buffer = stackalloc char[value.Length];
        int length = 0;
        foreach (char character in value)
        {
            if (character is ' ' or '-' or '_')
            {
                continue;
            }

            buffer[length++] = char.ToLowerInvariant(character);
        }

        return new string(buffer[..length]);
    }
}

public static class TransportSecurityConfigurationExtensions
{
    /// <summary>
    /// Registers <see cref="TransportSecurityOptions"/> from the
    /// <c>Security</c> configuration section.
    /// </summary>
    public static IServiceCollection AddTransportSecurityOptions(this IServiceCollection services, IConfiguration configuration)
    {
        services.Configure<TransportSecurityOptions>(
            configuration.GetSection(TransportSecurityOptions.ConfigurationSectionName));
        return services;
    }
}