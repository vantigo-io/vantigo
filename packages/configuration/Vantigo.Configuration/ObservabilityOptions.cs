namespace Vantigo.Configuration;

using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;

/// <summary>
/// OpenTelemetry exporter configuration. Mirror of the standard environment
/// variables used by the OTLP exporter; set via configuration when preferred.
/// </summary>
public sealed class ObservabilityOptions
{
    /// <summary>
    /// Shared endpoint for all telemetry signals. Falls back to signal-specific
    /// endpoints when absent.
    /// </summary>
    public string? OtlpExporterEndpoint { get; set; }

    public string? OtlpExporterTracesEndpoint { get; set; }

    public string? OtlpExporterMetricsEndpoint { get; set; }

    public string? OtlpExporterLogsEndpoint { get; set; }

    /// <summary>
    /// True when any standard OTLP endpoint is configured.
    /// </summary>
    public bool HasAnyOtlpEndpoint =>
        !string.IsNullOrWhiteSpace(OtlpExporterEndpoint)
        || !string.IsNullOrWhiteSpace(OtlpExporterTracesEndpoint)
        || !string.IsNullOrWhiteSpace(OtlpExporterMetricsEndpoint)
        || !string.IsNullOrWhiteSpace(OtlpExporterLogsEndpoint);
}

public static class ObservabilityConfigurationExtensions
{
    /// <summary>
    /// Registers <see cref="ObservabilityOptions"/> from the
    /// <c>Observability</c> configuration section, with automatic fallback to
    /// the standard <c>OTEL_EXPORTER_OTLP_*</c> environment variables.
    /// </summary>
    public static IServiceCollection AddObservabilityOptions(this IServiceCollection services, IConfiguration configuration)
    {
        services.Configure<ObservabilityOptions>(options =>
        {
            options.OtlpExporterEndpoint = Read(configuration, "Observability:OtlpExporterEndpoint", "OTEL_EXPORTER_OTLP_ENDPOINT");
            options.OtlpExporterTracesEndpoint = Read(configuration, "Observability:OtlpExporterTracesEndpoint", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT");
            options.OtlpExporterMetricsEndpoint = Read(configuration, "Observability:OtlpExporterMetricsEndpoint", "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT");
            options.OtlpExporterLogsEndpoint = Read(configuration, "Observability:OtlpExporterLogsEndpoint", "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT");
        });
        return services;
    }

    private static string? Read(IConfiguration configuration, string configKey, string environmentKey)
    {
        var value = configuration[configKey];
        if (!string.IsNullOrWhiteSpace(value))
        {
            return value;
        }

        value = Environment.GetEnvironmentVariable(environmentKey);
        if (!string.IsNullOrWhiteSpace(value))
        {
            return value;
        }

        return null;
    }
}