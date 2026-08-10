using System.Net.Http.Json;
using System.Security.Claims;

using Microsoft.AspNetCore.Authentication;
using Microsoft.AspNetCore.Builder;
using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Http;
using Microsoft.AspNetCore.Identity;
using Microsoft.AspNetCore.Mvc.Testing;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.DependencyInjection.Extensions;
using Microsoft.Extensions.Hosting;

using Testcontainers.PostgreSql;

using Vantigo.Host;
using Vantigo.Identity.Endpoints.Auth;
using Vantigo.Identity.Services;

namespace Vantigo.Customers.Module.Tests.Integration;

/// <summary>
/// An isolated, initially empty application used for bootstrap and race tests.
/// It deliberately does not bootstrap during startup.
/// </summary>
public sealed class FreshCustomersApiFactory : WebApplicationFactory<Program>, IAsyncDisposable
{
    public const string BootstrapSecret = "fresh-integration-bootstrap-secret";

    private readonly PostgreSqlContainer _postgres = new PostgreSqlBuilder("postgres:17-alpine")
        .WithDatabase("vantigo")
        .Build();

    public bool RequireOwnerMfa { get; set; }

    public bool EnableWorkforceOidc { get; set; }

    public bool ConfigureBootstrapSecret { get; set; } = true;

    internal CapturingEmailSender EmailSender { get; } = new();

    public async Task StartAsync() => await _postgres.StartAsync();

    public HttpClient CreateCookieClient()
    {
        return CreateClient(new WebApplicationFactoryClientOptions { HandleCookies = true });
    }

    public async Task<HttpClient> CreateAntiforgeryClientAsync()
    {
        var client = CreateCookieClient();
        var token = await client.GetFromJsonAsync<AntiforgeryToken>("/api/v1/identity/antiforgery");
        client.DefaultRequestHeaders.Add("X-XSRF-TOKEN", token!.Token);
        return client;
    }

    protected override void ConfigureWebHost(IWebHostBuilder builder)
    {
        builder.UseEnvironment(Environments.Development);
        if (EnableWorkforceOidc)
        {
            builder.UseSetting("Authentication:Oidc:Authority", "https://issuer.integration.test");
            builder.UseSetting("Authentication:Oidc:ClientId", "integration-client");
            builder.UseSetting("Authentication:Oidc:ClientSecret", "integration-client-secret");
        }
        builder.ConfigureAppConfiguration((_, configuration) =>
        {
            var values = new Dictionary<string, string?>
            {
                ["ConnectionStrings:vantigo"] = _postgres.GetConnectionString(),
                ["Modules:Customers:Enabled"] = "true",
                ["Modules:Communications:Enabled"] = "false",
                ["Modules:Products:Enabled"] = "false",
                ["Development:Seed:Enabled"] = "false",
                ["Authentication:Owners:RequireMfa"] = RequireOwnerMfa.ToString(),
                ["Authentication:Invitations:AcceptUrl"] = "http://test.local/invitations?token={token}",
                ["Authentication:PasswordReset:ResetUrl"] = "http://test.local/reset?email={email}&token={token}",
            };
            if (ConfigureBootstrapSecret)
            {
                values["Authentication:Bootstrap:Secret"] = BootstrapSecret;
            }
            if (EnableWorkforceOidc)
            {
                values["Authentication:Oidc:Authority"] = "https://issuer.integration.test";
                values["Authentication:Oidc:ClientId"] = "integration-client";
                values["Authentication:Oidc:ClientSecret"] = "integration-client-secret";
            }

            configuration.AddInMemoryCollection(values);
        });

        builder.ConfigureServices(services =>
        {
            services.AddSingleton(new HostTestStartupPreparation(
                ApplyMigrations: true,
                SeedDevelopmentData: false));
            services.RemoveAll<IApplicationEmailSender>();
            services.AddSingleton<IApplicationEmailSender>(EmailSender);
        });

        if (EnableWorkforceOidc)
        {
            // Test-only controlled external-cookie producer. It exercises the real
            // completion endpoint without contacting an IdP or copying test routes
            // into the production API.
            builder.ConfigureServices(services =>
                services.AddTransient<Microsoft.AspNetCore.Hosting.IStartupFilter, ControlledExternalCookieStartupFilter>());
        }
    }

    public override async ValueTask DisposeAsync()
    {
        await base.DisposeAsync();
        await _postgres.DisposeAsync();
    }
}

internal sealed class ControlledExternalCookieStartupFilter : Microsoft.AspNetCore.Hosting.IStartupFilter
{
    public Action<IApplicationBuilder> Configure(Action<IApplicationBuilder> next) => app =>
    {
        app.Use(async (HttpContext context, RequestDelegate continuation) =>
        {
            if (!context.Request.Path.Equals("/test/oidc-external", StringComparison.Ordinal))
            {
                await continuation(context);
                return;
            }

            var claims = new List<Claim>
            {
                new(WorkforceOidcOptions.ValidatedIssuerClaim, context.Request.Query["issuer"].FirstOrDefault() ?? "https://issuer.integration.test"),
                new("sub", context.Request.Query.ContainsKey("sub") ? context.Request.Query["sub"].ToString() : "test-subject"),
                new("name", context.Request.Query["name"].FirstOrDefault() ?? "Workforce User"),
            };
            if (context.Request.Query.TryGetValue("email", out var email) && !string.IsNullOrWhiteSpace(email))
            {
                claims.Add(new Claim("email", email.ToString()));
                claims.Add(new Claim("email_verified", "true"));
            }

            await context.SignInAsync(
                IdentityConstants.ExternalScheme,
                new ClaimsPrincipal(new ClaimsIdentity(claims, "controlled-external")));
            context.Response.Redirect(WorkforceOidcOptions.CompletionPath);
        });
        next(app);
    };
}