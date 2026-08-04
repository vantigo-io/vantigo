using Microsoft.Extensions.Configuration;

using Vantigo.Communications.Api.Services;

namespace Vantigo.Communications.Api.Tests.Services;

public sealed class CustomersApiKeyValidatorTests
{
    [Fact]
    public void Rejects_missing_or_wrong_key()
    {
        var configuration = new ConfigurationBuilder().AddInMemoryCollection(new Dictionary<string, string?>
        {
            ["Customers:Enabled"] = "true",
            ["Customers:ApiKey"] = "correct-secret",
        }).Build();
        var validator = new CustomersApiKeyValidator(configuration);

        Assert.False(validator.IsValid(null));
        Assert.False(validator.IsValid(string.Empty));
        Assert.False(validator.IsValid("wrong-secret"));
    }

    [Fact]
    public void Accepts_configured_key()
    {
        var configuration = new ConfigurationBuilder().AddInMemoryCollection(new Dictionary<string, string?>
        {
            ["Customers:Enabled"] = "true",
            ["Customers:ApiKey"] = "correct-secret",
        }).Build();

        Assert.True(new CustomersApiKeyValidator(configuration).IsValid("correct-secret"));
    }

    [Fact]
    public void Fails_closed_when_customers_integration_is_disabled_or_key_is_missing()
    {
        var disabled = new ConfigurationBuilder().AddInMemoryCollection(new Dictionary<string, string?>
        {
            ["Customers:Enabled"] = "false",
            ["Customers:ApiKey"] = "correct-secret",
        }).Build();
        var missingKey = new ConfigurationBuilder().AddInMemoryCollection(new Dictionary<string, string?>
        {
            ["Customers:Enabled"] = "true",
        }).Build();

        Assert.False(new CustomersApiKeyValidator(disabled).IsValid("correct-secret"));
        Assert.False(new CustomersApiKeyValidator(missingKey).IsValid("correct-secret"));
    }
}