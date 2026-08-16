using global::Azure.Identity;

using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Options;

using Vantigo.Azure.Identity;

namespace Vantigo.Azure.Identity.Tests;

public sealed class AzureIdentityServiceCollectionExtensionsTests
{
    [Fact]
    public void Defaults_to_enabled_and_registers_one_default_credential_singleton()
    {
        var services = new ServiceCollection();
        services.AddVantigoAzureIdentity(BuildConfiguration());
        using var provider = services.BuildServiceProvider();

        var credentials = provider.GetServices<global::Azure.Core.TokenCredential>().ToArray();
        Assert.Single(credentials);
        Assert.IsType<DefaultAzureCredential>(credentials[0]);
        Assert.Same(credentials[0], provider.GetRequiredService<global::Azure.Core.TokenCredential>());
    }

    [Fact]
    public void Disabled_does_not_register_a_token_credential()
    {
        var services = new ServiceCollection();
        services.AddVantigoAzureIdentity(BuildConfiguration(("Azure:Identity:Enabled", "false")));
        using var provider = services.BuildServiceProvider();

        Assert.Empty(provider.GetServices<global::Azure.Core.TokenCredential>());
        Assert.False(provider.GetRequiredService<IOptions<AzureIdentityOptions>>().Value.Enabled);
    }

    [Fact]
    public void Invalid_enabled_value_fails_options_validation_without_constructing_a_credential()
    {
        var services = new ServiceCollection();
        services.AddVantigoAzureIdentity(BuildConfiguration(("Azure:Identity:Enabled", "sometimes")));
        using var provider = services.BuildServiceProvider();

        var exception = Assert.Throws<OptionsValidationException>(() =>
        {
            _ = provider.GetRequiredService<IOptions<AzureIdentityOptions>>().Value;
        });
        Assert.Contains("must be true or false", exception.Message, StringComparison.Ordinal);
        Assert.Empty(provider.GetServices<global::Azure.Core.TokenCredential>());
    }

    [Fact]
    public void Standard_azure_sdk_environment_configuration_is_not_reimplemented()
    {
        var configuration = BuildConfiguration(
            ("Azure:Identity:Enabled", "true"),
            ("AZURE_CLIENT_ID", "client-id"),
            ("AZURE_TENANT_ID", "tenant-id"),
            ("AZURE_CLIENT_SECRET", "secret"));
        var services = new ServiceCollection();
        services.AddVantigoAzureIdentity(configuration);
        using var provider = services.BuildServiceProvider();

        Assert.True(provider.GetRequiredService<IOptions<AzureIdentityOptions>>().Value.Enabled);
        Assert.IsType<DefaultAzureCredential>(provider.GetRequiredService<global::Azure.Core.TokenCredential>());
    }

    private static IConfiguration BuildConfiguration(params (string Key, string Value)[] values) =>
        new ConfigurationBuilder()
            .AddInMemoryCollection(values.ToDictionary(value => value.Key, value => (string?)value.Value))
            .Build();
}