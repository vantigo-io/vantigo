using System.Net;
using System.Net.Http.Json;

using Microsoft.AspNetCore.Identity;
using Microsoft.AspNetCore.Mvc.Testing;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;

using Vantigo.Customers.Api.Database.Accounts;

namespace Vantigo.Customers.Api.Tests.Integration;

/// <summary>
/// Verifies that App:PublicOrigin derives the mailed URLs (combined with
/// App:BasePath) and that invalid values fail startup.
/// </summary>
[Collection(CustomersApiCollection.Name)]
public sealed class PublicOriginIntegrationTests
{
    private readonly CustomersApiFactory _factory;

    public PublicOriginIntegrationTests(CustomersApiFactory factory)
    {
        _factory = factory;
    }

    private WebApplicationFactory<Program> WithSettings(Dictionary<string, string?> settings) =>
        _factory.WithWebHostBuilder(builder =>
            builder.ConfigureAppConfiguration((_, configuration) =>
                configuration.AddInMemoryCollection(settings)));

    /// <summary>Password recovery only mails confirmed accounts with a password.</summary>
    private async Task<string> CreateConfirmedUserAsync()
    {
        var email = $"public-origin-{Guid.NewGuid():N}@integration.test";
        await using var scope = _factory.Services.CreateAsyncScope();
        var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
        var user = new ApplicationUser { UserName = email, Email = email, EmailConfirmed = true, DisplayName = "Public Origin User" };
        Assert.True((await users.CreateAsync(user, "PublicOrigin123")).Succeeded);
        return email;
    }

    [Fact]
    public async Task PasswordResetEmail_DerivesTheLinkFromPublicOriginAndBasePath()
    {
        using var factory = WithSettings(new Dictionary<string, string?>
        {
            ["App:PublicOrigin"] = "https://vantigo.example.test",
            ["App:BasePath"] = "/crm",
            // Unset the explicit template configured by the shared factory so
            // the derived default applies.
            ["Authentication:PasswordReset:ResetUrl"] = null,
        });
        using var client = factory.CreateClient(new WebApplicationFactoryClientOptions { HandleCookies = true });
        var token = await client.GetFromJsonAsync<AntiforgeryToken>("/auth/antiforgery");
        client.DefaultRequestHeaders.Add("X-XSRF-TOKEN", token!.Token);

        _factory.EmailSender.Clear();
        var email = await CreateConfirmedUserAsync();
        var response = await client.PostAsJsonAsync("/auth/password-recovery/request", new { email });

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        var body = Assert.Single(_factory.EmailSender.Messages).TextBody;
        Assert.Contains("https://vantigo.example.test/crm/password-reset?email=", body);
        Assert.Contains("&token=", body);
    }

    [Fact]
    public async Task ExplicitResetTemplate_StillWinsOverThePublicOrigin()
    {
        using var factory = WithSettings(new Dictionary<string, string?>
        {
            ["App:PublicOrigin"] = "https://vantigo.example.test",
            // The shared factory's explicit template (http://test.local/reset)
            // remains configured and must take precedence.
        });
        using var client = factory.CreateClient(new WebApplicationFactoryClientOptions { HandleCookies = true });
        var token = await client.GetFromJsonAsync<AntiforgeryToken>("/auth/antiforgery");
        client.DefaultRequestHeaders.Add("X-XSRF-TOKEN", token!.Token);

        _factory.EmailSender.Clear();
        var email = await CreateConfirmedUserAsync();
        var response = await client.PostAsJsonAsync("/auth/password-recovery/request", new { email });

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        var body = Assert.Single(_factory.EmailSender.Messages).TextBody;
        Assert.Contains("http://test.local/reset?email=", body);
    }

    [Fact]
    public void InvalidPublicOrigin_FailsStartup()
    {
        using var factory = WithSettings(new Dictionary<string, string?>
        {
            ["App:PublicOrigin"] = "https://vantigo.example.test/with-a-path",
        });

        var exception = Assert.ThrowsAny<Exception>(() => factory.CreateClient());
        Assert.Contains("App:PublicOrigin", exception.ToString());
    }
}