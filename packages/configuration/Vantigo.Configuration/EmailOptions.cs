using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Options;

namespace Vantigo.Configuration;

/// <summary>
/// Application email sender options.
/// </summary>
public sealed class EmailOptions
{
    public const string LoggingProvider = "Logging";
    public const string SmtpProvider = "Smtp";

    public string Provider { get; set; } = LoggingProvider;
    public string From { get; set; } = "no-reply@localhost";
    public SmtpEmailOptions Smtp { get; set; } = new();
}

public sealed class SmtpEmailOptions
{
    /// <summary>The submission port that speaks TLS from the first byte.</summary>
    public const int ImplicitTlsPort = 465;

    public string? Host { get; set; }
    public int Port { get; set; } = 587;
    public string? UserName { get; set; }
    public string? Password { get; set; }
    public bool EnableSsl { get; set; } = true;

    /// <summary>
    /// Escape hatch for a local mail catcher that speaks no TLS at all. It is
    /// rejected outside Development unless
    /// <see cref="TransportSecurityOptions.AllowInsecureTransport"/> is also set,
    /// because invitation and password-reset mails carry bearer links.
    /// </summary>
    public bool AllowInsecurePlaintext { get; set; }

    public int TimeoutSeconds { get; set; } = 30;

    /// <summary>
    /// Whether the configured endpoint negotiates TLS: STARTTLS when
    /// <see cref="EnableSsl"/> is set, implicit TLS on
    /// <see cref="ImplicitTlsPort"/>.
    /// </summary>
    public bool UsesTls => EnableSsl || Port == ImplicitTlsPort;
}

public static class EmailConfigurationExtensions
{
    public static IServiceCollection AddEmailOptions(this IServiceCollection services, IConfiguration configuration)
    {
        services.AddOptions<EmailOptions>()
            .Bind(configuration.GetSection("Email"))
            .ValidateOnStart();
        services.AddSingleton<IValidateOptions<EmailOptions>, EmailOptionsValidator>();
        return services;
    }
}

/// <summary>
/// Rejects the Logging provider outside Development. The Logging provider exists
/// only as a local developer convenience; it must never be the effective provider
/// in a deployed environment, since a misconfiguration there would silently route
/// invitation and password-reset bearer links into application logs. It also
/// requires SMTP to negotiate TLS, for the same reason: those bearer links are
/// the credential.
/// </summary>
internal sealed class EmailOptionsValidator(
    IHostEnvironment environment,
    IOptions<TransportSecurityOptions> transportSecurity) : IValidateOptions<EmailOptions>
{
    public ValidateOptionsResult Validate(string? name, EmailOptions options)
    {
        if (!environment.IsDevelopment() &&
            string.Equals(options.Provider, EmailOptions.LoggingProvider, StringComparison.OrdinalIgnoreCase))
        {
            return ValidateOptionsResult.Fail(
                $"Email configuration error: Provider must not be {EmailOptions.LoggingProvider} outside " +
                "Development. Configure Email:Provider=Smtp (or another real provider); the Logging provider " +
                "writes message delivery details and must not run where it could ever see production traffic.");
        }

        bool plaintextPermitted = options.Smtp.AllowInsecurePlaintext &&
            (environment.IsDevelopment() || transportSecurity.Value.AllowInsecureTransport);
        if (string.Equals(options.Provider, EmailOptions.SmtpProvider, StringComparison.OrdinalIgnoreCase) &&
            !options.Smtp.UsesTls && !plaintextPermitted)
        {
            return ValidateOptionsResult.Fail(
                "Transport security error: Email:Smtp must negotiate TLS. Set Email:Smtp:EnableSsl=true for " +
                $"STARTTLS, or use port {SmtpEmailOptions.ImplicitTlsPort} for implicit TLS. Invitation and " +
                "password-reset mails carry bearer links, so plaintext submission hands them to anyone on the " +
                "network path; it requires Email:Smtp:AllowInsecurePlaintext=true and either the Development " +
                $"environment or {TransportSecurityOptions.ConfigurationSectionName}:" +
                $"{nameof(TransportSecurityOptions.AllowInsecureTransport)}=true.");
        }

        return ValidateOptionsResult.Success;
    }
}