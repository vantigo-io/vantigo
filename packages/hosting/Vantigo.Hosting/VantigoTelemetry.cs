using System.Reflection;

using Microsoft.AspNetCore.Builder;
using Microsoft.AspNetCore.Http;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;

using Npgsql;

using OpenTelemetry.Logs;
using OpenTelemetry.Metrics;
using OpenTelemetry.Resources;
using OpenTelemetry.Trace;

namespace Vantigo.Hosting;

/// <summary>
/// Shared OpenTelemetry setup for all Vantigo backends: traces, metrics and
/// logs with ASP.NET Core, HttpClient, runtime and Npgsql instrumentation.
/// The OTLP exporter is driven entirely by the standard
/// <c>OTEL_EXPORTER_OTLP_*</c> environment variables (set automatically by
/// Aspire's <c>WithOtlpExporter()</c> in development); when no endpoint is
/// configured, no exporter is registered and telemetry is a silent no-op.
/// </summary>
public static class VantigoTelemetry
{
    /// <summary>
    /// Adds the shared telemetry pipeline. <paramref name="serviceName"/> is the
    /// short application name (e.g. <c>"customers"</c>), reported as
    /// <c>vantigo-customers</c>; names already starting with <c>vantigo-</c> are
    /// used as-is. Extra app-specific <see cref="System.Diagnostics.ActivitySource"/>
    /// or <see cref="System.Diagnostics.Metrics.Meter"/> names can be passed via
    /// the optional parameters.
    /// </summary>
    public static WebApplicationBuilder AddVantigoTelemetry(
        this WebApplicationBuilder builder,
        string serviceName,
        string[]? additionalActivitySources = null,
        string[]? additionalMeters = null)
    {
        var resolvedServiceName = ResolveServiceName(serviceName);
        var serviceVersion = ResolveServiceVersion(Assembly.GetEntryAssembly());
        var exporterConfigured = HasOtlpExporterEndpoint(builder.Configuration);

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

                if (additionalActivitySources is { Length: > 0 })
                {
                    tracing.AddSource(additionalActivitySources);
                }

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

                if (additionalMeters is { Length: > 0 })
                {
                    metrics.AddMeter(additionalMeters);
                }

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
    /// Normalizes the short application name to the reported service name:
    /// <c>"customers"</c> becomes <c>vantigo-customers</c>, while names already
    /// prefixed with <c>vantigo-</c> pass through unchanged.
    /// </summary>
    public static string ResolveServiceName(string serviceName)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(serviceName);
        var trimmed = serviceName.Trim();
        return trimmed.StartsWith("vantigo-", StringComparison.Ordinal) ? trimmed : $"vantigo-{trimmed}";
    }

    /// <summary>
    /// Resolves the reported service version: the assembly informational version
    /// (produced by GitVersion) when present, otherwise the plain assembly version.
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
    /// True when any standard OTLP endpoint variable is configured
    /// (<c>OTEL_EXPORTER_OTLP_ENDPOINT</c> or a signal-specific variant). Without
    /// one, no exporter is registered, keeping telemetry a warning-free no-op.
    /// </summary>
    public static bool HasOtlpExporterEndpoint(IConfiguration configuration) =>
        !string.IsNullOrWhiteSpace(configuration["OTEL_EXPORTER_OTLP_ENDPOINT"])
        || !string.IsNullOrWhiteSpace(configuration["OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"])
        || !string.IsNullOrWhiteSpace(configuration["OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"])
        || !string.IsNullOrWhiteSpace(configuration["OTEL_EXPORTER_OTLP_LOGS_ENDPOINT"]);

    /// <summary>
    /// Filters low-value server spans: OpenAPI documents, the SPA entry point
    /// ("/", "/index.html") and static assets (any path with a file extension,
    /// e.g. .js/.css/.svg) outside /api and /auth, which are always traced.
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

        // Static assets carry a file extension in the last segment (e.g. .js,
        // .css, .svg, .woff2). Extensionless paths are SPA deep links or future
        // endpoints and stay traced.
        var lastSlash = value.LastIndexOf('/');
        return value.IndexOf('.', lastSlash + 1) >= 0;
    }
}