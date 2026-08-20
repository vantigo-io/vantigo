using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.FileProviders;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Options;

namespace Vantigo.Configuration.Tests;

public sealed class TenancyOptionsTests
{
    [Theory]
    [InlineData(null)]
    [InlineData("")]
    [InlineData("   ")]
    public void Validate_DefaultsToSingleTenant(string? configured)
    {
        TenancyOptions options = new() { Mode = configured };

        options.Validate(isDevelopment: false);

        Assert.Equal(TenancyOptions.SingleMode, options.Mode);
        Assert.False(options.IsMultiTenant);
    }

    [Fact]
    public void Validate_RejectsUnknownMode()
    {
        TenancyOptions options = new() { Mode = "hybrid" };

        InvalidOperationException exception = Assert.Throws<InvalidOperationException>(
            () => options.Validate(isDevelopment: true));

        Assert.Contains("Mode must be single or multi", exception.Message, StringComparison.Ordinal);
    }

    [Fact]
    public void Validate_AllowsMultiTenantInDevelopment()
    {
        TenancyOptions options = new() { Mode = " MULTI " };

        options.Validate(isDevelopment: true);

        Assert.True(options.IsMultiTenant);
    }

    [Fact]
    public void Validate_RejectsMultiTenantOutsideDevelopmentWithoutTheEscapeHatch()
    {
        TenancyOptions options = new() { Mode = TenancyOptions.MultiMode };

        InvalidOperationException exception = Assert.Throws<InvalidOperationException>(
            () => options.Validate(isDevelopment: false));

        Assert.Contains("not production-ready", exception.Message, StringComparison.Ordinal);
        Assert.Contains("vantigo/issues/7", exception.Message, StringComparison.Ordinal);
        Assert.Contains("Tenancy:AllowUnsafeMultiTenant", exception.Message, StringComparison.Ordinal);
    }

    [Fact]
    public void Validate_AllowsMultiTenantOutsideDevelopmentWithTheEscapeHatch()
    {
        TenancyOptions options = new()
        {
            Mode = TenancyOptions.MultiMode,
            AllowUnsafeMultiTenant = true,
        };

        options.Validate(isDevelopment: false);

        Assert.True(options.IsMultiTenant);
    }

    [Fact]
    public void AddTenancyOptions_FailsClosedForMultiTenantOutsideDevelopment()
    {
        using ServiceProvider provider = BuildProvider(Environments.Production, new Dictionary<string, string?>
        {
            ["Tenancy:Mode"] = TenancyOptions.MultiMode,
        });

        OptionsValidationException exception = Assert.Throws<OptionsValidationException>(
            () => provider.GetRequiredService<IOptions<TenancyOptions>>().Value);

        Assert.Contains(exception.Failures, failure =>
            failure.Contains("not production-ready", StringComparison.Ordinal));
    }

    [Fact]
    public void AddTenancyOptions_AllowsMultiTenantOutsideDevelopmentWithTheEscapeHatch()
    {
        using ServiceProvider provider = BuildProvider(Environments.Production, new Dictionary<string, string?>
        {
            ["Tenancy:Mode"] = TenancyOptions.MultiMode,
            ["Tenancy:AllowUnsafeMultiTenant"] = "true",
        });

        Assert.True(provider.GetRequiredService<IOptions<TenancyOptions>>().Value.IsMultiTenant);
    }

    [Fact]
    public void AddTenancyOptions_AllowsMultiTenantInDevelopment()
    {
        using ServiceProvider provider = BuildProvider(Environments.Development, new Dictionary<string, string?>
        {
            ["Tenancy:Mode"] = TenancyOptions.MultiMode,
        });

        Assert.True(provider.GetRequiredService<IOptions<TenancyOptions>>().Value.IsMultiTenant);
    }

    [Fact]
    public void AddTenancyOptions_DefaultsToSingleTenantWithoutConfiguration()
    {
        using ServiceProvider provider = BuildProvider(Environments.Production, new Dictionary<string, string?>());

        Assert.Equal(TenancyOptions.SingleMode, provider.GetRequiredService<IOptions<TenancyOptions>>().Value.Mode);
    }

    private static ServiceProvider BuildProvider(string environmentName, Dictionary<string, string?> settings)
    {
        IConfigurationRoot configuration = new ConfigurationBuilder()
            .AddInMemoryCollection(settings)
            .Build();

        ServiceCollection services = new();
        services.AddSingleton<IHostEnvironment>(new StubHostEnvironment(environmentName));
        services.AddTenancyOptions(configuration);
        return services.BuildServiceProvider();
    }

    private sealed class StubHostEnvironment(string environmentName) : IHostEnvironment
    {
        public string EnvironmentName { get; set; } = environmentName;
        public string ApplicationName { get; set; } = "Vantigo.Configuration.Tests";
        public string ContentRootPath { get; set; } = AppContext.BaseDirectory;
        public IFileProvider ContentRootFileProvider { get; set; } = new NullFileProvider();
    }
}