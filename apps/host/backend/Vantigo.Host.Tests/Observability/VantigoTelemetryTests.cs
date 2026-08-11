using System.Reflection;

using Microsoft.Extensions.Configuration;

namespace Vantigo.Host.Tests.Observability;

public class VantigoTelemetryTests
{
    [Theory]
    [InlineData("customers", "vantigo-customers")]
    [InlineData("communications", "vantigo-communications")]
    [InlineData(" customers ", "vantigo-customers")]
    [InlineData("vantigo-customers", "vantigo-customers")]
    public void ResolveServiceName_prefixes_short_names(string input, string expected) =>
        Assert.Equal(expected, VantigoTelemetry.ResolveServiceName(input));

    [Theory]
    [InlineData("")]
    [InlineData("   ")]
    public void ResolveServiceName_rejects_blank_names(string input) =>
        Assert.ThrowsAny<ArgumentException>(() => VantigoTelemetry.ResolveServiceName(input));

    [Fact]
    public void ResolveServiceName_rejects_null() =>
        Assert.Throws<ArgumentNullException>(() => VantigoTelemetry.ResolveServiceName(null!));

    [Fact]
    public void ResolveServiceVersion_prefers_informational_version()
    {
        var assembly = typeof(VantigoTelemetryTests).Assembly;
        var expected = assembly.GetCustomAttribute<AssemblyInformationalVersionAttribute>()!.InformationalVersion;

        Assert.Equal(expected, VantigoTelemetry.ResolveServiceVersion(assembly));
    }

    [Fact]
    public void ResolveServiceVersion_returns_null_for_null_assembly() =>
        Assert.Null(VantigoTelemetry.ResolveServiceVersion(null));

    [Theory]
    [InlineData("OTEL_EXPORTER_OTLP_ENDPOINT")]
    [InlineData("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")]
    [InlineData("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT")]
    [InlineData("OTEL_EXPORTER_OTLP_LOGS_ENDPOINT")]
    public void HasOtlpExporterEndpoint_detects_standard_variables(string key)
    {
        var configuration = new ConfigurationBuilder()
            .AddInMemoryCollection(new Dictionary<string, string?> { [key] = "http://localhost:4317" })
            .Build();

        Assert.True(VantigoTelemetry.HasOtlpExporterEndpoint(configuration));
    }

    [Fact]
    public void HasOtlpExporterEndpoint_is_false_without_endpoint()
    {
        var configuration = new ConfigurationBuilder()
            .AddInMemoryCollection(new Dictionary<string, string?> { ["OTEL_EXPORTER_OTLP_ENDPOINT"] = " " })
            .Build();

        Assert.False(VantigoTelemetry.HasOtlpExporterEndpoint(configuration));
    }

    [Theory]
    [InlineData("/")]
    [InlineData("")]
    [InlineData("/index.html")]
    [InlineData("/openapi/v1.json")]
    [InlineData("/assets/index-abc123.js")]
    [InlineData("/customers/assets/style.css")]
    [InlineData("/favicon.svg")]
    [InlineData("/fonts/inter.woff2")]
    public void IsNoiseRequestPath_filters_openapi_and_static_assets(string path) =>
        Assert.True(VantigoTelemetry.IsNoiseRequestPath(path));

    [Theory]
    [InlineData("/api/v1/businesses")]
    [InlineData("/api/v1/files/report.pdf")]
    [InlineData("/api/v1/identity/login")]
    [InlineData("/customers/businesses/42")]
    [InlineData("/some-future-endpoint")]
    public void IsNoiseRequestPath_keeps_api_auth_and_deep_links(string path) =>
        Assert.False(VantigoTelemetry.IsNoiseRequestPath(path));
}