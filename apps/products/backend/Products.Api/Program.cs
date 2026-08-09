using Vantigo.Hosting;
using Vantigo.Products.Api;
using Vantigo.Products.Api.Database;
using Vantigo.Products.Api.Database.DevelopmentSeed;
using Vantigo.Products.Api.Endpoints;
using Vantigo.Products.Api.Endpoints.Auth;
using Vantigo.Products.Api.Services;

if (args.Length == 0)
{
    ProductApiCommandLine.WriteUsageError();
    return;
}

var commandLine = ProductApiCommandLine.Parse(args);
if (commandLine.Command == ProductApiCommand.Invalid)
{
    ProductApiCommandLine.WriteUsageError($"Unknown command '{commandLine.InvalidCommand}'.");
    return;
}

var builder = WebApplication.CreateBuilder(commandLine.RemainingArguments);

builder.Services.AddProductDatabases(builder.Configuration);

var configuresApi = commandLine.Command is ProductApiCommand.Api or ProductApiCommand.NoArguments;
WorkforceOidcOptions? workforceOidc = null;
if (configuresApi)
{
    builder.AddVantigoTelemetry("products");
    builder.Services.AddProductApiVersioning();
    workforceOidc = WorkforceOidcOptions.Load(builder.Configuration, builder.Environment);

    builder.Services.AddSingleton<BootstrapSecretProvider>();
    InfrastructureConfiguration.AddConfiguredDataProtection(builder.Services, builder.Configuration, builder.Environment);
    InfrastructureConfiguration.ConfigureForwardedHeaders(builder.Services, builder.Configuration);
    builder.Services.AddProductIdentity(builder.Environment);
    builder.Services.AddWorkforceOidc(workforceOidc, builder.Environment);
    builder.Services.AddApplicationEmail(builder.Configuration);
    builder.Services.AddProductAuthorization();
    builder.Services.AddProductAntiforgery(builder.Environment);
    builder.Services.AddAuthenticationRateLimiting();
    builder.Services.AddSpaIndexDocument(buildTimeBasePath: "/products", defaultTitle: "Products");
}
else if (commandLine.Command == ProductApiCommand.Seed)
{
    builder.Services.AddProductIdentity(builder.Environment);
}

var app = builder.Build();

var testPreparation = app.Services.GetService<ProductApiTestStartupPreparation>();
if (commandLine.Command == ProductApiCommand.NoArguments && testPreparation is null)
{
    ProductApiCommandLine.WriteUsageError();
    return;
}

if (commandLine.Command == ProductApiCommand.Migrate)
{
    await app.MigrateProductDatabasesAsync();
    return;
}

if (commandLine.Command == ProductApiCommand.Seed)
{
    if (!app.Environment.IsDevelopment())
    {
        ProductApiCommandLine.WriteUsageError("The seed command is only available in Development.");
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
        await app.MigrateProductDatabasesAsync();
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

// Mount the whole application under a configurable base path (default "/products")
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

// Deep links like /products must fall back to the templated SPA entry point.
// API and OpenAPI endpoints match their own routes first and are unaffected.
app.MapSpaFallback();

app.Run();

/// <summary>
/// Exposes the implicit <c>Program</c> class to the test project so integration
/// tests can bootstrap the API through <c>WebApplicationFactory</c>.
/// </summary>
public partial class Program;