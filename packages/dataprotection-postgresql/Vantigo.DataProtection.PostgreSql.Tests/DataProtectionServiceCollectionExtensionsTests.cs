using Microsoft.AspNetCore.DataProtection.KeyManagement;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.FileProviders;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Options;

using Vantigo.Configuration;

namespace Vantigo.DataProtection.PostgreSql.Tests;

public sealed class DataProtectionServiceCollectionExtensionsTests
{
    [Fact]
    public void No_key_vault_uri_configured_leaves_keys_unwrapped()
    {
        using var provider = BuildProvider(environmentName: "Development", keyVaultKeyUri: null);

        var keyManagementOptions = provider.GetRequiredService<IOptions<KeyManagementOptions>>().Value;

        Assert.Null(keyManagementOptions.XmlEncryptor);
    }

    [Fact]
    public void Key_vault_uri_configured_wraps_keys_with_azure_key_vault()
    {
        using var provider = BuildProvider(
            environmentName: "Development",
            keyVaultKeyUri: "https://vantigo.vault.azure.net/keys/dataprotection/abc123");

        var keyManagementOptions = provider.GetRequiredService<IOptions<KeyManagementOptions>>().Value;

        Assert.NotNull(keyManagementOptions.XmlEncryptor);
        Assert.Contains("AzureKeyVault", keyManagementOptions.XmlEncryptor.GetType().FullName, StringComparison.Ordinal);
    }

    [Fact]
    public void Key_vault_uri_is_exposed_through_options()
    {
        const string keyVaultKeyUri = "https://vantigo.vault.azure.net/keys/dataprotection/abc123";
        using var provider = BuildProvider(environmentName: "Development", keyVaultKeyUri);

        var options = provider.GetRequiredService<IOptions<DataProtectionKeyWrappingOptions>>().Value;

        Assert.Equal(keyVaultKeyUri, options.KeyVaultKeyUri);
    }

    [Fact]
    public void Unwrapped_keys_outside_development_fail_startup_validation()
    {
        using var provider = BuildProvider(environmentName: "Production", keyVaultKeyUri: null);

        Assert.Throws<OptionsValidationException>(
            () => provider.GetRequiredService<IOptions<DataProtectionKeyWrappingOptions>>().Value);
    }

    [Fact]
    public void Wrapped_keys_in_production_pass_startup_validation()
    {
        using var provider = BuildProvider(
            environmentName: "Production",
            keyVaultKeyUri: "https://vantigo.vault.azure.net/keys/dataprotection/abc123");

        var options = provider.GetRequiredService<IOptions<DataProtectionKeyWrappingOptions>>().Value;

        Assert.NotNull(options.KeyVaultKeyUri);
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

        var environment = new TestHostEnvironment(environmentName);
        var services = new ServiceCollection();
        services.AddSingleton<IHostEnvironment>(environment);
        services.AddVantigoDataProtection(configuration, environment);
        return services.BuildServiceProvider();
    }

    private sealed class TestHostEnvironment(string environmentName) : IHostEnvironment
    {
        public string EnvironmentName { get; set; } = environmentName;
        public string ApplicationName { get; set; } = nameof(DataProtectionServiceCollectionExtensionsTests);
        public string ContentRootPath { get; set; } = AppContext.BaseDirectory;
        public IFileProvider ContentRootFileProvider { get; set; } = new NullFileProvider();
    }
}