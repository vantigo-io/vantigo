using System.Net;
using System.Net.Http.Json;
using System.Security.Claims;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;

using Microsoft.AspNetCore.Authentication;
using Microsoft.AspNetCore.Authentication.OpenIdConnect;
using Microsoft.AspNetCore.Builder;
using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Http;
using Microsoft.AspNetCore.Identity;
using Microsoft.AspNetCore.Mvc.Testing;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.DependencyInjection.Extensions;
using Microsoft.Extensions.Hosting;
using Microsoft.IdentityModel.Protocols;
using Microsoft.IdentityModel.Protocols.OpenIdConnect;

using Testcontainers.PostgreSql;

using Vantigo.Configuration;
using Vantigo.Host;
using Vantigo.Identity.Database.Accounts;
using Vantigo.Identity.Services;
using Vantigo.Testing;

namespace Vantigo.Identity.Tests.Integration;

public class IdentityApiFactory : WebApplicationFactory<Program>, IAsyncLifetime
{
    internal const string OidcAuthority = "https://login.microsoftonline.com/00000000-0000-0000-0000-000000000000/v2.0";
    internal const string OidcClientId = "11111111-1111-1111-1111-111111111111";
    internal const string OidcTenantId = "00000000-0000-0000-0000-000000000000";
    public const string BootstrapSecret = "identity-integration-bootstrap-secret";
    public const string OwnerEmail = "identity-owner@integration.test";
    public const string OwnerPassword = "IdentityOwnerPassword123";
    public const string StaticScimToken = "static-scim-current-token-for-integration-tests";
    private readonly PostgreSqlContainer postgres = new PostgreSqlBuilder("postgres:17-alpine").WithDatabase("vantigo").Build();
    protected bool EnableStaticScim { get; set; }
    protected bool RequireOwnerMfa { get; set; }
    protected bool EnableWorkforceOidc { get; set; }
    protected bool EnableMultiTenant { get; set; }

    /// <summary>The ASP.NET Core environment the host boots under. Overridden by
    /// <see cref="ProductionIdentityApiFactory"/> to exercise non-Development startup.</summary>
    protected virtual string HostEnvironmentName => Environments.Development;

    /// <summary>Enabled state for each module's <c>Modules:*:Enabled</c> configuration key.</summary>
    protected virtual bool CustomersModuleEnabled => true;
    protected virtual bool CommunicationsModuleEnabled => false;
    protected virtual bool ProductsModuleEnabled => false;
    protected virtual bool EnergyModuleEnabled => false;

    /// <summary>The <c>App:PublicOrigin</c> override, required once the host leaves
    /// Development (password/cookie/passkey policy all tighten outside it).</summary>
    protected virtual string? PublicOrigin => null;

    /// <summary>Configuration merged over this factory's defaults.</summary>
    protected virtual IReadOnlyDictionary<string, string?> ExtraConfiguration =>
        new Dictionary<string, string?>();

    /// <summary>
    /// Client base address used by every request this factory issues. Outside
    /// Development, auth/antiforgery cookies are marked Secure, so clients must use
    /// https for TestServer to round-trip them.
    /// </summary>
    protected Uri ClientBaseAddress => new(HostEnvironmentName == Environments.Development
        ? "http://localhost"
        : "https://localhost");

    public Guid OwnerId { get; private set; }

    /// <summary>
    /// Least-privilege runtime role connection string the application connects
    /// with; migrations use the container superuser.
    /// </summary>
    internal string RuntimeConnectionString { get; private set; } = string.Empty;

