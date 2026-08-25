using Microsoft.Extensions.Options;

using Vantigo.Azure.Identity;
using Vantigo.Communications.Database;
using Vantigo.Communications.Endpoints;
using Vantigo.Configuration;
using Vantigo.Contracts.AspNetCore.Authorization;
using Vantigo.Contracts.Authorization;
using Vantigo.Contracts.Web;
using Vantigo.Customers.Database;
using Vantigo.Customers.Database.DevelopmentSeed;
using Vantigo.Customers.Endpoints;
using Vantigo.DataProtection.PostgreSql;
using Vantigo.Energy.Database;
using Vantigo.Energy.Endpoints;
using Vantigo.Host;
using Vantigo.Host.Antiforgery;
using Vantigo.Host.Diagnostics;
using Vantigo.Host.Security;
using Vantigo.Identity.Authorization;
using Vantigo.Identity.Database;
using Vantigo.Identity.Endpoints.Auth;
using Vantigo.Identity.Services;
using Vantigo.Products.Database;
using Vantigo.Products.Endpoints;
using Vantigo.Storage;
using Vantigo.Tenancy;

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

if (commandLine.Command == VantigoCommand.HealthCheck)
{
    // Deliberately skips WebApplication.CreateBuilder: the chiseled container
    // has no shell for a CMD-SHELL healthcheck, so Docker/ACA exec this
    // process itself and it must stay a cheap, dependency-free HTTP probe.
    Environment.ExitCode = await VantigoHealthCheckClient.RunAsync() ? 0 : 1;
    return;
}

var builder = WebApplication.CreateBuilder(commandLine.RemainingArguments);
if (commandLine.Command == VantigoCommand.Worker)
{
    // The dedicated worker process always hosts the background services, even
    // when the shared environment disables in-process workers for the API.
    builder.Configuration["Workers:InProcess"] = "true";
}

var configuresApi = Program.IsApiCommand(commandLine.Command);
Program.RegisterHostServices(builder, commandLine.Command);

var app = builder.Build();
var testPreparation = app.Services.GetService<HostTestStartupPreparation>();
if (commandLine.Command == VantigoCommand.NoArguments && testPreparation is null)
{
    VantigoCommandLine.WriteUsageError();
    return;
}

if (commandLine.Command is VantigoCommand.Migrate or VantigoCommand.ResetCommunications)
{
    var commandSucceeded = await VantigoCommandDispatcher.ExecuteDatabaseCommandAsync(
        commandLine.Command,
        () => Program.MigrateAllAsync(app.Services),
        () => CommunicationsSchemaResetCommand.ExecuteAsync(
            app.Services,
            app.Environment,
            Environment.GetEnvironmentVariable(CommunicationsSchemaResetCommand.ConfirmationEnvironmentVariable)));
    if (commandSucceeded == false)
    {
        Environment.ExitCode = 2;
    }

    return;
}

if (commandLine.Command == VantigoCommand.Worker)
{
    // Background services only: no API surface, no SPA, just the health
    // endpoints a container platform needs to probe the worker.
    app.MapVantigoHealthChecks();
    app.Run();
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
        await Program.SeedEnabledModulesAsync(app.Services);
    return;
}

if (configuresApi)
{
    if (testPreparation is not null)
        await Program.PrepareHostForTestsAsync(app, testPreparation);

    await Program.InitializeApiAsync(app);
    // The OpenAPI document enumerates every endpoint and shape in the
    // installation; it stays a development and staging aid, not public
    // reconnaissance material.
    if (!app.Environment.IsProduction())
        app.MapOpenApi().WithDocumentPerVersion();
    app.UseExceptionHandler();
    app.UseVantigoSecurityHeaders();
    app.UseForwardedHeaders();
    // After forwarded headers so a TLS-terminating proxy's scheme is the one HSTS
    // sees; the middleware skips loopback hosts on its own.
    if (!app.Environment.IsDevelopment())
        app.UseHsts();
    app.UseAppBasePath();
    app.UseSpaIndexRewrite();
    app.UseStaticFiles();
    app.UseRouting();
    app.MapVantigoHealthChecks();
    app.UseRateLimiter();
    app.UseAuthentication();
    app.UseVantigoTenancy();
    app.UseAuthorization();
    app.UseVantigoAntiforgery();
    app.MapVantigoIdentityEndpoints();
    Program.MapEnabledModules(app);
    app.ValidatePermissionCatalog(app.Services.GetRequiredService<IPermissionCatalog>());
    app.Map("/api", () => Results.NotFound());
    app.Map("/api/{**path}", () => Results.NotFound());
    app.MapSpaFallback();
    app.Run();
}

