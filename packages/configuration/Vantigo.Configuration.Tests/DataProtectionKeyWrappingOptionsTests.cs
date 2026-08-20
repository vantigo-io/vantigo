using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.FileProviders;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Options;

namespace Vantigo.Configuration.Tests;

public sealed class DataProtectionKeyWrappingOptionsTests
{
    [Theory]
    [InlineData("Production")]
    [InlineData("Staging")]
    public void Unwrapped_OutsideDevelopment_FailsStartupValidation(string environmentName)
    {
        var provider = BuildProvider(environmentName, keyVaultKeyUri: null);

        var exception = Assert.Throws<OptionsValidationException>(
            () => provider.GetRequiredService<IOptions<DataProtectionKeyWrappingOptions>>().Value);

        Assert.Contains("DataProtection:KeyVaultKeyUri is required outside Development", exception.Message, StringComparison.Ordinal);
    }

    [Fact]
    public void Unwrapped_InDevelopment_PassesStartupValidation()
    {
        var provider = BuildProvider("Development", keyVaultKeyUri: null);

        var options = provider.GetRequiredService<IOptions<DataProtectionKeyWrappingOptions>>().Value;

        Assert.Null(options.KeyVaultKeyUri);
    }

    [Fact]
    public void Wrapped_InProduction_PassesStartupValidation()
    {
        const string keyVaultKeyUri = "https://vantigo.vault.azure.net/keys/dataprotection/abc123";
        var provider = BuildProvider("Production", keyVaultKeyUri);

        var options = provider.GetRequiredService<IOptions<DataProtectionKeyWrappingOptions>>().Value;

        Assert.Equal(keyVaultKeyUri, options.KeyVaultKeyUri);
    }

    [Fact]
    public void Whitespace_OnlyKeyVaultKeyUri_OutsideDevelopment_FailsStartupValidation()
    {
        var provider = BuildProvider("Production", "   ");

        Assert.Throws<OptionsValidationException>(
            () => provider.GetRequiredService<IOptions<DataProtectionKeyWrappingOptions>>().Value);
    }

    private static ServiceProvider BuildProvider(string environmentName, string? keyVaultKeyUri)
    {
        var configurationValues = new Dictionary<string, string?>();
        if (keyVaultKeyUri is not null)
        {
            configurationValues["DataProtection:KeyVaultKeyUri"] = keyVaultKeyUri;
        }

        var configuration = new ConfigurationBuilder()
            .AddInMemoryCollection(configurationValues)
            .Build();

        var services = new ServiceCollection();
        services.AddSingleton<IHostEnvironment>(new TestHostEnvironment(environmentName));
        services.AddDataProtectionOptions(configuration);
        return services.BuildServiceProvider();
    }

    private sealed class TestHostEnvironment(string environmentName) : IHostEnvironment
    {
        public string EnvironmentName { get; set; } = environmentName;
        public string ApplicationName { get; set; } = nameof(DataProtectionKeyWrappingOptionsTests);
        public string ContentRootPath { get; set; } = AppContext.BaseDirectory;
        public IFileProvider ContentRootFileProvider { get; set; } = new NullFileProvider();
    }
}