    public async Task InitializeAsync()
    {
        await postgres.StartAsync();
        RuntimeConnectionString = await Vantigo.Tenancy.EntityFramework.TenantRuntimeRoleSql
            .ProvisionAsync(postgres.GetConnectionString());
        using var client = CreateClient(new WebApplicationFactoryClientOptions { HandleCookies = true, BaseAddress = ClientBaseAddress });
        var token = await client.GetFromJsonAsync<AntiforgeryToken>("/api/v1/identity/antiforgery");
        client.DefaultRequestHeaders.Add("X-XSRF-TOKEN", token!.Token);
        var bootstrap = await client.PostAsJsonAsync("/api/v1/identity/bootstrap", new { secret = BootstrapSecret, email = OwnerEmail, displayName = "Integration Owner", password = OwnerPassword });
        if (!bootstrap.IsSuccessStatusCode) throw new InvalidOperationException(await bootstrap.Content.ReadAsStringAsync());
        await using var scope = Services.CreateAsyncScope();
        OwnerId = (await scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>().FindByEmailAsync(OwnerEmail))!.Id;
    }

    private static int nextClientAddress;

    /// <summary>
    /// Gives every test client its own synthetic address so rate-limit
    /// partitions (IP backstop, per-account throttle) never couple unrelated
    /// tests through the shared "unknown" partition of the test server.
    /// </summary>
    public HttpClient CreateCookieClient()
    {
        var client = CreateClient(new WebApplicationFactoryClientOptions { HandleCookies = true, BaseAddress = ClientBaseAddress });
        var next = Interlocked.Increment(ref nextClientAddress);
        client.DefaultRequestHeaders.TryAddWithoutValidation("X-Test-Client-Address", $"10.1.{next / 250 % 250 + 1}.{next % 250 + 1}");
        return client;
    }
    public async Task<HttpClient> CreateAntiforgeryClientAsync()
    {
        var client = CreateCookieClient();
        await RefreshAntiforgeryAsync(client);
        return client;
    }
    public async Task<HttpClient> CreateOwnerClientAsync()
    {
        var client = CreateCookieClient();
        await RefreshAntiforgeryAsync(client);
        var login = await client.PostAsJsonAsync("/api/v1/identity/login", new { email = OwnerEmail, password = OwnerPassword });
        if (!login.IsSuccessStatusCode) throw new InvalidOperationException(await login.Content.ReadAsStringAsync());
        await RefreshAntiforgeryAsync(client);
        return client;
    }

    public async Task<(Guid Id, string Email, string Password)> CreateUserWithCredentialsAsync(string role)
    {
        var email = $"user-{Guid.NewGuid():N}@integration.test";
        const string password = "IntegrationUserPassword123";
        var id = await CreateUserAsync(role, email, password);
        return (id, email, password);
    }

    public async Task<HttpClient> CreateAuthenticatedClientAsync(string email, string password)
    {
        var client = await CreateAntiforgeryClientAsync();
        var response = await client.PostAsJsonAsync("/api/v1/identity/login", new { email, password });
        if (!response.IsSuccessStatusCode) throw new InvalidOperationException(await response.Content.ReadAsStringAsync());
        await RefreshAntiforgeryAsync(client);
        return client;
    }

    public async Task ResetIdentityStateAsync()
    {
        await ResetIdentitySchemaAsAdminAsync();
        await using var scope = Services.CreateAsyncScope();
        await scope.ServiceProvider.GetRequiredService<StaticScimStateInitializer>().EnsureAsync();
        await BootstrapOwnerAsync();
    }

    public async Task ResetIdentityStateWithoutBootstrapAsync()
    {
        await ResetIdentitySchemaAsAdminAsync();
        await using var scope = Services.CreateAsyncScope();
        await scope.ServiceProvider.GetRequiredService<StaticScimStateInitializer>().EnsureAsync();
    }

    /// <summary>
    /// Dropping and re-migrating the schema is owner-only DDL, so it runs over
    /// the container superuser rather than the app's least-privilege role. The
    /// re-created objects are covered by the owner's default privileges, so the
    /// runtime role keeps its DML access.
    /// </summary>
    private async Task ResetIdentitySchemaAsAdminAsync()
    {
        var options = new DbContextOptionsBuilder<AccountsDbContext>()
            .UseNpgsql(
                postgres.GetConnectionString(),
                npgsql => npgsql.MigrationsHistoryTable("__EFMigrationsHistory", "identity"))
            .Options;
        await using var admin = new AccountsDbContext(options);
        await admin.Database.ExecuteSqlRawAsync("DROP SCHEMA IF EXISTS identity CASCADE");
        await admin.Database.MigrateAsync();
    }

