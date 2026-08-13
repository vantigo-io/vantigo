using System.Net;
using System.Net.Http.Json;

using Microsoft.AspNetCore.Authentication;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;

using Vantigo.Configuration;
using Vantigo.Identity.Endpoints.Auth;

namespace Vantigo.Customers.Module.Tests.Integration;

public static class WorkforceOidcOptionsTestLoader
{
    public static WorkforceOidcOptions Load(IConfiguration configuration, IHostEnvironment environment) =>
        WorkforceOidcOptionsExtensions.Load(configuration, environment);
}

public sealed class WorkforceOidcOptionsTests
{
    [Fact]
    public void MissingRequiredValues_DisablesOidcEvenWhenOptionalValuesArePresent()
    {
        var configuration = new ConfigurationBuilder()
            .AddInMemoryCollection(new Dictionary<string, string?>
            {
                ["Authentication:Oidc:Enabled"] = "false",
                ["Authentication:Oidc:DisplayName"] = "Company SSO",
            })
            .Build();

        var options = WorkforceOidcOptionsTestLoader.Load(configuration, new HostEnvironment
        {
            EnvironmentName = Environments.Production,
        });

        Assert.False(options.Enabled);
        Assert.Equal("Workforce SSO", options.DisplayName);
        Assert.Equal(WorkforceOidcOptions.DefaultCallbackPath, options.CallbackPath);
    }

    [Fact]
    public void PartialConfiguration_FailsClearly()
    {
        var configuration = new ConfigurationBuilder()
            .AddInMemoryCollection(new Dictionary<string, string?>
            {
                ["Authentication:Oidc:Enabled"] = "true",
                ["Authentication:Oidc:Provider"] = "Entra",
                ["Authentication:Oidc:Authority"] = "https://login.microsoftonline.com/00000000-0000-0000-0000-000000000000/v2.0",
                ["Authentication:Oidc:ClientId"] = "11111111-1111-1111-1111-111111111111",
                ["Authentication:Oidc:ClientAuthentication"] = "ClientSecret",
            })
            .Build();

        var exception = Assert.Throws<InvalidOperationException>(() => WorkforceOidcOptionsTestLoader.Load(
            configuration,
            new HostEnvironment { EnvironmentName = Environments.Production }));

        Assert.Contains("ClientSecret is required", exception.Message, StringComparison.Ordinal);
    }

    [Fact]
    public void ProductionHttpAuthority_FailsClearly()
    {
        var configuration = new ConfigurationBuilder()
            .AddInMemoryCollection(new Dictionary<string, string?>
            {
                ["Authentication:Oidc:Enabled"] = "true",
                ["Authentication:Oidc:Provider"] = "Entra",
                ["Authentication:Oidc:Authority"] = "http://login.microsoftonline.com/00000000-0000-0000-0000-000000000000/v2.0",
                ["Authentication:Oidc:ClientId"] = "11111111-1111-1111-1111-111111111111",
                ["Authentication:Oidc:ClientAuthentication"] = "ClientSecret",
                ["Authentication:Oidc:ClientSecret"] = "secret",
            })
            .Build();

        var exception = Assert.Throws<InvalidOperationException>(() => WorkforceOidcOptionsTestLoader.Load(
            configuration,
            new HostEnvironment { EnvironmentName = Environments.Production }));

        Assert.Contains("Entra Authority", exception.Message, StringComparison.Ordinal);
    }

