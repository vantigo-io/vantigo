using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Identity;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.FileProviders;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Options;

using Vantigo.Communications.Api.Endpoints.Auth;

namespace Vantigo.Communications.Api.Tests.Endpoints;

public sealed class CommunicationsIdentityOptionsTests
{
    [Fact]
    public void Development_accepts_the_short_seed_password_policy()
    {
        var options = CreateIdentityOptions(Environments.Development);

        Assert.Equal(1, options.Password.RequiredLength);
        Assert.False(options.Password.RequireDigit);
        Assert.False(options.Password.RequireUppercase);
        Assert.False(options.Password.RequireLowercase);
        Assert.False(options.Password.RequireNonAlphanumeric);
    }

    [Fact]
    public void Non_development_keeps_the_strict_password_policy()
    {
        var options = CreateIdentityOptions(Environments.Production);

        Assert.Equal(12, options.Password.RequiredLength);
        Assert.True(options.Password.RequireDigit);
        Assert.True(options.Password.RequireUppercase);
        Assert.True(options.Password.RequireLowercase);
        Assert.False(options.Password.RequireNonAlphanumeric);
    }

    private static IdentityOptions CreateIdentityOptions(string environmentName)
    {
        var services = new ServiceCollection();
        services.AddCommunicationsIdentity(new TestHostEnvironment(environmentName));
        using var provider = services.BuildServiceProvider();
        return provider.GetRequiredService<IOptions<IdentityOptions>>().Value;
    }

    private sealed class TestHostEnvironment(string environmentName) : IHostEnvironment
    {
        public string EnvironmentName { get; set; } = environmentName;
        public string ApplicationName { get; set; } = typeof(Program).Assembly.GetName().Name!;
        public string ContentRootPath { get; set; } = AppContext.BaseDirectory;
        public IFileProvider ContentRootFileProvider { get; set; } = new NullFileProvider();
    }
}