    public async Task BootstrapOwnerAsync()
    {
        using var client = CreateClient(new WebApplicationFactoryClientOptions { HandleCookies = true, BaseAddress = ClientBaseAddress });
        var token = await client.GetFromJsonAsync<AntiforgeryToken>("/api/v1/identity/antiforgery");
        client.DefaultRequestHeaders.Add("X-XSRF-TOKEN", token!.Token);
        var response = await client.PostAsJsonAsync("/api/v1/identity/bootstrap", new
        {
            secret = BootstrapSecret,
            email = OwnerEmail,
            displayName = "Integration Owner",
            password = OwnerPassword,
        });
        if (!response.IsSuccessStatusCode)
            throw new InvalidOperationException(await response.Content.ReadAsStringAsync());
        await using var scope = Services.CreateAsyncScope();
        OwnerId = (await scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>().FindByEmailAsync(OwnerEmail))!.Id;
    }

    public async Task<Guid> CreateUserAsync(string role, string? email = null, string? password = null)
    {
        await using var scope = Services.CreateAsyncScope();
        var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
        var roles = scope.ServiceProvider.GetRequiredService<RoleManager<IdentityRole<Guid>>>();
        if (await roles.FindByNameAsync(role) is null)
        {
            var roleResult = await roles.CreateAsync(new IdentityRole<Guid>(role));
            if (!roleResult.Succeeded)
            {
                throw new InvalidOperationException(
                    $"Could not create integration role '{role}': {FormatIdentityErrors(roleResult)}");
            }
        }
        email ??= $"user-{Guid.NewGuid():N}@integration.test";
        password ??= "IntegrationUserPassword123";
        var user = new ApplicationUser { UserName = email, Email = email, EmailConfirmed = true, DisplayName = "Test User" };
        var userResult = await users.CreateAsync(user, password);
        if (!userResult.Succeeded)
        {
            throw new InvalidOperationException(
                $"Could not create integration user '{email}': {FormatIdentityErrors(userResult)}");
        }

        await scope.ServiceProvider.GetRequiredService<TenantMembershipService>().EnsureDefaultMembershipAsync(user.Id);

        var roleAssignmentResult = await users.AddToRoleAsync(user, role);
        if (!roleAssignmentResult.Succeeded)
        {
            throw new InvalidOperationException(
                $"Could not assign integration role '{role}' to '{email}': {FormatIdentityErrors(roleAssignmentResult)}");
        }

        return user.Id;
    }

    private static string FormatIdentityErrors(IdentityResult result) =>
        string.Join("; ", result.Errors.Select(error => $"{error.Code}: {error.Description}"));

    public static string CreateTotpCode(string base32Secret, DateTimeOffset? timestamp = null)
    {
        const string alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
        var buffer = 0; var bits = 0; var bytes = new List<byte>();
        foreach (var character in base32Secret.TrimEnd('=').ToUpperInvariant())
        {
            buffer = (buffer << 5) | alphabet.IndexOf(character); bits += 5;
            if (bits < 8) continue; bits -= 8; bytes.Add((byte)(buffer >> bits)); buffer &= (1 << bits) - 1;
        }
        var counter = (timestamp ?? DateTimeOffset.UtcNow).ToUnixTimeSeconds() / 30;
        Span<byte> challenge = stackalloc byte[8];
        System.Buffers.Binary.BinaryPrimitives.WriteInt64BigEndian(challenge, counter);
        using var hmac = new System.Security.Cryptography.HMACSHA1(bytes.ToArray());
        var hash = hmac.ComputeHash(challenge.ToArray()); var offset = hash[^1] & 0x0F;
        var code = ((hash[offset] & 0x7F) << 24) | (hash[offset + 1] << 16) | (hash[offset + 2] << 8) | hash[offset + 3];
        return (code % 1_000_000).ToString("D6");
    }

