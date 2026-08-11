using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;

namespace Vantigo.Configuration;

/// <summary>
/// Application email sender options.
/// </summary>
public sealed class EmailOptions
{
    public string Provider { get; set; } = "Logging";
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
        services.Configure<EmailOptions>(configuration.GetSection("Email"));
        return services;
    }
}