using System.Net.Http.Json;
using System.Security.Claims;
using System.Security.Cryptography;

using Microsoft.AspNetCore.Authentication;
using Microsoft.AspNetCore.Builder;
using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Http;
using Microsoft.AspNetCore.Identity;
using Microsoft.AspNetCore.Mvc.Testing;
using Microsoft.AspNetCore.WebUtilities;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.DependencyInjection.Extensions;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Http;

using Testcontainers.PostgreSql;

using Vantigo.Configuration;
using Vantigo.Contracts.Identity;
using Vantigo.Host;
using Vantigo.Identity.Database.Accounts;
using Vantigo.Identity.Endpoints.Auth;
using Vantigo.Identity.Services;

namespace Vantigo.Identity.Tests.Integration;

/// <summary>
/// Boots the real host, identity module, and Customers business API against
/// PostgreSQL. The collection fixture keeps the container shared while each test
/// uses unique accounts for stateful endpoint coverage.
/// </summary>
public class IdentityApiFactory : WebApplicationFactory<Program>, IAsyncLifetime
{
    public const string BootstrapSecret = "identity-integration-bootstrap-secret";
    public const string OwnerEmail = "identity-owner@integration.test";
    public const string OwnerPassword = "IdentityOwnerPassword123";
    public const string ScimTokenPepperReference = "VANTIGO_SCIM_INTEGRATION_TOKEN_PEPPER";
    private static readonly object ScimPepperLock = new();
    private static string? originalScimTokenPepper;
    private static int scimPepperUsers;
    private bool ownsScimPepper;

    private readonly PostgreSqlContainer postgres = new PostgreSqlBuilder("postgres:17-alpine")
        .WithDatabase("vantigo")
        .Build();

    public IdentityApiFactory()
    {
        // EnvironmentScimTokenPepper intentionally resolves the secret from
        // the process environment, not from test configuration. Set it before
        // the WebApplicationFactory can initialize the host and restore it
        // after the host is disposed.
        lock (ScimPepperLock)
        {
            if (scimPepperUsers++ == 0)
            {
                originalScimTokenPepper = Environment.GetEnvironmentVariable(ScimTokenPepperReference);
            }
            Environment.SetEnvironmentVariable(ScimTokenPepperReference, "integration-scim-token-pepper");
            ownsScimPepper = true;
        }
    }

    protected bool RequireOwnerMfa { get; set; }
    protected bool EnableWorkforceOidc { get; set; }

    public Guid OwnerId { get; private set; }

    public static string CreateTotpCode(string base32Secret, DateTimeOffset? timestamp = null)
    {
        var normalized = base32Secret.TrimEnd('=').ToUpperInvariant();
        var bytes = Base32Decode(normalized);
        var counter = (timestamp ?? DateTimeOffset.UtcNow).ToUnixTimeSeconds() / 30;
        Span<byte> challenge = stackalloc byte[8];
        System.Buffers.Binary.BinaryPrimitives.WriteInt64BigEndian(challenge, counter);
        using var hmac = new HMACSHA1(bytes);
        var hash = hmac.ComputeHash(challenge.ToArray());
        var offset = hash[^1] & 0x0F;
        var code = ((hash[offset] & 0x7F) << 24) |
            (hash[offset + 1] << 16) |
            (hash[offset + 2] << 8) |
            hash[offset + 3];
        return (code % 1_000_000).ToString("D6");
    }

    private static byte[] Base32Decode(string value)
    {
        const string alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
        var buffer = 0;
        var bits = 0;
        var bytes = new List<byte>();
        foreach (var character in value)
        {
            buffer = (buffer << 5) | alphabet.IndexOf(character);
            bits += 5;
            if (bits < 8) continue;
            bits -= 8;
            bytes.Add((byte)(buffer >> bits));
            buffer &= (1 << bits) - 1;
        }
        return bytes.ToArray();
    }

    public async Task InitializeAsync()
    {
        await postgres.StartAsync();

        using var client = CreateClient(new WebApplicationFactoryClientOptions { HandleCookies = true });
        var token = await client.GetFromJsonAsync<AntiforgeryToken>("/api/v1/identity/antiforgery");
        client.DefaultRequestHeaders.Add("X-XSRF-TOKEN", token!.Token);
        var bootstrap = await client.PostAsJsonAsync("/api/v1/identity/bootstrap", new
        {
            secret = BootstrapSecret,
            email = OwnerEmail,
            displayName = "Integration Owner",
            password = OwnerPassword,
        });
        if (bootstrap.StatusCode != System.Net.HttpStatusCode.Created)
        {
            throw new InvalidOperationException(
                $"Identity integration bootstrap failed: {bootstrap.StatusCode} {await bootstrap.Content.ReadAsStringAsync()}");
        }

        await using var scope = Services.CreateAsyncScope();
        var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
        OwnerId = (await users.FindByEmailAsync(OwnerEmail))!.Id;
    }