    public static async Task RefreshAntiforgeryAsync(HttpClient client)
    {
        client.DefaultRequestHeaders.Remove("X-XSRF-TOKEN");
        var token = await client.GetFromJsonAsync<AntiforgeryToken>("/api/v1/identity/antiforgery");
        client.DefaultRequestHeaders.Add("X-XSRF-TOKEN", token!.Token);
    }

    internal TestEmailSender EmailSender => Services.GetRequiredService<TestEmailSender>();

    protected override void ConfigureWebHost(IWebHostBuilder builder)
    {
        builder.UseEnvironment(HostEnvironmentName);
        // Host configuration, so the module flags are in place before the host
        // composes its modules. ConfigureAppConfiguration is applied while the
        // host is built, which is after the modules have been registered.
        builder.UseSetting("Modules:Customers:Enabled", CustomersModuleEnabled.ToString());
        builder.UseSetting("Modules:Communications:Enabled", CommunicationsModuleEnabled.ToString());
        builder.UseSetting("Modules:Products:Enabled", ProductsModuleEnabled.ToString());
        builder.UseSetting("Modules:Energy:Enabled", EnergyModuleEnabled.ToString());
        if (EnableWorkforceOidc)
        {
            builder.UseSetting("Authentication:Oidc:Enabled", "true");
            builder.UseSetting("Authentication:Oidc:Provider", "Entra");
            builder.UseSetting("Authentication:Oidc:Authority", OidcAuthority);
            builder.UseSetting("Authentication:Oidc:ClientId", OidcClientId);
            builder.UseSetting("Authentication:Oidc:ClientAuthentication", "ClientSecret");
            builder.UseSetting("Authentication:Oidc:ClientSecret", "identity-integration-secret");
        }
        builder.ConfigureAppConfiguration((_, config) =>
        {
            var values = new Dictionary<string, string?>
            {
                ["ConnectionStrings:vantigo"] = RuntimeConnectionString,
                ["ConnectionStrings:migrations"] = postgres.GetConnectionString(),
                ["Tenancy:Mode"] = EnableMultiTenant ? "multi" : "single",
                ["Development:Seed:Enabled"] = "false",
                ["Authentication:Bootstrap:Secret"] = BootstrapSecret,
                ["Authentication:Owners:RequireMfa"] = RequireOwnerMfa.ToString(),
                ["Authentication:SystemAdmin:Email"] = OwnerEmail,
                ["Authentication:Scim:Enabled"] = EnableStaticScim.ToString(),
            };
            // The Logging email provider is rejected outside Development, and this
            // factory stubs IApplicationEmailSender anyway, so any real provider
            // satisfies startup validation without sending anything.
            if (HostEnvironmentName != Environments.Development)
            {
                values["Email:Provider"] = EmailOptions.SmtpProvider;
                // An unwrapped Data Protection key ring is rejected outside Development
                // unless AllowUnwrappedKeys explicitly accepts it. There is no Key
                // Vault to wrap keys with in this test host, so opt in deliberately
                // rather than configuring a fake key URI the host would try to use.
                values["DataProtection:AllowUnwrappedKeys"] = "true";
                // The Testcontainers PostgreSQL instance serves no certificate, so the
                // non-Development boot path is only reachable through the documented
                // transport escape hatch. The rules it relaxes have their own tests in
                // Vantigo.Host.Tests and Vantigo.Configuration.Tests.
                values["Security:AllowInsecureTransport"] = bool.TrueString;
                // Outside Development, privileged MFA is required unless this escape
                // hatch is set; RequireOwnerMfa defaults to false for most factories,
                // so opt in deliberately rather than forcing every non-Development
                // factory to also configure RequireMfa. The rule itself has its own
                // tests in Vantigo.Configuration.Tests.
                values["Authentication:Owners:AllowInsecureNoMfa"] = bool.TrueString;
            }
            foreach (var setting in ExtraConfiguration)
            {
                values[setting.Key] = setting.Value;
            }
            if (EnableStaticScim) values["Authentication:Scim:BearerToken"] = StaticScimToken;
            if (PublicOrigin is not null) values["App:PublicOrigin"] = PublicOrigin;
            if (EnableWorkforceOidc)
            {
                values["Authentication:Oidc:Enabled"] = "true";
                values["Authentication:Oidc:Provider"] = "Entra";
                values["Authentication:Oidc:Authority"] = OidcAuthority;
                values["Authentication:Oidc:ClientId"] = OidcClientId;
                values["Authentication:Oidc:ClientAuthentication"] = "ClientSecret";
                values["Authentication:Oidc:ClientSecret"] = "identity-integration-secret";
            }
            config.AddInMemoryCollection(values);
        });
        builder.ConfigureServices(services =>
        {
            services.AddContractRecording();
            services.AddSingleton(new HostTestStartupPreparation(true, false));
            services.AddTransient<IStartupFilter, IdentityTestClientAddressStartupFilter>();
            services.RemoveAll<IApplicationEmailSender>();
            services.AddSingleton<TestEmailSender>();
            services.AddSingleton<IApplicationEmailSender>(serviceProvider =>
                serviceProvider.GetRequiredService<TestEmailSender>());
            if (EnableWorkforceOidc)
            {
                services.AddTransient<IStartupFilter, ControlledExternalCookieStartupFilter>();
                services.PostConfigure<OpenIdConnectOptions>(WorkforceOidcOptions.Scheme, options =>
                {
                    var configuredAuthority = options.Authority?.Trim();
                    if (string.IsNullOrWhiteSpace(configuredAuthority) ||
                        !Uri.TryCreate(configuredAuthority, UriKind.Absolute, out var authorityUri) ||
                        authorityUri.Scheme is not ("http" or "https") ||
                        !string.IsNullOrEmpty(authorityUri.UserInfo) ||
                        !string.IsNullOrEmpty(authorityUri.Query) ||
                        !string.IsNullOrEmpty(authorityUri.Fragment))
                    {
                        throw new InvalidOperationException(
                            "The production OIDC options did not provide a usable Authority for the deterministic integration backchannel.");
                    }

                    if (string.IsNullOrWhiteSpace(options.ClientId) || string.IsNullOrWhiteSpace(options.ClientSecret))
                    {
                        throw new InvalidOperationException(
                            "The production OIDC options did not provide the client credentials for the deterministic integration backchannel.");
                    }

                    var handler = new DeterministicOidcBackchannelHandler(
                        configuredAuthority, options.ClientId, options.ClientSecret, OidcTenantId);
                    options.Backchannel = new HttpClient(handler)
                    {
                        BaseAddress = new Uri("http://deterministic.test"),
                    };
                    options.ConfigurationManager = new ConfigurationManager<OpenIdConnectConfiguration>(
                        $"{configuredAuthority.TrimEnd('/')}/.well-known/openid-configuration",
                        new OpenIdConnectConfigurationRetriever(),
                        options.Backchannel);
                    options.NonceCookie.SecurePolicy = CookieSecurePolicy.SameAsRequest;
                    options.CorrelationCookie.SecurePolicy = CookieSecurePolicy.SameAsRequest;
                });
            }
        });
    }

