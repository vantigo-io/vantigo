using Asp.Versioning;

namespace Vantigo.Energy.Endpoints;

internal static class EnergyApiServiceCollectionExtensions
{
    internal static IServiceCollection AddEnergyApiVersioning(this IServiceCollection services)
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