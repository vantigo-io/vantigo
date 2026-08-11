using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;

namespace Vantigo.Configuration;

/// <summary>
/// Development-only seed configuration.
/// </summary>
public sealed class DevelopmentSeedOptions
{
    public bool Enabled { get; set; } = true;

    public DevelopmentSeedAdminOptions Admin { get; set; } = new();

    public DevelopmentSeedDataOptions Data { get; set; } = new();
}

public sealed class DevelopmentSeedAdminOptions
{
    public string? Email { get; set; }
    public string? DisplayName { get; set; }
    public string? Password { get; set; }
}

public sealed class DevelopmentSeedDataOptions
{
    public int Customers { get; set; } = 50;
    public int Contacts { get; set; } = 150;
    public int Messages { get; set; } = 50;
    public int Products { get; set; } = 100;
}

public static class DevelopmentSeedConfigurationExtensions
{
    public static IServiceCollection AddDevelopmentSeedOptions(this IServiceCollection services, IConfiguration configuration)
    {
        services.Configure<DevelopmentSeedOptions>(configuration.GetSection("Development:Seed"));
        return services;
    }
}