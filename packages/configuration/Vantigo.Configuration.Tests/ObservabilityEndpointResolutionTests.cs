namespace Vantigo.Configuration.Tests;

public sealed class ObservabilityEndpointResolutionTests
{
    [Fact]
    public void No_endpoints_means_no_exporter_for_any_signal()
    {
        var options = new ObservabilityOptions();

        Assert.Null(options.EffectiveTracesEndpoint);
        Assert.Null(options.EffectiveMetricsEndpoint);
        Assert.Null(options.EffectiveLogsEndpoint);
    }

    [Fact]
    public void The_shared_endpoint_covers_every_signal()
    {
        var options = new ObservabilityOptions { OtlpExporterEndpoint = "http://collector:4317" };

        Assert.Equal("http://collector:4317", options.EffectiveTracesEndpoint);
        Assert.Equal("http://collector:4317", options.EffectiveMetricsEndpoint);
        Assert.Equal("http://collector:4317", options.EffectiveLogsEndpoint);
    }

    [Fact]
    public void A_signal_specific_endpoint_overrides_the_shared_one_only_for_that_signal()
    {
        var options = new ObservabilityOptions
        {
            OtlpExporterEndpoint = "http://collector:4317",
            OtlpExporterMetricsEndpoint = "http://metrics-collector:4318",
        };

        Assert.Equal("http://collector:4317", options.EffectiveTracesEndpoint);
        Assert.Equal("http://metrics-collector:4318", options.EffectiveMetricsEndpoint);
        Assert.Equal("http://collector:4317", options.EffectiveLogsEndpoint);
    }

    [Fact]
    public void A_signal_specific_endpoint_alone_enables_only_that_signal()
    {
        var options = new ObservabilityOptions { OtlpExporterTracesEndpoint = "http://traces:4318" };

        Assert.Equal("http://traces:4318", options.EffectiveTracesEndpoint);
        Assert.Null(options.EffectiveMetricsEndpoint);
        Assert.Null(options.EffectiveLogsEndpoint);
    }
}