    async Task IAsyncLifetime.DisposeAsync() { await base.DisposeAsync(); await postgres.DisposeAsync(); }
}

public sealed class StaticScimIdentityApiFactory : IdentityApiFactory
{
    public StaticScimIdentityApiFactory() => EnableStaticScim = true;
}

public sealed class OidcIdentityApiFactory : IdentityApiFactory { public OidcIdentityApiFactory() => EnableWorkforceOidc = true; }
public sealed class MfaIdentityApiFactory : IdentityApiFactory { public MfaIdentityApiFactory() => RequireOwnerMfa = true; }
public sealed class MultiTenantIdentityApiFactory : IdentityApiFactory { public MultiTenantIdentityApiFactory() => EnableMultiTenant = true; }

/// <summary>
/// Boots the host outside Development against a clean database, mirroring a fresh
/// production deploy, with several modules host-enabled so the tenant bootstrap fix
/// can be verified end to end via the tenant capabilities endpoint.
/// </summary>
public sealed class ProductionIdentityApiFactory : IdentityApiFactory
{
    protected override string HostEnvironmentName => Environments.Production;
    protected override bool ProductsModuleEnabled => true;
    protected override bool EnergyModuleEnabled => true;
    protected override string? PublicOrigin => "https://vantigo.integration.test";
}

