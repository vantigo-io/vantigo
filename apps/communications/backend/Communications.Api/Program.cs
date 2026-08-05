using Vantigo.Communications.Api;
using Vantigo.Communications.Api.Database;
using Vantigo.Communications.Api.Database.DevelopmentSeed;
using Vantigo.Communications.Api.Endpoints;
using Vantigo.Communications.Api.Endpoints.Auth;
using Vantigo.Communications.Api.Infrastructure;
using Vantigo.Communications.Api.Services;

var commandLine = CommunicationsCommandLine.Parse(args);
if (commandLine.Mode == CommunicationsCommandMode.Invalid)
{
    CommunicationsCommandLine.WriteUsageError($"Unknown command '{commandLine.InvalidCommand}'.");
    return 2;
}

if (commandLine.Mode == CommunicationsCommandMode.NoArguments && args.Length == 0)
{
    CommunicationsCommandLine.WriteUsageError();
    return 2;
}

var builder = WebApplication.CreateBuilder(commandLine.RemainingArguments.ToArray());

if (commandLine.Mode == CommunicationsCommandMode.Migrate)
{
    builder.Services.AddCommunicationsDatabases(builder.Configuration);
    var migrationApp = builder.Build();
    await migrationApp.MigrateCommunicationsDatabasesAsync();
    return 0;
}

if (commandLine.Mode == CommunicationsCommandMode.Seed)
{
    if (!builder.Environment.IsDevelopment())
    {
        Console.Error.WriteLine("The 'seed' command is only available in Development.");
        Console.Error.WriteLine(CommunicationsCommandLine.Usage);
        return 2;
    }

    if (!builder.Configuration.GetValue("Development:Seed:Enabled", true))
    {
        return 0;
    }

    builder.Services.AddCommunicationsDatabases(builder.Configuration);
    builder.Services.AddCommunicationsIdentity(builder.Environment);
    var seedApp = builder.Build();
    await seedApp.SeedConfiguredMailboxAsync();
    await seedApp.SeedDevelopmentDataAsync();
    return 0;
}

AddApiServices(builder);
var app = builder.Build();

var testPreparation = app.Services.GetService<CommunicationsTestStartupPreparationMarker>();
if (commandLine.Mode == CommunicationsCommandMode.NoArguments && testPreparation is null)
{
    CommunicationsCommandLine.WriteUsageError();
    return 2;
}

if (commandLine.Mode == CommunicationsCommandMode.NoArguments && testPreparation is not null)
{
    await app.MigrateCommunicationsDatabasesAsync();
    await app.SeedConfiguredMailboxAsync();
    if (app.Environment.IsDevelopment() && builder.Configuration.GetValue("Development:Seed:Enabled", true))
    {
        await app.SeedDevelopmentDataAsync();
    }
}

_ = app.Services.GetRequiredService<BootstrapSecretProvider>();
MapApiEndpoints(app);
app.Run();
return 0;

static void AddApiServices(WebApplicationBuilder builder)
{
    builder.Services.AddCommunicationsOpenTelemetry();
    builder.Services.AddCommunicationsApiVersioning();
    builder.Services.AddCommunicationsDatabases(builder.Configuration);
    builder.Services.AddSingleton<BootstrapSecretProvider>();
    builder.Services.AddSingleton<CustomersApiKeyValidator>();
    InfrastructureConfiguration.AddConfiguredDataProtection(builder.Services, builder.Configuration, builder.Environment);
    InfrastructureConfiguration.ConfigureForwardedHeaders(builder.Services, builder.Configuration);
    builder.Services.AddCommunicationsIdentity(builder.Environment);
    builder.Services.AddScoped<OutboxJobProcessor>();
    builder.Services.AddScoped<RetentionCleanupService>();
    builder.Services.AddSingleton<IEmailSender, SmtpEmailSender>();
    builder.Services.AddHostedService<CommunicationsOutboxWorker>();
    builder.Services.AddHostedService<CommunicationsRetentionWorker>();
}

static void MapApiEndpoints(WebApplication app)
{
    app.MapOpenApi().WithDocumentPerVersion();
    app.UseForwardedHeaders();
    app.UseDefaultFiles();
    app.UseStaticFiles();
    app.UseRouting();
    app.UseAuthentication();
    app.UseAuthorization();
    app.UseAntiforgery();

    app.MapAuthEndpoints();
    app.MapVersionedBusinessEndpoints();

    app.Map("/api", () => Results.NotFound());
    app.Map("/api/{**path}", () => Results.NotFound());
    app.Map("/auth", () => Results.NotFound());
    app.Map("/auth/{**path}", () => Results.NotFound());
    app.MapFallbackToFile("index.html");
}

public partial class Program;