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
/// with ASP.NET Core, HttpClient, runtime and Npgsql instrumentation. Each
/// signal's OTLP exporter is registered only when that signal has an endpoint
/// configured — <c>Observability:*</c> configuration keys or the standard
/// <c>OTEL_EXPORTER_OTLP_*</c> environment variables, with signal-specific
/// endpoints overriding the shared one. With no endpoint, telemetry is a
/// silent no-op.
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
        // Resolved straight from configuration: this runs before the container
        // is built, and building a throwaway provider here would leak every
        // singleton registered so far.
        var observabilityOptions = ObservabilityConfigurationExtensions.Resolve(builder.Configuration);

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

                // Each signal gets its own exporter only when that signal has
                // an endpoint (specific or shared); configuring one signal must
                // not silently point the others at exporter defaults.
                if (observabilityOptions.EffectiveTracesEndpoint is { } tracesEndpoint)
                {
                    tracing.AddOtlpExporter(options => options.Endpoint = new Uri(tracesEndpoint));
                }
            })
            .WithMetrics(metrics =>
            {
                metrics
                    .AddAspNetCoreInstrumentation()
                    .AddHttpClientInstrumentation()
                    .AddRuntimeInstrumentation()
                    .AddNpgsqlInstrumentation()
                    // Module meters; registered by name so disabled modules
                    // simply emit nothing.
                    .AddMeter("Vantigo.Communications");

                if (observabilityOptions.EffectiveMetricsEndpoint is { } metricsEndpoint)
                {
                    metrics.AddOtlpExporter(options => options.Endpoint = new Uri(metricsEndpoint));
                }
            })
            .WithLogging(
                logging =>
                {
                    if (observabilityOptions.EffectiveLogsEndpoint is { } logsEndpoint)
                    {
                        logging.AddOtlpExporter(options => options.Endpoint = new Uri(logsEndpoint));
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