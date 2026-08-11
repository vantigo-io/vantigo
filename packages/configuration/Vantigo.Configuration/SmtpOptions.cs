using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;

namespace Vantigo.Configuration;

/// <summary>
/// SMTP options used by the communications module's default delivery provider.
/// </summary>
public sealed class SmtpOptions
{
    public string? Host { get; set; }
    public int Port { get; set; } = 587;
    public string? Username { get; set; }
    public string? Password { get; set; }
    public bool UseSsl { get; set; } = true;
    public bool AllowInsecurePlaintext { get; set; }
    public int TimeoutSeconds { get; set; } = 30;
}

public static class SmtpConfigurationExtensions
{
    public static IServiceCollection AddSmtpOptions(this IServiceCollection services, IConfiguration configuration)
    {
        services.Configure<SmtpOptions>(configuration.GetSection("Smtp"));
        return services;
    }
}