public partial class Program
{
    public static bool IsApiCommand(VantigoCommand command) =>
        command is VantigoCommand.Api or VantigoCommand.NoArguments;

    public static void RegisterHostServices(WebApplicationBuilder builder, VantigoCommand command)
    {
        var configuresApi = IsApiCommand(command);
        // Configuration options are registered before telemetry because the
        // telemetry extension reads observability options during composition.
        builder.Services.AddVantigoConfiguration(builder.Configuration);
        if (configuresApi)
        {
            builder.AddVantigoTelemetry("vantigo");
            // Unhandled exceptions become sanitized Problem Details instead of
            // leaking stack traces or an empty body.
            builder.Services.AddVantigoExceptionHandling();
            // Accept only the configured public origin's host header instead of
            // the framework default of every host.
            builder.Services.AddVantigoHostFiltering(builder.Configuration);
        }

        builder.Services.AddVantigoTenancy();
        // Tenant directory services are needed by every command path: API traffic,
        // seeding, and module migrations that resolve the default tenant.
        builder.Services.AddVantigoIdentityTenancy();
        builder.Services.AddVantigoAzureIdentity(builder.Configuration);
        builder.Services.AddHostDatabases();
        builder.Services.AddVantigoDataProtection(builder.Configuration, builder.Environment);
        builder.Services.AddVantigoObjectStorage(builder.Configuration);
        builder.Services.AddVantigoHealthChecks();
        builder.Services.AddSingleton<BootstrapSecretProvider>();
        builder.Services.AddVantigoIdentity(builder.Environment);
        builder.Services.AddWorkforceOidc(builder.Environment);
        builder.Services.AddVantigoAuthorization();
        builder.Services.AddVantigoAntiforgery(builder.Environment);
        builder.Services.AddVantigoAuthenticationRateLimiting();
        builder.Services.AddApplicationEmail();
        builder.Services.AddSpaIndexDocument(buildTimeBasePath: "/", defaultTitle: "Vantigo");
        // API versioning and the versioned OpenAPI documents are host
        // infrastructure: the modules that used to be the only callers can all be
        // switched off, and MapOpenApi still has to resolve.
        builder.Services.AddVantigoApiVersioning();

        AddEnabledModules(builder.Services, builder.Configuration);

        // Resolve the catalog only after every enabled module has registered its
        // contributor. AddPermissionCatalog creates a deferred singleton so this
        // composition point also makes the boot-order contract explicit.
        builder.Services.AddPermissionCatalog(catalog =>
        {
            catalog.Add(new PermissionDescriptor(
                "identity:manage", "Manage identity", "Manage accounts, roles, and access.",
                "identity", "Administration", Sensitive: true, Delegable: false));
        });
    }

    public static async Task PrepareHostForTestsAsync(WebApplication app, HostTestStartupPreparation testPreparation)
    {
        if (testPreparation.ApplyMigrations)
        {
            await MigrateAllAsync(app.Services);
            if (ModuleActivation.Resolve(app.Services).Communications)
                await app.Services.SeedCommunicationsAsync();
        }

        if (testPreparation.SeedDevelopmentData && !app.Environment.IsDevelopment())
            throw new InvalidOperationException("Test seed preparation requires the Development environment.");
        if (testPreparation.SeedDevelopmentData && app.Services.GetRequiredService<IOptions<DevelopmentSeedOptions>>().Value.Enabled)
            await SeedEnabledModulesAsync(app.Services);
    }

