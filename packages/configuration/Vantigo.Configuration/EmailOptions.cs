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
    public string? Host { get; set; }
    public int Port { get; set; } = 587;
    public string? UserName { get; set; }
    public string? Password { get; set; }
    public bool EnableSsl { get; set; } = true;
    public int TimeoutSeconds { get; set; } = 30;
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
/// invitation and password-reset bearer links into application logs.
/// </summary>
internal sealed class EmailOptionsValidator(IHostEnvironment environment) : IValidateOptions<EmailOptions>
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

        return ValidateOptionsResult.Success;
    }
}