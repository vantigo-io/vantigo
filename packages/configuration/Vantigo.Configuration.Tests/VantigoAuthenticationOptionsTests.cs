using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Options;

namespace Vantigo.Configuration.Tests;

public sealed class VantigoAuthenticationOptionsTests
{
    [Fact]
    public void MissingBootstrapSecret_OutsideDevelopment_FailsOptionsValidation()
    {
        using var provider = BuildProvider(Environments.Production);

        var exception = Assert.Throws<OptionsValidationException>(() =>
            provider.GetRequiredService<IOptions<VantigoAuthenticationOptions>>().Value);

        Assert.Contains("Bootstrap:Secret is required outside Development", exception.Message, StringComparison.Ordinal);
    }

    [Fact]
    public void MissingBootstrapSecret_InDevelopment_PassesOptionsValidation()
    {
        using var provider = BuildProvider(Environments.Development);

        var options = provider.GetRequiredService<IOptions<VantigoAuthenticationOptions>>().Value;

        Assert.True(string.IsNullOrEmpty(options.Bootstrap.Secret));
    }

    [Fact]
    public void ConfiguredBootstrapSecret_OutsideDevelopment_PassesOptionsValidation()
    {
        using var provider = BuildProvider(Environments.Production, ("Authentication:Bootstrap:Secret", "configured-secret"));

        var options = provider.GetRequiredService<IOptions<VantigoAuthenticationOptions>>().Value;

        Assert.Equal("configured-secret", options.Bootstrap.Secret);
    }

    private static ServiceProvider BuildProvider(string environmentName, params (string Key, string? Value)[] settings)
    {
        var configuration = new ConfigurationBuilder().AddInMemoryCollection(
            settings.ToDictionary(setting => setting.Key, setting => setting.Value)).Build();
        var services = new ServiceCollection();
        services.AddSingleton<IHostEnvironment>(new TestHostEnvironment(environmentName));
        services.AddVantigoAuthenticationOptions(configuration);
        return services.BuildServiceProvider();
    }

    private sealed class TestHostEnvironment(string environmentName) : IHostEnvironment
    {
        public string EnvironmentName { get; set; } = environmentName;
        public string ApplicationName { get; set; } = "tests";
        public string ContentRootPath { get; set; } = AppContext.BaseDirectory;
        public Microsoft.Extensions.FileProviders.IFileProvider ContentRootFileProvider { get; set; } =
            new Microsoft.Extensions.FileProviders.NullFileProvider();
    }
}