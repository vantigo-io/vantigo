using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.FileProviders;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Options;

namespace Vantigo.Configuration.Tests;

public sealed class DataProtectionKeyWrappingOptionsTests
{
    [Fact]
    public void Validate_AllowsUnwrappedKeysInDevelopment()
    {
        DataProtectionKeyWrappingOptions options = new();

        options.Validate(isDevelopment: true);
    }

    [Fact]
    public void Validate_RejectsUnwrappedKeysOutsideDevelopmentWithoutTheEscapeHatch()
    {
        DataProtectionKeyWrappingOptions options = new();

        InvalidOperationException exception = Assert.Throws<InvalidOperationException>(
            () => options.Validate(isDevelopment: false));

        Assert.Contains("not production-ready", exception.Message, StringComparison.Ordinal);
        Assert.Contains("DataProtection:KeyVaultKeyUri", exception.Message, StringComparison.Ordinal);
        Assert.Contains("DataProtection:AllowUnwrappedKeys", exception.Message, StringComparison.Ordinal);
    }

    [Fact]
    public void Validate_AllowsConfiguredKeyVaultUriOutsideDevelopment()
    {
        DataProtectionKeyWrappingOptions options = new() { KeyVaultKeyUri = "https://vantigo.vault.azure.net/keys/dataprotection/abc123" };

        options.Validate(isDevelopment: false);
    }

    [Fact]
    public void Validate_WhitespaceOnlyKeyVaultUri_StillRejectsOutsideDevelopment()
    {
        DataProtectionKeyWrappingOptions options = new() { KeyVaultKeyUri = "   " };

        Assert.Throws<InvalidOperationException>(() => options.Validate(isDevelopment: false));
    }

    [Fact]
    public void Validate_AllowsUnwrappedKeysOutsideDevelopmentWithTheEscapeHatch()
    {
        DataProtectionKeyWrappingOptions options = new() { AllowUnwrappedKeys = true };

        options.Validate(isDevelopment: false);
    }

    [Theory]
    [InlineData("Production")]
    [InlineData("Staging")]
    public void AddDataProtectionOptions_FailsClosedOutsideDevelopmentWithoutTheEscapeHatch(string environmentName)
    {
        using ServiceProvider provider = BuildProvider(environmentName, new Dictionary<string, string?>());

        OptionsValidationException exception = Assert.Throws<OptionsValidationException>(
            () => provider.GetRequiredService<IOptions<DataProtectionKeyWrappingOptions>>().Value);

        Assert.Contains(exception.Failures, failure => failure.Contains("not production-ready", StringComparison.Ordinal));
    }

    [Fact]
    public void AddDataProtectionOptions_AllowsUnwrappedKeysOutsideDevelopmentWithTheEscapeHatch()
    {
        using ServiceProvider provider = BuildProvider("Production", new Dictionary<string, string?>
        {
            ["DataProtection:AllowUnwrappedKeys"] = "true",
        });

        DataProtectionKeyWrappingOptions options = provider.GetRequiredService<IOptions<DataProtectionKeyWrappingOptions>>().Value;

        Assert.True(options.AllowUnwrappedKeys);
        Assert.Null(options.KeyVaultKeyUri);
    }

    [Fact]
    public void AddDataProtectionOptions_AllowsConfiguredKeyVaultUriInProduction()
    {
        const string keyVaultKeyUri = "https://vantigo.vault.azure.net/keys/dataprotection/abc123";
        using ServiceProvider provider = BuildProvider("Production", new Dictionary<string, string?>
        {
            ["DataProtection:KeyVaultKeyUri"] = keyVaultKeyUri,
        });

        DataProtectionKeyWrappingOptions options = provider.GetRequiredService<IOptions<DataProtectionKeyWrappingOptions>>().Value;

        Assert.Equal(keyVaultKeyUri, options.KeyVaultKeyUri);
    }

    [Fact]
    public void AddDataProtectionOptions_AllowsUnwrappedKeysInDevelopment()
    {
        using ServiceProvider provider = BuildProvider("Development", new Dictionary<string, string?>());

        DataProtectionKeyWrappingOptions options = provider.GetRequiredService<IOptions<DataProtectionKeyWrappingOptions>>().Value;

        Assert.Null(options.KeyVaultKeyUri);
        Assert.False(options.AllowUnwrappedKeys);
    }

    private static ServiceProvider BuildProvider(string environmentName, Dictionary<string, string?> settings)
    {
        IConfigurationRoot configuration = new ConfigurationBuilder()
            .AddInMemoryCollection(settings)
            .Build();

        ServiceCollection services = new();
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