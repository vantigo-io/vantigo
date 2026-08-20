using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Options;

using Vantigo.Configuration;
using Vantigo.Identity.Services;

namespace Vantigo.Identity.Tests.Services;

public sealed class BootstrapSecretProviderTests
{
    [Fact]
    public void MissingSecret_InDevelopment_GeneratesUrlSafeHighEntropySecretAndLogsWarning()
    {
        var logger = new RecordingLogger();
        var provider = new BootstrapSecretProvider(
            Options.Create(new VantigoAuthenticationOptions()),
            new TestHostEnvironment(Environments.Development),
            logger);

        Assert.True(provider.Secret.Length >= 43);
        Assert.DoesNotContain(provider.Secret, "+/=");
        var entry = Assert.Single(logger.Entries);
        Assert.Equal(LogLevel.Warning, entry.Level);
        Assert.Contains(provider.Secret, entry.Message, StringComparison.Ordinal);
        Assert.Contains("valid only until setup completes or this process restarts", entry.Message, StringComparison.Ordinal);
    }

    [Fact]
    public void MissingSecret_OutsideDevelopment_ThrowsAndNeverLogs()
    {
        var logger = new RecordingLogger();

        var exception = Assert.Throws<OptionsValidationException>(() => new BootstrapSecretProvider(
            Options.Create(new VantigoAuthenticationOptions()),
            new TestHostEnvironment(Environments.Production),
            logger));

        Assert.Contains("Bootstrap:Secret is required outside Development", exception.Message, StringComparison.Ordinal);
        Assert.Empty(logger.Entries);
    }

    [Theory]
    [InlineData("Development")]
    [InlineData("Production")]
    public void ConfiguredSecret_IsUsedExactlyAndNeverLogged(string environmentName)
    {
        const string configuredSecret = " configured-secret-with-preserved-space ";
        var options = new VantigoAuthenticationOptions
        {
            Bootstrap = new BootstrapSecretOptions { Secret = configuredSecret },
        };
        var logger = new RecordingLogger();

        var provider = new BootstrapSecretProvider(
            Options.Create(options),
            new TestHostEnvironment(environmentName),
            logger);

        Assert.Equal(configuredSecret, provider.Secret);
        Assert.Empty(logger.Entries);
    }

    private sealed class TestHostEnvironment(string environmentName) : IHostEnvironment
    {
        public string EnvironmentName { get; set; } = environmentName;
        public string ApplicationName { get; set; } = "tests";
        public string ContentRootPath { get; set; } = AppContext.BaseDirectory;
        public Microsoft.Extensions.FileProviders.IFileProvider ContentRootFileProvider { get; set; } =
            new Microsoft.Extensions.FileProviders.NullFileProvider();
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