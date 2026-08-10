using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.Logging;

using Vantigo.Identity.Services;

namespace Vantigo.Identity.Tests.Services;

public sealed class BootstrapSecretProviderTests
{
    [Fact]
    public void MissingSecret_GeneratesUrlSafeHighEntropySecretAndLogsWarning()
    {
        var logger = new RecordingLogger();
        var provider = new BootstrapSecretProvider(new ConfigurationBuilder().Build(), logger);

        Assert.True(provider.Secret.Length >= 43);
        Assert.DoesNotContain(provider.Secret, "+/=");
        var entry = Assert.Single(logger.Entries);
        Assert.Equal(LogLevel.Warning, entry.Level);
        Assert.Contains(provider.Secret, entry.Message, StringComparison.Ordinal);
        Assert.Contains("valid only until setup completes or this process restarts", entry.Message, StringComparison.Ordinal);
    }

    [Fact]
    public void ConfiguredSecret_IsUsedExactlyAndNeverLogged()
    {
        const string configuredSecret = " configured-secret-with-preserved-space ";
        var logger = new RecordingLogger();
        var configuration = new ConfigurationBuilder()
            .AddInMemoryCollection(new Dictionary<string, string?>
            {
                ["Authentication:Bootstrap:Secret"] = configuredSecret,
            })
            .Build();

        var provider = new BootstrapSecretProvider(configuration, logger);

        Assert.Equal(configuredSecret, provider.Secret);
        Assert.Empty(logger.Entries);
    }

    private sealed class RecordingLogger : ILogger<BootstrapSecretProvider>
    {
        public List<Entry> Entries { get; } = [];

        public IDisposable? BeginScope<TState>(TState state) where TState : notnull => null;
        public bool IsEnabled(LogLevel logLevel) => true;
        public void Log<TState>(LogLevel level, EventId eventId, TState state, Exception? exception,
            Func<TState, Exception?, string> formatter) =>
            Entries.Add(new Entry(level, formatter(state, exception)));

        public sealed record Entry(LogLevel Level, string Message);
    }
}