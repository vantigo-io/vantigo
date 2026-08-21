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
        using var provider = BuildProvider(
            Environments.Production,
            ("Authentication:Bootstrap:Secret", "configured-secret"),
            ("Authentication:Owners:AllowInsecureNoMfa", "true"));

        var options = provider.GetRequiredService<IOptions<VantigoAuthenticationOptions>>().Value;

        Assert.Equal("configured-secret", options.Bootstrap.Secret);
    }

    [Fact]
    public void MfaNotRequired_OutsideDevelopment_FailsOptionsValidation()
    {
        using var provider = BuildProvider(
            Environments.Production,
            ("Authentication:Bootstrap:Secret", "configured-secret"));

        var exception = Assert.Throws<OptionsValidationException>(() =>
            provider.GetRequiredService<IOptions<VantigoAuthenticationOptions>>().Value);

        Assert.Contains("privileged MFA is not enabled", exception.Message, StringComparison.Ordinal);
        Assert.Contains("Authentication:Owners:RequireMfa", exception.Message, StringComparison.Ordinal);
        Assert.Contains("Authentication:Owners:AllowInsecureNoMfa", exception.Message, StringComparison.Ordinal);
    }

    [Fact]
    public void MfaNotRequired_InDevelopment_PassesOptionsValidation()
    {
        using var provider = BuildProvider(Environments.Development);

        var options = provider.GetRequiredService<IOptions<VantigoAuthenticationOptions>>().Value;

        Assert.False(options.Owners.RequireMfa);
    }

    [Fact]
    public void MfaRequired_OutsideDevelopment_PassesOptionsValidation()
    {
        using var provider = BuildProvider(
            Environments.Production,
            ("Authentication:Bootstrap:Secret", "configured-secret"),
            ("Authentication:Owners:RequireMfa", "true"));

        var options = provider.GetRequiredService<IOptions<VantigoAuthenticationOptions>>().Value;

        Assert.True(options.Owners.RequireMfa);
    }

    [Fact]
    public void MfaNotRequired_OutsideDevelopment_PassesOptionsValidationWithTheEscapeHatch()
    {
        using var provider = BuildProvider(
            Environments.Production,
            ("Authentication:Bootstrap:Secret", "configured-secret"),
            ("Authentication:Owners:AllowInsecureNoMfa", "true"));

        var options = provider.GetRequiredService<IOptions<VantigoAuthenticationOptions>>().Value;

        Assert.False(options.Owners.RequireMfa);
        Assert.True(options.Owners.AllowInsecureNoMfa);
    }

    [Fact]
    public void SessionBounds_DefaultToBoundedPrivilegedLifetimes()
    {
        using var provider = BuildProvider(Environments.Development);

        var sessions = provider.GetRequiredService<IOptions<VantigoAuthenticationOptions>>().Value.Sessions;

        Assert.Equal(TimeSpan.FromHours(8), sessions.IdleTimeout);
        Assert.Equal(TimeSpan.FromHours(2), sessions.PrivilegedIdleTimeout);
        Assert.Equal(TimeSpan.FromHours(24), sessions.AbsoluteLifetime);
        Assert.Equal(TimeSpan.FromHours(8), sessions.PrivilegedAbsoluteLifetime);
        Assert.True(sessions.PrivilegedIdleTimeout < sessions.IdleTimeout);
        Assert.True(sessions.PrivilegedAbsoluteLifetime < sessions.AbsoluteLifetime);
        Assert.Equal(TimeSpan.FromSeconds(30), sessions.RevocationCacheDuration);
        Assert.Equal(TimeSpan.FromMinutes(15), sessions.PrincipalRefreshInterval);
    }

    [Fact]
    public void SessionBounds_AreConfigurable()
    {
        using var provider = BuildProvider(
            Environments.Development,
            ("Authentication:Sessions:IdleTimeout", "00:45:00"),
            ("Authentication:Sessions:PrivilegedAbsoluteLifetime", "04:00:00"));

        var sessions = provider.GetRequiredService<IOptions<VantigoAuthenticationOptions>>().Value.Sessions;

        Assert.Equal(TimeSpan.FromMinutes(45), sessions.IdleTimeout);
        Assert.Equal(TimeSpan.FromHours(4), sessions.PrivilegedAbsoluteLifetime);
    }

    [Fact]
    public void NonPositiveSessionLifetime_FailsOptionsValidation()
    {
        using var provider = BuildProvider(
            Environments.Development,
            ("Authentication:Sessions:AbsoluteLifetime", "00:00:00"));

        var exception = Assert.Throws<OptionsValidationException>(() =>
            provider.GetRequiredService<IOptions<VantigoAuthenticationOptions>>().Value);

        Assert.Contains("Sessions:AbsoluteLifetime must be greater than zero", exception.Message, StringComparison.Ordinal);
    }

    [Fact]
    public void NegativeRevocationCacheDuration_FailsOptionsValidation()
    {
        using var provider = BuildProvider(
            Environments.Development,
            ("Authentication:Sessions:RevocationCacheDuration", "-00:00:01"));

        var exception = Assert.Throws<OptionsValidationException>(() =>
            provider.GetRequiredService<IOptions<VantigoAuthenticationOptions>>().Value);

        Assert.Contains("Sessions:RevocationCacheDuration must not be negative", exception.Message, StringComparison.Ordinal);
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