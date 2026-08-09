using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Identity;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.FileProviders;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Options;

using Vantigo.Products.Api;
using Vantigo.Products.Api.Endpoints.Auth;
using Vantigo.Products.Api.Services;

namespace Vantigo.Products.Api.Tests.Integration;

public sealed class AuthSecurityUnitTests
{
    [Fact]
    public void CommandLineRequiresExplicitCommandAndPreservesRemainingArguments()
    {
        // Program handles the truly empty process argument list before creating a
        // builder; this parse result remains reserved for WebApplicationFactory's
        // host bootstrap arguments and its DI startup marker.
        Assert.Equal(ProductApiCommand.NoArguments, ProductApiCommandLine.Parse([]).Command);
        Assert.Equal(
            ProductApiCommand.Api,
            ProductApiCommandLine.Parse(["api", "--urls", "http://localhost"]).Command);
        Assert.Equal(
            ["--urls", "http://localhost"],
            ProductApiCommandLine.Parse(["api", "--urls", "http://localhost"]).RemainingArguments);
        Assert.Equal(ProductApiCommand.Migrate, ProductApiCommandLine.Parse(["migrate"]).Command);
        Assert.Equal(ProductApiCommand.Seed, ProductApiCommandLine.Parse(["seed"]).Command);
        Assert.Equal(ProductApiCommand.Invalid, ProductApiCommandLine.Parse(["unknown"]).Command);
        Assert.Equal(
            ProductApiCommand.NoArguments,
            ProductApiCommandLine.Parse([
                "--environment=Development",
                "--contentRoot=/tmp/products",
                "--applicationName=Vantigo.Products.Api",
            ]).Command);
        Assert.Equal(ProductApiCommand.Invalid, ProductApiCommandLine.Parse(["--unknown"]).Command);
    }

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
        services.AddProductIdentity(new TestHostEnvironment(environmentName));
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