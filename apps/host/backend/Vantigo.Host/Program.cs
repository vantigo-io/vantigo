using Microsoft.Extensions.Options;

using Vantigo.Communications.Database;
using Vantigo.Communications.Endpoints;
using Vantigo.Configuration;
using Vantigo.Contracts.Web;
using Vantigo.Customers.Database;
using Vantigo.Customers.Database.DevelopmentSeed;
using Vantigo.Customers.Endpoints;
using Vantigo.DataProtection.PostgreSql;
using Vantigo.Energy.Database;
using Vantigo.Energy.Endpoints;
using Vantigo.Host;
using Vantigo.Host.Antiforgery;
using Vantigo.Identity.Database;
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
builder.Services.AddVantigoConfiguration(builder.Configuration);
builder.Services.AddHostDatabases();
builder.Services.AddVantigoDataProtection(builder.Configuration, builder.Environment);

if (configuresApi)
{
    builder.AddVantigoTelemetry("vantigo");
    builder.Services.AddSingleton<BootstrapSecretProvider>();
    builder.Services.AddVantigoForwardedHeaders();
    builder.Services.AddVantigoIdentity(builder.Environment);
    builder.Services.AddWorkforceOidc(builder.Configuration, builder.Environment);
    builder.Services.AddVantigoAuthorization();
    builder.Services.AddVantigoAntiforgery(builder.Environment);
    builder.Services.AddVantigoAuthenticationRateLimiting();
    builder.Services.AddApplicationEmail();
    builder.Services.AddSpaIndexDocument(buildTimeBasePath: "/", defaultTitle: "Vantigo");
}
else if (commandLine.Command == VantigoCommand.Seed)
{
    builder.Services.AddVantigoIdentity(builder.Environment);
}

AddEnabledModules(builder.Services);

var app = builder.Build();
var testPreparation = app.Services.GetService<HostTestStartupPreparation>();
if (commandLine.Command == VantigoCommand.NoArguments && testPreparation is null)
{
    VantigoCommandLine.WriteUsageError();
    return;
}

if (commandLine.Command == VantigoCommand.Migrate)
{
    await app.Services.MigrateDataProtectionAsync();
    await MigrateEnabledModulesAsync(app.Services);
    return;
}

if (commandLine.Command == VantigoCommand.Seed)
{
    if (!app.Environment.IsDevelopment())
    {
        VantigoCommandLine.WriteUsageError("The seed command is only available in Development.");
        return;
    }

    if (app.Services.GetRequiredService<IOptions<DevelopmentSeedOptions>>().Value.Enabled)
        await SeedEnabledModulesAsync(app.Services);
    return;
}

if (testPreparation is not null)
{
    if (testPreparation.ApplyMigrations) await app.Services.MigrateDataProtectionAsync();
    if (testPreparation.ApplyMigrations) await MigrateEnabledModulesAsync(app.Services);
    if (testPreparation.ApplyMigrations && app.Services.GetRequiredService<IOptions<ModuleHostingOptions>>().Value.Communications.Enabled)
        await app.Services.SeedCommunicationsAsync();
    if (testPreparation.SeedDevelopmentData && !app.Environment.IsDevelopment())
        throw new InvalidOperationException("Test seed preparation requires the Development environment.");
    if (testPreparation.SeedDevelopmentData && app.Services.GetRequiredService<IOptions<DevelopmentSeedOptions>>().Value.Enabled)
        await SeedEnabledModulesAsync(app.Services);
}

_ = app.Services.GetRequiredService<BootstrapSecretProvider>();
_ = app.Services.GetRequiredService<WorkforceOidcOptions>();
_ = app.Services.GetRequiredService<AppPublicUrls>();
app.MapOpenApi().WithDocumentPerVersion();
app.UseForwardedHeaders();
app.UseAppBasePath();
app.UseSpaIndexRewrite();
app.UseStaticFiles();
app.UseRouting();
app.UseRateLimiter();
app.UseAuthentication();
app.UseAuthorization();
app.UseVantigoAntiforgery();
app.MapVantigoIdentityEndpoints();
MapEnabledModules(app);
app.Map("/api", () => Results.NotFound());
app.Map("/api/{**path}", () => Results.NotFound());
app.MapSpaFallback();
app.Run();

static void AddEnabledModules(IServiceCollection services)
{
    services.AddCustomersModule();
    services.AddCommunicationsModule();
    services.AddProductsModule();
    services.AddEnergyModule();
}

static async Task MigrateEnabledModulesAsync(IServiceProvider services)
{
    var modules = services.GetRequiredService<IOptions<ModuleHostingOptions>>().Value;
    if (modules.Customers.Enabled) await services.MigrateAsync();
    if (modules.Communications.Enabled) await services.MigrateCommunicationsAsync();
    if (modules.Products.Enabled) await services.MigrateProductsAsync();
    if (modules.Energy.Enabled) await services.MigrateEnergyAsync();
    await services.MigrateIdentityAsync();
}

static async Task SeedEnabledModulesAsync(IServiceProvider services, CancellationToken cancellationToken = default)
{
    var modules = services.GetRequiredService<IOptions<ModuleHostingOptions>>().Value;
    await IdentityDevelopmentSeeder.SeedAsync(services, cancellationToken);
    if (modules.Customers.Enabled) await services.SeedAsync();
    if (modules.Communications.Enabled) await services.SeedCommunicationsAsync();
    if (modules.Products.Enabled) await services.SeedProductsAsync();
    if (modules.Energy.Enabled) await services.SeedEnergyAsync(cancellationToken);
}

static void MapEnabledModules(WebApplication app)
{
    app.MapCustomersModule();
    app.MapCommunicationsModule();
    app.MapProductsModule();
    app.MapEnergyModule();
}

public partial class Program
{
}