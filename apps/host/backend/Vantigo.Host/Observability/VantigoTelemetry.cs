using System.Reflection;

using Microsoft.AspNetCore.Builder;
using Microsoft.AspNetCore.Http;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Options;

using Npgsql;

using OpenTelemetry.Logs;
using OpenTelemetry.Metrics;
using OpenTelemetry.Resources;
using OpenTelemetry.Trace;

using Vantigo.Configuration;

namespace Vantigo.Host;

/// <summary>
/// Shared OpenTelemetry setup for the Vantigo host: traces, metrics and logs
/// with ASP.NET Core, HttpClient, runtime and Npgsql instrumentation. The OTLP
/// exporter is driven entirely by the standard <c>OTEL_EXPORTER_OTLP_*</c>
/// environment variables; when no endpoint is configured, no exporter is
/// registered and telemetry is a silent no-op.
/// </summary>
public static class VantigoTelemetry
{
    /// <summary>
    /// Adds the shared telemetry pipeline. <paramref name="serviceName"/> is the
    /// short application name (e.g. <c>"vantigo"</c>), reported as
    /// <c>vantigo-vantigo</c>; names already starting with <c>vantigo-</c> are
    /// used as-is.
    /// </summary>
    public static WebApplicationBuilder AddVantigoTelemetry(
        this WebApplicationBuilder builder,
        string serviceName)
    {
        var resolvedServiceName = ResolveServiceName(serviceName);
        var serviceVersion = ResolveServiceVersion(Assembly.GetEntryAssembly());
        var observabilityOptions = builder.Services
            .BuildServiceProvider()
            .GetRequiredService<IOptions<ObservabilityOptions>>()
            .Value;
        var exporterConfigured = observabilityOptions.HasAnyOtlpEndpoint;

        builder.Services
            .AddOpenTelemetry()
            .ConfigureResource(resource => resource
                .AddService(serviceName: resolvedServiceName, serviceVersion: serviceVersion)
                .AddAttributes(new Dictionary<string, object>
                {
                    ["deployment.environment.name"] = builder.Environment.EnvironmentName,
                }))
            .WithTracing(tracing =>
            {
                tracing
                    .AddAspNetCoreInstrumentation(options =>
                        options.Filter = context => !IsNoiseRequestPath(context.Request.Path))
                    .AddHttpClientInstrumentation()
                    .AddNpgsql();

                if (exporterConfigured)
                {
                    tracing.AddOtlpExporter();
                }
            })
            .WithMetrics(metrics =>
            {
                metrics
                    .AddAspNetCoreInstrumentation()
                    .AddHttpClientInstrumentation()
                    .AddRuntimeInstrumentation()
                    .AddNpgsqlInstrumentation();

                if (exporterConfigured)
                {
                    metrics.AddOtlpExporter();
                }
            })
            .WithLogging(
                logging =>
                {
                    if (exporterConfigured)
                    {
                        logging.AddOtlpExporter();
                    }
                },
                options =>
                {
                    options.IncludeScopes = true;
                    options.IncludeFormattedMessage = true;
                });

        return builder;
    }

    /// <summary>
    /// Normalizes the short application name to the reported service name.
    /// </summary>
    public static string ResolveServiceName(string serviceName)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(serviceName);
        var trimmed = serviceName.Trim();
        return trimmed.StartsWith("vantigo-", StringComparison.Ordinal) ? trimmed : $"vantigo-{trimmed}";
    }

    /// <summary>
    /// Resolves the reported service version.
    /// </summary>
    public static string? ResolveServiceVersion(Assembly? assembly)
    {
        if (assembly is null)
        {
            return null;
        }

        return assembly.GetCustomAttribute<AssemblyInformationalVersionAttribute>()?.InformationalVersion
            ?? assembly.GetName().Version?.ToString();
    }

    /// <summary>
    /// True when any standard OTLP endpoint variable is configured.
    /// </summary>
    [Obsolete("Use IOptions<ObservabilityOptions>.Value.HasAnyOtlpEndpoint instead.")]
    public static bool HasOtlpExporterEndpoint(IConfiguration configuration) =>
        !string.IsNullOrWhiteSpace(configuration["OTEL_EXPORTER_OTLP_ENDPOINT"])
        || !string.IsNullOrWhiteSpace(configuration["OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"])
        || !string.IsNullOrWhiteSpace(configuration["OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"])
        || !string.IsNullOrWhiteSpace(configuration["OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"]);

    /// <summary>
    /// Filters low-value server spans: OpenAPI documents, the SPA entry point
    /// and static assets outside /api and /auth, which are always traced.
    /// </summary>
    public static bool IsNoiseRequestPath(PathString path)
    {
        var value = path.Value;
        if (string.IsNullOrEmpty(value) || value == "/")
        {
            return true;
        }

        if (value.StartsWith("/api", StringComparison.OrdinalIgnoreCase)
            || value.StartsWith("/auth", StringComparison.OrdinalIgnoreCase))
        {
            return false;
        }

        if (value.StartsWith("/openapi", StringComparison.OrdinalIgnoreCase))
        {
            return true;
        }

        // Static assets carry a file extension in the last segment.
        var lastSlash = value.LastIndexOf('/');
        return value.IndexOf('.', lastSlash + 1) >= 0;
    }
}