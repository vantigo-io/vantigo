using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;

namespace Vantigo.Configuration;

/// <summary>
/// Configuration for the Brønnøysund Register Centre lookup integration.
/// </summary>
public sealed class BrregLookupOptions
{
    public string BaseUrl { get; set; } = "https://data.brreg.no";
}

public static class BrregLookupConfigurationExtensions
{
    public static IServiceCollection AddBrregLookupOptions(this IServiceCollection services, IConfiguration configuration)
    {
        services.Configure<BrregLookupOptions>(configuration.GetSection("Brreg"));
        return services;
    }
}