    public HttpClient CreateCookieClient() =>
        CreateClient(new WebApplicationFactoryClientOptions { HandleCookies = true });

    public async Task<HttpClient> CreateAntiforgeryClientAsync()
    {
        var client = CreateCookieClient();
        await RefreshAntiforgeryAsync(client);
        return client;
    }

    public async Task<HttpClient> CreateOwnerClientAsync()
    {
        var client = await CreateAntiforgeryClientAsync();
        var login = await client.PostAsJsonAsync("/api/v1/identity/login", new
        {
            email = OwnerEmail,
            password = OwnerPassword,
        });
        if (login.StatusCode != System.Net.HttpStatusCode.OK)
        {
            throw new InvalidOperationException(
                $"Identity owner login failed: {login.StatusCode} {await login.Content.ReadAsStringAsync()}");
        }

        await RefreshAntiforgeryAsync(client);
        return client;
    }

    public async Task<HttpClient> CreateAuthenticatedClientAsync(string email, string password)
    {
        var client = await CreateAntiforgeryClientAsync();
        var login = await client.PostAsJsonAsync("/api/v1/identity/login", new { email, password });
        if (login.StatusCode != System.Net.HttpStatusCode.OK)
        {
            throw new InvalidOperationException(
                $"Identity test-user login failed: {login.StatusCode} {await login.Content.ReadAsStringAsync()}");
        }

        await RefreshAntiforgeryAsync(client);
        return client;
    }

    public async Task<Guid> CreateUserAsync(string role, string? email = null, string? password = null)
    {
        email ??= $"user-{Guid.NewGuid():N}@integration.test";
        password ??= "IntegrationUserPassword123";
        await using var scope = Services.CreateAsyncScope();
        var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
        var roleManager = scope.ServiceProvider.GetRequiredService<RoleManager<IdentityRole<Guid>>>();
        if (await roleManager.FindByNameAsync(role) is null)
        {
            var roleResult = await roleManager.CreateAsync(new IdentityRole<Guid>
            {
                Name = role,
                NormalizedName = role.ToUpperInvariant(),
            });
            if (!roleResult.Succeeded)
                throw new InvalidOperationException($"Could not create integration role {role}.");
        }
        var user = new ApplicationUser
        {
            UserName = email,
            Email = email,
            EmailConfirmed = true,
            DisplayName = $"Test {role}",
        };
        var create = await users.CreateAsync(user, password);
        if (!create.Succeeded || !(await users.AddToRoleAsync(user, role)).Succeeded)
        {
            throw new InvalidOperationException($"Could not create integration {role} user {email}.");
        }

        return user.Id;
    }

    public async Task<(Guid Id, string Email, string Password)> CreateUserWithCredentialsAsync(string role)
    {
        var email = $"user-{Guid.NewGuid():N}@integration.test";
        const string password = "IntegrationUserPassword123";
        var id = await CreateUserAsync(role, email, password);
        return (id, email, password);
    }

    public static async Task RefreshAntiforgeryAsync(HttpClient client)
    {
        client.DefaultRequestHeaders.Remove("X-XSRF-TOKEN");
        var token = await client.GetFromJsonAsync<AntiforgeryToken>("/api/v1/identity/antiforgery");
        client.DefaultRequestHeaders.Add("X-XSRF-TOKEN", token!.Token);
    }

    protected override void ConfigureWebHost(IWebHostBuilder builder)
    {
        builder.UseEnvironment(Environments.Development);
        builder.ConfigureAppConfiguration((_, configuration) =>
        {
            var values = new Dictionary<string, string?>
            {
                ["ConnectionStrings:vantigo"] = postgres.GetConnectionString(),
                ["Modules:Customers:Enabled"] = "true",
                ["Modules:Communications:Enabled"] = "true",
                ["Modules:Products:Enabled"] = "false",
                ["Modules:Energy:Enabled"] = "false",
                ["Development:Seed:Enabled"] = "false",
                ["Authentication:Bootstrap:Secret"] = BootstrapSecret,
                ["Authentication:Owners:RequireMfa"] = RequireOwnerMfa.ToString(),
                ["Authentication:Invitations:AcceptUrl"] = "http://test.local/invitations?token={token}",
                ["Authentication:PasswordReset:ResetUrl"] = "http://test.local/reset?email={email}&token={token}",
                ["VANTIGO_SSO_ENTRA_CLIENT_SECRET"] = "integration-entra-secret",
                ["VANTIGO_SSO_GOOGLE_CLIENT_SECRET"] = "integration-google-secret",
                ["Authentication:Scim:TokenPepperReference"] = ScimTokenPepperReference,
            };
            if (EnableWorkforceOidc)
            {
                values["Authentication:Oidc:Authority"] = "https://issuer.integration.test";
                values["Authentication:Oidc:ClientId"] = "identity-integration-client";
                values["Authentication:Oidc:ClientSecret"] = "identity-integration-secret";
            }

            configuration.AddInMemoryCollection(values);
        });

        builder.ConfigureServices(services =>
        {
            services.AddSingleton(new HostTestStartupPreparation(
                ApplyMigrations: true,
                SeedDevelopmentData: false));
            services.RemoveAll<IApplicationEmailSender>();
            services.AddSingleton<IApplicationEmailSender, TestEmailSender>();
            services.RemoveAll<IFederationDiscoveryValidator>();
            services.AddScoped<IFederationDiscoveryValidator, TestFederationDiscoveryValidator>();
            services.RemoveAll<IFederationHostAddressResolver>();
            services.AddSingleton<IFederationHostAddressResolver, TestFederationHostAddressResolver>();
            services.Configure<HttpClientFactoryOptions>(OidcFederationDiscoveryValidator.HttpClientName,
                options => options.HttpMessageHandlerBuilderActions.Add(builder =>
                    builder.PrimaryHandler = new DynamicFederationTestHttpMessageHandler()));
            Environment.SetEnvironmentVariable("VANTIGO_SSO_INTEGRATION_CLIENT_SECRET", "integration-federation-secret");
        });

        if (EnableWorkforceOidc)
        {
            builder.ConfigureServices(services =>
                services.AddTransient<IStartupFilter, ControlledExternalCookieStartupFilter>());
        }
    }

