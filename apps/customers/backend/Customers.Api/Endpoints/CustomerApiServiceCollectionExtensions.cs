using Asp.Versioning;

namespace Vantigo.Customers.Api.Endpoints;

internal static class CustomerApiServiceCollectionExtensions
{
    internal static IServiceCollection AddCustomerApiVersioning(this IServiceCollection services)
    {
        services
            .AddApiVersioning(options =>
            {
                options.DefaultApiVersion = new ApiVersion(1);
                options.ApiVersionReader = new UrlSegmentApiVersionReader();
                options.ReportApiVersions = true;
            })
            .AddApiExplorer(options =>
            {
                // Format the group name as "v1" so the OpenAPI documents are exposed at
                // /openapi/v1.json, matching the default Microsoft.AspNetCore.OpenApi behavior.
                options.GroupNameFormat = "'v'VVV";

                // Replace the {version:apiVersion} route template parameter with the actual
                // version number in the generated OpenAPI paths.
                options.SubstituteApiVersionInUrl = true;
            })
            // Asp.Versioning's AddOpenApi must be used (instead of Microsoft.AspNetCore.OpenApi's)
            // to generate versioned OpenAPI documents.
            .AddOpenApi();

        return services;
    }
}
