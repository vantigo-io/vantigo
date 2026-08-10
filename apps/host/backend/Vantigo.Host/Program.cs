using Vantigo.Communications.Database;
using Vantigo.Communications.Endpoints;
using Vantigo.Customers.Database;
using Vantigo.Customers.Database.DevelopmentSeed;
using Vantigo.Customers.Endpoints;
using Vantigo.Host;
using Vantigo.Hosting;
using Vantigo.Identity.Endpoints.Auth;
using Vantigo.Identity.Services;
using Vantigo.Products.Database;
using Vantigo.Products.Endpoints;

var commandLine = VantigoCommandLine.Parse(args);
if (commandLine.Command == VantigoCommand.Invalid)
{
    VantigoCommandLine.WriteUsageError($"Unknown command '{commandLine.InvalidCommand}'.");
    return;
}

if (commandLine.Command == VantigoCommand.NoArguments && args.Length == 0)
{
    VantigoCommandLine.WriteUsageError();
    return;
}

var builder = WebApplication.CreateBuilder(commandLine.RemainingArguments);
var configuresApi = commandLine.Command is VantigoCommand.Api or VantigoCommand.NoArguments;
builder.Services.AddHostDatabases(builder.Configuration);

if (configuresApi)
{
    builder.AddVantigoTelemetry("vantigo");
    builder.Services.AddSingleton<BootstrapSecretProvider>();
    InfrastructureConfiguration.AddConfiguredDataProtection(builder.Services, builder.Configuration, builder.Environment);
    InfrastructureConfiguration.ConfigureForwardedHeaders(builder.Services, builder.Configuration);
    builder.Services.AddVantigoIdentity(builder.Environment);
    var oidc = WorkforceOidcOptions.Load(builder.Configuration, builder.Environment);
    builder.Services.AddWorkforceOidc(oidc, builder.Environment);
    builder.Services.AddApplicationEmail(builder.Configuration);
    builder.Services.AddVantigoAuthorization();
    builder.Services.AddVantigoAntiforgery(builder.Environment);
    builder.Services.AddVantigoAuthenticationRateLimiting();
    builder.Services.AddSpaIndexDocument(buildTimeBasePath: "/", defaultTitle: "Vantigo");
    AddEnabledModules(builder.Services, builder.Configuration);
}
else if (commandLine.Command == VantigoCommand.Seed)
{
    builder.Services.AddVantigoIdentity(builder.Environment);
    AddEnabledModules(builder.Services, builder.Configuration);
}
else if (commandLine.Command == VantigoCommand.Migrate)
{
    AddEnabledModules(builder.Services, builder.Configuration);
}

var app = builder.Build();
var testPreparation = app.Services.GetService<HostTestStartupPreparation>();
if (commandLine.Command == VantigoCommand.NoArguments && testPreparation is null)
{
    VantigoCommandLine.WriteUsageError();
    return;
}

if (commandLine.Command == VantigoCommand.Migrate)
{
    await MigrateEnabledModulesAsync(app.Services, builder.Configuration);
    return;
}

if (commandLine.Command == VantigoCommand.Seed)
{
    if (!app.Environment.IsDevelopment())
    {
        VantigoCommandLine.WriteUsageError("The seed command is only available in Development.");
        return;
    }
    if (builder.Configuration.GetValue("Development:Seed:Enabled", true))
        await SeedEnabledModulesAsync(app.Services, builder.Configuration);
    return;
}

if (testPreparation is not null)
{
    if (testPreparation.ApplyMigrations) await MigrateEnabledModulesAsync(app.Services, builder.Configuration);
    if (testPreparation.ApplyMigrations && builder.Configuration.GetValue("Modules:Communications:Enabled", true))
        await app.Services.SeedCommunicationsAsync(builder.Configuration);
    if (testPreparation.SeedDevelopmentData && !app.Environment.IsDevelopment())
        throw new InvalidOperationException("Test seed preparation requires the Development environment.");
    if (testPreparation.SeedDevelopmentData && builder.Configuration.GetValue("Development:Seed:Enabled", true))
        await SeedEnabledModulesAsync(app.Services, builder.Configuration);
}

_ = app.Services.GetRequiredService<BootstrapSecretProvider>();
var workforceOidc = app.Services.GetRequiredService<WorkforceOidcOptions>();
_ = new AppPublicUrls(app.Configuration);
app.MapOpenApi().WithDocumentPerVersion();
app.UseForwardedHeaders();
app.UseAppBasePath();
app.UseSpaIndexRewrite();
app.UseStaticFiles();
app.UseRouting();
app.UseRateLimiter();
app.UseAuthentication();
app.UseAuthorization();
app.UseAntiforgery();
app.MapVantigoIdentityEndpoints(workforceOidc);
MapEnabledModules(app, builder.Configuration);
app.Map("/api", () => Results.NotFound());
app.Map("/api/{**path}", () => Results.NotFound());
app.MapSpaFallback();
app.Run();

static void AddEnabledModules(IServiceCollection services, IConfiguration configuration)
{
    if (configuration.GetValue("Modules:Customers:Enabled", true)) services.AddCustomersModule(configuration);
    if (configuration.GetValue("Modules:Communications:Enabled", true)) services.AddCommunicationsModule(configuration);
    if (configuration.GetValue("Modules:Products:Enabled", true)) services.AddProductsModule(configuration);
}

static async Task MigrateEnabledModulesAsync(IServiceProvider services, IConfiguration configuration)
{
    if (configuration.GetValue("Modules:Customers:Enabled", true)) await services.MigrateAsync();
    if (configuration.GetValue("Modules:Communications:Enabled", true)) await services.MigrateCommunicationsAsync();
    if (configuration.GetValue("Modules:Products:Enabled", true)) await services.MigrateProductsAsync();
    await services.MigrateIdentityAsync();
}

static async Task SeedEnabledModulesAsync(IServiceProvider services, IConfiguration configuration)
{
    await IdentityDevelopmentSeeder.SeedAsync(services, configuration);
    if (configuration.GetValue("Modules:Customers:Enabled", true)) await services.SeedAsync(configuration);
    if (configuration.GetValue("Modules:Communications:Enabled", true)) await services.SeedCommunicationsAsync(configuration);
    if (configuration.GetValue("Modules:Products:Enabled", true)) await services.SeedProductsAsync(configuration);
}

static void MapEnabledModules(WebApplication app, IConfiguration configuration)
{
    if (configuration.GetValue("Modules:Customers:Enabled", true)) app.MapCustomersModule();
    if (configuration.GetValue("Modules:Communications:Enabled", true)) app.MapCommunicationsModule();
    if (configuration.GetValue("Modules:Products:Enabled", true)) app.MapProductsModule();
}

public partial class Program { }