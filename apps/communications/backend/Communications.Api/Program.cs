using Vantigo.Communications.Api;
using Vantigo.Communications.Api.Database;
using Vantigo.Communications.Api.Endpoints;
using Vantigo.Communications.Api.Endpoints.Auth;
using Vantigo.Communications.Api.Services;

var builder = WebApplication.CreateBuilder(args);

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

var app = builder.Build();
_ = app.Services.GetRequiredService<BootstrapSecretProvider>();

if (app.Environment.IsDevelopment() || builder.Configuration.GetValue<bool>("Database:ApplyMigrationsOnStartup"))
{
    await app.MigrateCommunicationsDatabasesAsync();
    await app.SeedConfiguredMailboxAsync();
}

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

app.Run();

public partial class Program;
