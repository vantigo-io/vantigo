using Asp.Versioning;

namespace Vantigo.Communications.Endpoints;

internal static class ApiVersioningExtensions
{
    public static IServiceCollection AddCommunicationsModuleVersioning(this IServiceCollection services)
    {
        services.AddApiVersioning(options =>
        {
            options.DefaultApiVersion = new ApiVersion(1);
            options.ApiVersionReader = new UrlSegmentApiVersionReader();
            options.ReportApiVersions = true;
        }).AddApiExplorer(options =>
        {
            options.GroupNameFormat = "'v'VVV";
            options.SubstituteApiVersionInUrl = true;
        }).AddOpenApi();
        return services;
    }
}