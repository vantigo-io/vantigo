using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.FileProviders;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Options;

namespace Vantigo.Configuration.Tests;

public sealed class EmailOptionsTests
{
    [Fact]
    public void Smtp_InProduction_DoesNotFailStartupValidation()
    {
        var provider = BuildProvider("Production", "Smtp");

        var options = provider.GetRequiredService<IOptions<EmailOptions>>().Value;

        Assert.Equal("Smtp", options.Provider);
    }

    [Theory]
    [InlineData("Production")]
    [InlineData("Staging")]
    public void Logging_OutsideDevelopment_FailsStartupValidation(string environmentName)
    {
        var provider = BuildProvider(environmentName, "Logging");

        var exception = Assert.Throws<OptionsValidationException>(
            () => provider.GetRequiredService<IOptions<EmailOptions>>().Value);

        Assert.Contains("Provider must not be Logging", exception.Message, StringComparison.Ordinal);
    }

    [Fact]
    public void Logging_OutsideDevelopment_FailsStartupValidation_WhenProviderIsUnconfigured()
    {
        // The default Provider value is Logging, so leaving Email:Provider unset
        // outside Development must fail exactly like configuring it explicitly.
        var provider = BuildProvider("Production", emailProvider: null);

        Assert.Throws<OptionsValidationException>(
            () => provider.GetRequiredService<IOptions<EmailOptions>>().Value);
    }

    [Fact]
    public void Logging_InDevelopment_PassesStartupValidation()
    {
        var provider = BuildProvider("Development", "Logging");

        var options = provider.GetRequiredService<IOptions<EmailOptions>>().Value;

        Assert.Equal("Logging", options.Provider);
    }

    private static ServiceProvider BuildProvider(string environmentName, string? emailProvider)
    {
        var configurationValues = new Dictionary<string, string?>();
        if (emailProvider is not null)
        {
            configurationValues["Email:Provider"] = emailProvider;
        }

        var configuration = new ConfigurationBuilder()
            .AddInMemoryCollection(configurationValues)
            .Build();

        var services = new ServiceCollection();
        services.AddSingleton<IHostEnvironment>(new TestHostEnvironment(environmentName));
        services.AddEmailOptions(configuration);
        return services.BuildServiceProvider();
    }

    private sealed class TestHostEnvironment(string environmentName) : IHostEnvironment
    {
        public string EnvironmentName { get; set; } = environmentName;
        public string ApplicationName { get; set; } = nameof(EmailOptionsTests);
        public string ContentRootPath { get; set; } = AppContext.BaseDirectory;
        public IFileProvider ContentRootFileProvider { get; set; } = new NullFileProvider();
    }
}