/// <summary>
/// Boots with second-scale session bounds so the absolute lifetime and the tighter
/// privileged idle window can be observed inside a test. The standard idle window
/// stays long on purpose: it is the cookie's own expiry span, and keeping it long
/// proves the privileged timeout is what ends a privileged session.
/// </summary>
public sealed class ShortSessionIdentityApiFactory : IdentityApiFactory
{
    public static readonly TimeSpan PrivilegedIdleTimeout = TimeSpan.FromSeconds(4);
    public static readonly TimeSpan AbsoluteLifetime = TimeSpan.FromSeconds(12);

    protected override IReadOnlyDictionary<string, string?> ExtraConfiguration => new Dictionary<string, string?>
    {
        ["Authentication:Sessions:IdleTimeout"] = "08:00:00",
        ["Authentication:Sessions:PrivilegedIdleTimeout"] = "00:00:04",
        ["Authentication:Sessions:AbsoluteLifetime"] = "00:00:12",
        ["Authentication:Sessions:PrivilegedAbsoluteLifetime"] = "00:00:12",
    };
}

internal sealed record AntiforgeryToken(string Token);
internal sealed class TestEmailSender : IApplicationEmailSender
{
    private readonly object gate = new();
    private readonly List<ApplicationEmail> messages = [];

    public IReadOnlyCollection<ApplicationEmail> Messages
    {
        get
        {
            lock (gate)
            {
                return messages.ToArray();
            }
        }
    }

    public Task SendAsync(ApplicationEmail email, CancellationToken cancellationToken = default)
    {
        cancellationToken.ThrowIfCancellationRequested();
        lock (gate)
        {
            messages.Add(email);
        }
        return Task.CompletedTask;
    }

    public IReadOnlyCollection<ApplicationEmail> ReadFor(string recipient)
    {
        lock (gate)
        {
            return messages.Where(message =>
                string.Equals(message.To, recipient, StringComparison.OrdinalIgnoreCase)).ToArray();
        }
    }

    public void ClearFor(string recipient)
    {
        lock (gate)
        {
            messages.RemoveAll(message =>
                string.Equals(message.To, recipient, StringComparison.OrdinalIgnoreCase));
        }
    }
}

internal sealed class ControlledExternalCookieStartupFilter : IStartupFilter
{
    public Action<IApplicationBuilder> Configure(Action<IApplicationBuilder> next) => app =>
    {
        app.Use(async (context, continuation) =>
        {
            if (!context.Request.Path.Equals("/test/oidc-external", StringComparison.Ordinal)) { await continuation(context); return; }
            var claims = new List<Claim>
            {
                new(WorkforceOidcOptions.ValidatedIssuerClaim, "https://login.microsoftonline.com/00000000-0000-0000-0000-000000000000/v2.0"),
                new("sub", context.Request.Query["sub"].FirstOrDefault() ?? "test-subject"),
                new("name", "Workforce User"),
                new("tid", "00000000-0000-0000-0000-000000000000"),
                new("oid", "22222222-2222-2222-2222-222222222222"),
                new("azp", "11111111-1111-1111-1111-111111111111"),
            };
            if (context.Request.Query.TryGetValue("email", out var email))
            {
                claims.Add(new Claim("email", email.ToString()));
                claims.Add(new Claim("email_verified", "true"));
            }
            await context.SignInAsync(IdentityConstants.ExternalScheme, new ClaimsPrincipal(new ClaimsIdentity(claims, "controlled-external")));
            context.Response.Redirect(WorkforceOidcOptions.CompletionPath);
        });
        next(app);
    };
}

