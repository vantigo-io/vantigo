using Vantigo.Customers.Api;
using Vantigo.Customers.Api.Database;
using Vantigo.Customers.Api.Database.DevelopmentSeed;
using Vantigo.Customers.Api.Endpoints;
using Vantigo.Customers.Api.Endpoints.Auth;
using Vantigo.Customers.Api.Endpoints.Lookup;
using Vantigo.Customers.Api.Services;
using Vantigo.Hosting;

if (args.Length == 0)
{
    CustomerApiCommandLine.WriteUsageError();
    return;
}

var commandLine = CustomerApiCommandLine.Parse(args);
if (commandLine.Command == CustomerApiCommand.Invalid)
{
    CustomerApiCommandLine.WriteUsageError($"Unknown command '{commandLine.InvalidCommand}'.");
    return;
}

var builder = WebApplication.CreateBuilder(commandLine.RemainingArguments);

builder.Services.AddCustomerDatabases(builder.Configuration);

var configuresApi = commandLine.Command is CustomerApiCommand.Api or CustomerApiCommand.NoArguments;
WorkforceOidcOptions? workforceOidc = null;
if (configuresApi)
{
    builder.AddVantigoTelemetry("customers");
    builder.Services.AddCustomerApiVersioning();
    workforceOidc = WorkforceOidcOptions.Load(builder.Configuration, builder.Environment);

    builder.Services.AddSingleton<BootstrapSecretProvider>();
    InfrastructureConfiguration.AddConfiguredDataProtection(builder.Services, builder.Configuration, builder.Environment);
    InfrastructureConfiguration.ConfigureForwardedHeaders(builder.Services, builder.Configuration);
    builder.Services.AddCustomerIdentity(builder.Environment);
    builder.Services.AddWorkforceOidc(workforceOidc, builder.Environment);
    builder.Services.AddApplicationEmail(builder.Configuration);
    builder.Services.AddCustomerAuthorization();
    builder.Services.AddCustomerAntiforgery(builder.Environment);
    builder.Services.AddAuthenticationRateLimiting();
    builder.Services.AddCustomerTimeline();
    builder.Services.AddSpaIndexDocument(buildTimeBasePath: "/customers", defaultTitle: "Customers");
}
else if (commandLine.Command == CustomerApiCommand.Seed)
{
    builder.Services.AddCustomerIdentity(builder.Environment);
}

if (configuresApi)
{
    // Named client for the open Brønnøysundregisteret (Enhetsregisteret) API used by
    // the /lookup/brreg endpoint. The base URL is configurable so tests and other
    // environments can point it at a stub.
    builder.Services.AddHttpClient(BrregLookupEndpoint.HttpClientName, client =>
    {
        client.BaseAddress = new Uri(builder.Configuration["Brreg:BaseUrl"] ?? "https://data.brreg.no");
        client.Timeout = TimeSpan.FromSeconds(5);
    });
}

var app = builder.Build();

var testPreparation = app.Services.GetService<CustomerApiTestStartupPreparation>();
if (commandLine.Command == CustomerApiCommand.NoArguments && testPreparation is null)
{
    CustomerApiCommandLine.WriteUsageError();
    return;
}

if (commandLine.Command == CustomerApiCommand.Migrate)
{
    await app.MigrateCustomerDatabasesAsync();
    return;
}

if (commandLine.Command == CustomerApiCommand.Seed)
{
    if (!app.Environment.IsDevelopment())
    {
        CustomerApiCommandLine.WriteUsageError("The seed command is only available in Development.");
        return;
    }

    if (builder.Configuration.GetValue("Development:Seed:Enabled", true))
    {
        await app.SeedDevelopmentDataAsync();
    }

    return;
}

if (testPreparation is not null)
{
    if (testPreparation.ApplyMigrations)
    {
        await app.MigrateCustomerDatabasesAsync();
    }

    if (testPreparation.SeedDevelopmentData)
    {
        if (!app.Environment.IsDevelopment())
        {
            throw new InvalidOperationException("Test seed preparation requires the Development environment.");
        }

        if (builder.Configuration.GetValue("Development:Seed:Enabled", true))
        {
            await app.SeedDevelopmentDataAsync();
        }
    }
}

// Force creation at startup so a generated secret is logged exactly once. This is
// intentionally API-only: migrate and seed never resolve or host API services.
_ = app.Services.GetRequiredService<BootstrapSecretProvider>();

workforceOidc = app.Services.GetRequiredService<WorkforceOidcOptions>();

// Validate App:PublicOrigin at startup (fail fast on invalid values) and surface
// the URLs it derives: the exact OIDC callback URI to register with the identity
// provider, and a warning when explicit email-URL templates disagree with it.
var publicUrls = new AppPublicUrls(app.Configuration);
if (publicUrls.Origin is not null)
{
    if (workforceOidc.Enabled)
    {
        app.Logger.LogInformation(
            "Workforce OIDC public callback URI (register this with the identity provider): {CallbackUri}",
            publicUrls.PublicUrl(workforceOidc.CallbackPath));
    }

    foreach (var key in new[] { "Authentication:Invitations:AcceptUrl", "Authentication:PasswordReset:ResetUrl" })
    {
        var template = app.Configuration[key];
        if (template is not null && !template.StartsWith($"{publicUrls.Origin}{publicUrls.BasePath}", StringComparison.OrdinalIgnoreCase))
        {
            app.Logger.LogWarning(
                "{Key} ({Template}) does not start with the configured public origin and base path ({Public}); mailed links may point to the wrong place.",
                key, template, $"{publicUrls.Origin}{publicUrls.BasePath}");
        }
    }
}

app.MapOpenApi().WithDocumentPerVersion();

// Serve the built SPA (embedded into wwwroot on publish) from "/". In development
// the frontend runs on the Vite dev server, which proxies /api to this API.
app.UseForwardedHeaders();

// Mount the whole application under a configurable base path (default "/customers")
// so multiple apps can share one domain. Requests without the prefix pass through
// untouched, so serving from the root keeps working. Set App__BasePath="" to
// disable prefix generation entirely.
app.UseAppBasePath();

// The SPA entry document is templated at runtime with the configured base path
// (see SpaIndexDocument); never serve the raw file from wwwroot. "/" and
// "/index.html" both fall through to the SPA fallback endpoint below.
app.UseSpaIndexRewrite();

app.UseStaticFiles();
app.UseRouting();
app.UseRateLimiter();
app.UseAuthentication();
app.UseAuthorization();
app.UseAntiforgery();

app.MapAuthEndpoints(workforceOidc!);
app.MapVersionedBusinessEndpoints();

// Never let an unrecognized API/auth URL be mistaken for an SPA deep link.
app.Map("/api", () => Results.NotFound());
app.Map("/api/{**path}", () => Results.NotFound());
app.Map("/auth", () => Results.NotFound());
app.Map("/auth/{**path}", () => Results.NotFound());

// Deep links like /customers must fall back to the templated SPA entry point.
// API and OpenAPI endpoints match their own routes first and are unaffected.
app.MapSpaFallback();

app.Run();

/// <summary>
/// Exposes the implicit <c>Program</c> class to the test project so integration
/// tests can bootstrap the API through <c>WebApplicationFactory</c>.
/// </summary>
public partial class Program;