    public static async Task InitializeApiAsync(WebApplication app)
    {
        await using var tenantStartupScope = app.Services.CreateAsyncScope();
        await tenantStartupScope.ServiceProvider.GetRequiredService<TenantBootstrapper>().EnsureAsync();
        await tenantStartupScope.ServiceProvider.GetRequiredService<SystemAdminBootstrapper>().EnsureAsync();

        _ = app.Services.GetRequiredService<BootstrapSecretProvider>();
        _ = app.Services.GetRequiredService<WorkforceOidcOptions>();
        _ = app.Services.GetRequiredService<StaticScimOptions>();
        _ = app.Services.GetRequiredService<AppPublicUrls>();

        await using var startupScope = app.Services.CreateAsyncScope();
        await startupScope.ServiceProvider.GetRequiredService<StaticScimStateInitializer>()
            .EnsureAsync();
    }

    /// <summary>
    /// Takes the one hosting decision the rest of the process reads back out of
    /// the container. A disabled module contributes no services, no workers, no
    /// permissions, and no endpoints; every later stage asks
    /// <see cref="ModuleActivation"/> rather than the configuration again.
    /// </summary>
    private static void AddEnabledModules(IServiceCollection services, IConfiguration configuration)
    {
        ModuleActivation activation = ModuleActivation.FromConfiguration(configuration);
        services.AddSingleton(activation);

        if (activation.Customers) services.AddCustomersModule();
        // The Communications background workers are registered by this call, so
        // they are switched off by the same decision as its endpoints.
        if (activation.Communications) services.AddCommunicationsModule(configuration);
        if (activation.Products) services.AddProductsModule();
        if (activation.Energy) services.AddEnergyModule();

        // Rejected here, before the container is built, so the error names the
        // two module flags that disagree instead of a missing service.
        ModuleCompositionValidator.Validate(activation, services);
    }

    /// <summary>
    /// Runs every enabled context's migrations under one installation-wide
    /// advisory lock, so concurrent migrators (multi-replica rollouts, retried
    /// jobs) serialize instead of corrupting a half-upgraded schema.
    /// </summary>
    private static Task MigrateAllAsync(IServiceProvider services) =>
        MigrationLock.RunAsync(services, async () =>
        {
            await services.MigrateDataProtectionAsync();
            await MigrateEnabledModulesAsync(services);
        });

    private static async Task MigrateEnabledModulesAsync(IServiceProvider services)
    {
        var logger = services.GetRequiredService<ILoggerFactory>().CreateLogger("Vantigo.Migrations");
        ModuleActivation activation = ModuleActivation.Resolve(services);
        if (activation.Customers)
        {
            logger.LogInformation("Migrating the customers schema.");
            await services.MigrateAsync();
        }
        if (activation.Communications)
        {
            logger.LogInformation("Migrating the communications schema.");
            await services.MigrateCommunicationsAsync();
        }
        if (activation.Products)
        {
            logger.LogInformation("Migrating the products schema.");
            await services.MigrateProductsAsync();
        }
        if (activation.Energy)
        {
            logger.LogInformation("Migrating the energy schema.");
            await services.MigrateEnergyAsync();
        }
        logger.LogInformation("Migrating the identity schema.");
        await services.MigrateIdentityAsync();
    }

    private static async Task SeedEnabledModulesAsync(IServiceProvider services, CancellationToken cancellationToken = default)
    {
        ModuleActivation activation = ModuleActivation.Resolve(services);
        await IdentityDevelopmentSeeder.SeedAsync(services, cancellationToken);
        if (activation.Customers) await services.SeedAsync();
        if (activation.Communications) await services.SeedCommunicationsAsync();
        if (activation.Products) await services.SeedProductsAsync();
        if (activation.Energy) await services.SeedEnergyAsync(cancellationToken);
    }

    /// <summary>
    /// Maps the endpoints of the modules this process composed. Public so the
    /// composition tests can map every module combination and run the permission
    /// catalog validation the API pipeline runs immediately afterwards.
    /// </summary>
    public static void MapEnabledModules(WebApplication app)
    {
        // The same decision that registered the modules, so a mapped endpoint can
        // never outlive the permission catalog contributor it depends on.
        ModuleActivation activation = ModuleActivation.Resolve(app.Services);
        if (activation.Customers) app.MapCustomersModule();
        if (activation.Communications) app.MapCommunicationsModule();
        if (activation.Products) app.MapProductsModule();
        if (activation.Energy) app.MapEnergyModule();
    }
}