    async Task IAsyncLifetime.DisposeAsync()
    {
        await base.DisposeAsync();
        await postgres.DisposeAsync();
        if (!ownsScimPepper) return;
        lock (ScimPepperLock)
        {
            if (--scimPepperUsers == 0)
            {
                Environment.SetEnvironmentVariable(ScimTokenPepperReference, originalScimTokenPepper);
                originalScimTokenPepper = null;
            }
            ownsScimPepper = false;
        }
    }
}

public sealed class MfaIdentityApiFactory : IdentityApiFactory
{
    public MfaIdentityApiFactory() => RequireOwnerMfa = true;
}

public sealed class OidcIdentityApiFactory : IdentityApiFactory
{
    public OidcIdentityApiFactory() => EnableWorkforceOidc = true;
}

internal sealed record AntiforgeryToken(string Token);

internal sealed class TestEmailSender : IApplicationEmailSender
{
    public Task SendAsync(ApplicationEmail email, CancellationToken cancellationToken = default) =>
        Task.CompletedTask;
}

internal sealed class TestFederationDiscoveryValidator : IFederationDiscoveryValidator
{
    public Task<FederationDiscoveryValidationResult> ValidateAsync(
        FederationProviderKind providerKind,
        string normalizedAuthority,
        CancellationToken cancellationToken) =>
        Task.FromResult(new FederationDiscoveryValidationResult(
            true,
            "validated",
            "The OIDC discovery document was validated.",
            normalizedAuthority,
            normalizedAuthority + "/.well-known/openid-configuration",
            normalizedAuthority + "/authorize",
            normalizedAuthority + "/token",
            normalizedAuthority + "/keys"));
}

internal sealed class TestFederationHostAddressResolver : IFederationHostAddressResolver
{
    public Task<System.Net.IPAddress[]> ResolveAsync(string host, CancellationToken cancellationToken) =>
        Task.FromResult(new[] { System.Net.IPAddress.Parse("1.2.3.4") });
}

internal static class DynamicFederationTestTransport
{
    public static Func<HttpRequestMessage, HttpResponseMessage>? Responder { get; set; }
    public static List<Uri> Requests { get; } = [];
    public static List<string> RequestBodies { get; } = [];
}

internal sealed class DynamicFederationTestHttpMessageHandler : HttpMessageHandler
{
    protected override Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken cancellationToken)
    {
        DynamicFederationTestTransport.Requests.Add(request.RequestUri!);
        DynamicFederationTestTransport.RequestBodies.Add(request.Content?.ReadAsStringAsync().GetAwaiter().GetResult() ?? string.Empty);
        return Task.FromResult(DynamicFederationTestTransport.Responder?.Invoke(request) ??
            new HttpResponseMessage(System.Net.HttpStatusCode.NotFound));
    }
}

internal sealed class ControlledExternalCookieStartupFilter : IStartupFilter
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
                new(WorkforceOidcOptions.ValidatedIssuerClaim,
                    context.Request.Query["issuer"].FirstOrDefault() ?? "https://issuer.integration.test"),
                new("sub", context.Request.Query.ContainsKey("sub")
                    ? context.Request.Query["sub"].ToString()
                    : "test-subject"),
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

[CollectionDefinition(Name)]
public sealed class IdentityApiCollection : ICollectionFixture<IdentityApiFactory>
{
    public const string Name = "IdentityApi";
}

[CollectionDefinition(Name)]
public sealed class IdentityMfaApiCollection : ICollectionFixture<MfaIdentityApiFactory>
{
    public const string Name = "IdentityMfaApi";
}

[CollectionDefinition(Name)]
public sealed class IdentityOidcApiCollection : ICollectionFixture<OidcIdentityApiFactory>
{
    public const string Name = "IdentityOidcApi";
}