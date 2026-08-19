using Asp.Versioning;

using Microsoft.Extensions.DependencyInjection;

namespace Vantigo.Configuration;

/// <summary>
/// Registers the app-wide API versioning configuration used by Vantigo modules.
/// </summary>
public static class ApiVersioningServiceCollectionExtensions
{
    /// <summary>
    /// Registers the shared Vantigo API versioning configuration used by the backend modules.
    /// </summary>
    public static IServiceCollection AddVantigoApiVersioning(this IServiceCollection services)
    {
        services.AddApiVersioning(options =>
        {
            options.DefaultApiVersion = new ApiVersion(1);
            options.ApiVersionReader = new UrlSegmentApiVersionReader();
            options.ReportApiVersions = true;
        })
        .AddApiExplorer(options =>
        {
            options.GroupNameFormat = "'v'VVV";
            options.SubstituteApiVersionInUrl = true;
        })
        .AddOpenApi();

        return services;
    }
}