    [Theory]
    [InlineData("/api/v1/identity/providers")]
    [InlineData("/api/v1/identity/oidc/challenge")]
    [InlineData("/api/v1/identity/oidc/complete")]
    public void CallbackPath_CannotCollideWithLocalAuthRoutes(string callbackPath)
    {
        var configuration = new ConfigurationBuilder()
            .AddInMemoryCollection(new Dictionary<string, string?>
            {
                ["Authentication:Oidc:Enabled"] = "true",
                ["Authentication:Oidc:Provider"] = "Entra",
                ["Authentication:Oidc:Authority"] = "https://login.microsoftonline.com/00000000-0000-0000-0000-000000000000/v2.0",
                ["Authentication:Oidc:ClientId"] = "11111111-1111-1111-1111-111111111111",
                ["Authentication:Oidc:ClientAuthentication"] = "ClientSecret",
                ["Authentication:Oidc:ClientSecret"] = "secret",
                ["Authentication:Oidc:CallbackPath"] = callbackPath,
            })
            .Build();

        var exception = Assert.Throws<InvalidOperationException>(() => WorkforceOidcOptionsTestLoader.Load(
            configuration,
            new HostEnvironment { EnvironmentName = Environments.Production }));

        Assert.Contains("CallbackPath", exception.Message, StringComparison.Ordinal);
    }

    [Fact]
    public void CallbackPath_MustBeAbsoluteRouteForm()
    {
        var configuration = new ConfigurationBuilder()
            .AddInMemoryCollection(new Dictionary<string, string?>
            {
                ["Authentication:Oidc:Enabled"] = "true",
                ["Authentication:Oidc:Provider"] = "Entra",
                ["Authentication:Oidc:Authority"] = "https://login.microsoftonline.com/00000000-0000-0000-0000-000000000000/v2.0",
                ["Authentication:Oidc:ClientId"] = "11111111-1111-1111-1111-111111111111",
                ["Authentication:Oidc:ClientAuthentication"] = "ClientSecret",
                ["Authentication:Oidc:ClientSecret"] = "secret",
                ["Authentication:Oidc:CallbackPath"] = "https://issuer.integration.test/callback",
            })
            .Build();

        var exception = Assert.Throws<InvalidOperationException>(() => WorkforceOidcOptionsTestLoader.Load(
            configuration,
            new HostEnvironment { EnvironmentName = Environments.Production }));

        Assert.Contains("CallbackPath", exception.Message, StringComparison.Ordinal);
    }

    private sealed class HostEnvironment : IHostEnvironment
    {
        public string EnvironmentName { get; set; } = Environments.Production;
        public string ApplicationName { get; set; } = "Vantigo.Customers.Module.Tests";
        public string ContentRootPath { get; set; } = AppContext.BaseDirectory;
        public Microsoft.Extensions.FileProviders.IFileProvider ContentRootFileProvider { get; set; } =
            new Microsoft.Extensions.FileProviders.NullFileProvider();
    }
}

[Collection("FreshCustomersApi")]
public sealed class WorkforceOidcRoutingTests
{
    [Fact]
    public async Task ConfiguredProvider_RegistersFixedSchemeAndSafeCompletionFailure()
    {
        await using var factory = new FreshCustomersApiFactory { EnableWorkforceOidc = true };
        await factory.StartAsync();

        var options = factory.Services.GetRequiredService<WorkforceOidcOptions>();
        var configuration = factory.Services.GetRequiredService<IConfiguration>();
        Assert.Equal("https://login.microsoftonline.com/00000000-0000-0000-0000-000000000000/v2.0", configuration["Authentication:Oidc:Authority"]);
        Assert.True(options.Enabled);
        var schemeProvider = factory.Services.GetRequiredService<IAuthenticationSchemeProvider>();
        var schemes = await schemeProvider.GetAllSchemesAsync();
        Assert.Contains(schemes, scheme => scheme.Name == WorkforceOidcOptions.Scheme);

        using var client = factory.CreateClient(new Microsoft.AspNetCore.Mvc.Testing.WebApplicationFactoryClientOptions
        {
            AllowAutoRedirect = false,
        });
        var response = await client.GetAsync(WorkforceOidcOptions.CompletionPath);

        Assert.True(response.StatusCode == HttpStatusCode.Redirect,
            $"{response.StatusCode} {response.Headers.Location} {await response.Content.ReadAsStringAsync()}");
        Assert.Equal("/sign-in?error=oidc_external_identity_missing", response.Headers.Location?.OriginalString);
    }
}