using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Identity;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.FileProviders;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Options;

using Vantigo.Identity.Endpoints.Auth;
using Vantigo.Identity.Services;

namespace Vantigo.Customers.Module.Tests.Integration;

public sealed class AuthSecurityUnitTests
{
    [Fact]
    public void InvitationTokensAreOpaqueAndOnlyTheirHashIsStable()
    {
        var first = InvitationTokenService.Create();
        var second = InvitationTokenService.Create();

        Assert.NotEqual(first.RawToken, second.RawToken);
        Assert.NotEqual(first.Hash, second.Hash);
        Assert.Equal(first.Hash, InvitationTokenService.Hash(first.RawToken));
        Assert.Equal(string.Empty, InvitationTokenService.Hash("not-a-valid-token"));
    }

    [Fact]
    public void DevelopmentAllowsAdminPasswordWhileNonDevelopmentKeepsStrictPolicy()
    {
        var development = ResolveIdentityOptions(Environments.Development);
        Assert.Equal(1, development.Password.RequiredLength);
        Assert.False(development.Password.RequireDigit);
        Assert.False(development.Password.RequireUppercase);
        Assert.False(development.Password.RequireLowercase);
        Assert.False(development.Password.RequireNonAlphanumeric);

        var production = ResolveIdentityOptions(Environments.Production);
        Assert.Equal(12, production.Password.RequiredLength);
        Assert.True(production.Password.RequireDigit);
        Assert.True(production.Password.RequireUppercase);
        Assert.True(production.Password.RequireLowercase);
        Assert.False(production.Password.RequireNonAlphanumeric);
    }

    private static IdentityOptions ResolveIdentityOptions(string environmentName)
    {
        var services = new ServiceCollection();
        services.AddVantigoIdentity(new TestHostEnvironment(environmentName));
        using var provider = services.BuildServiceProvider();
        return provider.GetRequiredService<IOptions<IdentityOptions>>().Value;
    }

    private sealed class TestHostEnvironment(string environmentName) : IHostEnvironment
    {
        public string EnvironmentName { get; set; } = environmentName;

        public string ApplicationName { get; set; } = typeof(AuthSecurityUnitTests).Assembly.GetName().Name!;

        public string ContentRootPath { get; set; } = AppContext.BaseDirectory;

        public IFileProvider ContentRootFileProvider { get; set; } = new NullFileProvider();
    }
}