internal sealed class DeterministicOidcBackchannelHandler(
    string authority,
    string clientId,
    string clientSecret,
    string tenantId) : HttpMessageHandler
{
    private const string SigningKeyId = "identity-integration-rsa";
    private readonly RSA signingKey = RSA.Create(2048);

    protected override async Task<HttpResponseMessage> SendAsync(
        HttpRequestMessage request,
        CancellationToken cancellationToken)
    {
        if (request.Method == HttpMethod.Get &&
            string.Equals(request.RequestUri?.AbsoluteUri,
                $"{authority}/.well-known/openid-configuration", StringComparison.Ordinal))
        {
            return JsonResponse(new
            {
                issuer = authority,
                authorization_endpoint = $"https://login.microsoftonline.com/{tenantId}/oauth2/v2.0/authorize",
                token_endpoint = $"https://login.microsoftonline.com/{tenantId}/oauth2/v2.0/token",
                jwks_uri = $"https://login.microsoftonline.com/{tenantId}/discovery/v2.0/keys",
                response_types_supported = new[] { "code" },
                subject_types_supported = new[] { "public" },
                id_token_signing_alg_values_supported = new[] { "RS256" },
                scopes_supported = new[] { "openid", "profile", "email" },
                token_endpoint_auth_methods_supported = new[] { "client_secret_post" },
            });
        }

        if (request.Method == HttpMethod.Get &&
            string.Equals(request.RequestUri?.AbsoluteUri,
                $"https://login.microsoftonline.com/{tenantId}/discovery/v2.0/keys", StringComparison.Ordinal))
        {
            var parameters = signingKey.ExportParameters(false);
            return JsonResponse(new
            {
                keys = new[]
                {
                    new
                    {
                        kty = "RSA",
                        use = "sig",
                        alg = "RS256",
                        kid = SigningKeyId,
                        n = Base64Url(parameters.Modulus!),
                        e = Base64Url(parameters.Exponent!),
                    },
                },
            });
        }

        if (request.Method == HttpMethod.Post &&
            string.Equals(request.RequestUri?.AbsoluteUri,
                $"https://login.microsoftonline.com/{tenantId}/oauth2/v2.0/token", StringComparison.Ordinal))
        {
            var form = ParseForm(await request.Content!.ReadAsStringAsync(cancellationToken));
            var codeParts = form.GetValueOrDefault("code", string.Empty).Split(':');
            if (codeParts.Length > 0 && string.Equals(codeParts[0], "invalid_grant", StringComparison.Ordinal))
            {
                return JsonResponse(new
                {
                    error = "invalid_grant",
                    error_description = "The deterministic test authorization code is invalid.",
                }, HttpStatusCode.BadRequest);
            }

            var expectedRedirectUri = $"http://localhost{WorkforceOidcOptions.DefaultCallbackPath}";
            var codeVerifier = form.GetValueOrDefault("code_verifier", string.Empty);
            if (!string.Equals(form.GetValueOrDefault("grant_type"), "authorization_code", StringComparison.Ordinal) ||
                !string.Equals(form.GetValueOrDefault("client_id"), clientId, StringComparison.Ordinal) ||
                !string.Equals(form.GetValueOrDefault("client_secret"), clientSecret, StringComparison.Ordinal) ||
                !string.Equals(form.GetValueOrDefault("redirect_uri"), expectedRedirectUri, StringComparison.Ordinal) ||
                codeParts.Length != 4 || !string.Equals(codeParts[0], "oidc-test", StringComparison.Ordinal) ||
                string.IsNullOrEmpty(codeVerifier) ||
                !string.Equals(codeParts[3], CreateCodeChallenge(codeVerifier), StringComparison.Ordinal))
            {
                return JsonResponse(new { error = "invalid_request" }, HttpStatusCode.BadRequest);
            }

            return JsonResponse(new
            {
                token_type = "Bearer",
                access_token = "deterministic-access-token",
                id_token = CreateIdToken(codeParts[1], $"{codeParts[1]}@integration.test", codeParts[2]),
            });
        }

        throw new InvalidOperationException($"Unexpected OIDC backchannel request: {request.Method} {request.RequestUri}");
    }

    private string CreateIdToken(string subject, string email, string nonce)
    {
        var header = Base64Url(JsonSerializer.SerializeToUtf8Bytes(new
        {
            alg = "RS256",
            typ = "JWT",
            kid = SigningKeyId,
        }));
        var now = DateTimeOffset.UtcNow.ToUnixTimeSeconds();
        var payload = Base64Url(JsonSerializer.SerializeToUtf8Bytes(new
        {
            iss = authority,
            aud = clientId,
            iat = now,
            nbf = now,
            exp = now + 600,
            nonce,
            tid = tenantId,
            oid = "22222222-2222-2222-2222-222222222222",
            azp = clientId,
            sub = subject,
            name = "Workforce User",
            email,
            email_verified = true,
        }));
        var unsigned = $"{header}.{payload}";
        var signature = signingKey.SignData(
            Encoding.ASCII.GetBytes(unsigned), HashAlgorithmName.SHA256, RSASignaturePadding.Pkcs1);
        return $"{unsigned}.{Base64Url(signature)}";
    }

    private static string CreateCodeChallenge(string codeVerifier) =>
        Base64Url(SHA256.HashData(Encoding.ASCII.GetBytes(codeVerifier)));

    private static Dictionary<string, string> ParseForm(string value) =>
        value.Split('&', StringSplitOptions.RemoveEmptyEntries)
            .Select(item => item.Split('=', 2))
            .ToDictionary(item => Uri.UnescapeDataString(item[0].Replace('+', ' ')),
                item => Uri.UnescapeDataString((item.Length == 2 ? item[1] : string.Empty).Replace('+', ' ')));

    private static HttpResponseMessage JsonResponse(object value, HttpStatusCode statusCode = HttpStatusCode.OK) =>
        new(statusCode)
        {
            Content = new StringContent(JsonSerializer.Serialize(value), Encoding.UTF8, "application/json"),
        };

    private static string Base64Url(byte[] bytes) =>
        Convert.ToBase64String(bytes).TrimEnd('=').Replace('+', '-').Replace('/', '_');

    protected override void Dispose(bool disposing)
    {
        if (disposing) signingKey.Dispose();
        base.Dispose(disposing);
    }
}

