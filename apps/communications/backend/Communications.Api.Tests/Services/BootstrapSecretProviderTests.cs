using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Logging.Abstractions;

using Vantigo.Communications.Api.Services;

namespace Vantigo.Communications.Api.Tests.Services;

public sealed class BootstrapSecretProviderTests
{
    [Fact]
    public void Missing_secret_is_rejected_outside_development()
    {
        var configuration = new ConfigurationBuilder().Build();
        var environment = new TestHostEnvironment { EnvironmentName = Environments.Production };

        var exception = Assert.Throws<InvalidOperationException>(() => new BootstrapSecretProvider(configuration, environment, NullLogger<BootstrapSecretProvider>.Instance));

        Assert.Contains("Authentication:Bootstrap:Secret", exception.Message, StringComparison.Ordinal);
    }

    [Fact]
    public void Missing_secret_generates_in_development()
    {
        var configuration = new ConfigurationBuilder().Build();
        var environment = new TestHostEnvironment { EnvironmentName = Environments.Development };

        var provider = new BootstrapSecretProvider(configuration, environment, NullLogger<BootstrapSecretProvider>.Instance);

        Assert.NotEmpty(provider.Secret);
    }

    private sealed class TestHostEnvironment : IHostEnvironment
    {
        public string EnvironmentName { get; set; } = string.Empty;
        public string ApplicationName { get; set; } = "tests";
        public string ContentRootPath { get; set; } = AppContext.BaseDirectory;
        public Microsoft.Extensions.FileProviders.IFileProvider ContentRootFileProvider { get; set; } = new Microsoft.Extensions.FileProviders.NullFileProvider();
    }
}