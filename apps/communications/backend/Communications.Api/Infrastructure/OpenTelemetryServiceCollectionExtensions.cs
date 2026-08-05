using System.Reflection;

using Microsoft.Extensions.DependencyInjection;

using OpenTelemetry;
using OpenTelemetry.Resources;

namespace Vantigo.Communications.Api.Infrastructure;

internal static class OpenTelemetryServiceCollectionExtensions
{
    internal static IServiceCollection AddCommunicationsOpenTelemetry(this IServiceCollection services)
    {
        var apiAssembly = typeof(OpenTelemetryServiceCollectionExtensions).Assembly;
        var assemblyName = apiAssembly.GetName();
        var serviceVersion = apiAssembly.GetCustomAttribute<AssemblyInformationalVersionAttribute>()?.InformationalVersion
            ?? assemblyName.Version?.ToString();

        services
            .AddOpenTelemetry()
            .ConfigureResource(resource => resource.AddService(serviceName: assemblyName.Name!, serviceVersion: serviceVersion));

        return services;
    }
}