internal sealed class IdentityTestClientAddressStartupFilter : IStartupFilter
{
    public Action<IApplicationBuilder> Configure(Action<IApplicationBuilder> next) => app =>
    {
        app.Use(async (context, continuation) =>
        {
            var address = context.Request.Headers["X-Test-Client-Address"].FirstOrDefault();
            context.Connection.RemoteIpAddress = System.Net.IPAddress.TryParse(address, out var parsed)
                ? parsed
                : System.Net.IPAddress.Parse("10.1.0.1");
            await continuation(context);
        });
        next(app);
    };
}

[CollectionDefinition(Name)] public sealed class IdentityApiCollection : ICollectionFixture<IdentityApiFactory> { public const string Name = "IdentityApi"; }
[CollectionDefinition(Name)] public sealed class StaticScimApiCollection : ICollectionFixture<StaticScimIdentityApiFactory> { public const string Name = "StaticScimApi"; }
[CollectionDefinition(Name)] public sealed class IdentityOidcApiCollection : ICollectionFixture<OidcIdentityApiFactory> { public const string Name = "IdentityOidcApi"; }
[CollectionDefinition(Name)] public sealed class IdentityMfaApiCollection : ICollectionFixture<MfaIdentityApiFactory> { public const string Name = "IdentityMfaApi"; }
[CollectionDefinition(Name)] public sealed class MultiTenantIdentityApiCollection : ICollectionFixture<MultiTenantIdentityApiFactory> { public const string Name = "MultiTenantIdentityApi"; }
[CollectionDefinition(Name)] public sealed class ProductionIdentityApiCollection : ICollectionFixture<ProductionIdentityApiFactory> { public const string Name = "ProductionIdentityApi"; }
[CollectionDefinition(Name)] public sealed class ShortSessionIdentityApiCollection : ICollectionFixture<ShortSessionIdentityApiFactory> { public const string Name = "ShortSessionIdentityApi"; }