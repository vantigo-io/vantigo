using System.Threading.RateLimiting;

using Microsoft.AspNetCore.RateLimiting;

using Vantigo.Customers.Api;
using Vantigo.Customers.Api.Database;
using Vantigo.Customers.Api.Endpoints;
using Vantigo.Customers.Api.Endpoints.Auth;
using Vantigo.Customers.Api.Endpoints.Lookup;
using Vantigo.Customers.Api.Services;

var builder = WebApplication.CreateBuilder(args);
var workforceOidc = WorkforceOidcOptions.Load(builder.Configuration, builder.Environment);

builder.Services.AddCustomerApiVersioning();

builder.Services.AddCustomerDatabases(builder.Configuration);
builder.Services.AddSingleton<BootstrapSecretProvider>();
InfrastructureConfiguration.AddConfiguredDataProtection(builder.Services, builder.Configuration, builder.Environment);
InfrastructureConfiguration.ConfigureForwardedHeaders(builder.Services, builder.Configuration);
builder.Services.AddCustomerIdentity(builder.Environment);
builder.Services.AddWorkforceOidc(workforceOidc, builder.Environment);
builder.Services.Configure<EmailOptions>(builder.Configuration.GetSection("Email"));
if (string.Equals(builder.Configuration["Email:Provider"], "Smtp", StringComparison.OrdinalIgnoreCase))
{
    builder.Services.AddSingleton<IApplicationEmailSender, SmtpApplicationEmailSender>();
}
else
{
    builder.Services.AddSingleton<IApplicationEmailSender, LoggingApplicationEmailSender>();
}
builder.Services.AddCustomerAuthorization();
builder.Services.AddCustomerAntiforgery(builder.Environment);
builder.Services.AddRateLimiter(options =>
{
    options.RejectionStatusCode = StatusCodes.Status429TooManyRequests;
    options.OnRejected = async (context, cancellationToken) =>
    {
        if (context.Lease.TryGetMetadata(MetadataName.RetryAfter, out var retryAfter) && retryAfter is TimeSpan retryAfterDuration)
        {
            context.HttpContext.Response.Headers.RetryAfter = ((int)Math.Ceiling(retryAfterDuration.TotalSeconds)).ToString();
        }

        context.HttpContext.Response.ContentType = "application/json";
        await context.HttpContext.Response.WriteAsJsonAsync(
            new AuthErrorResponse(new AuthError("rate_limited", "Too many authentication attempts. Please try again later.")),
            cancellationToken);
    };
    options.AddPolicy(AuthRateLimitPolicies.Login, context =>
        RateLimitPartition.GetFixedWindowLimiter(
            InfrastructureConfiguration.GetRateLimitPartitionKey(context),
            _ => new FixedWindowRateLimiterOptions
            {
                PermitLimit = 100,
                Window = TimeSpan.FromMinutes(1),
                QueueLimit = 0,
                AutoReplenishment = true,
            }));
    options.AddPolicy(AuthRateLimitPolicies.Bootstrap, context =>
        RateLimitPartition.GetFixedWindowLimiter(
            InfrastructureConfiguration.GetRateLimitPartitionKey(context),
            _ => new FixedWindowRateLimiterOptions
            {
                PermitLimit = 20,
                Window = TimeSpan.FromMinutes(1),
                QueueLimit = 0,
                AutoReplenishment = true,
            }));
    options.AddPolicy(AuthRateLimitPolicies.Invitations, context =>
        RateLimitPartition.GetFixedWindowLimiter(InfrastructureConfiguration.GetRateLimitPartitionKey(context),
            _ => new FixedWindowRateLimiterOptions { PermitLimit = 30, Window = TimeSpan.FromMinutes(1), QueueLimit = 0 }));
    options.AddPolicy(AuthRateLimitPolicies.InvitationAcceptance, context =>
        RateLimitPartition.GetFixedWindowLimiter(InfrastructureConfiguration.GetRateLimitPartitionKey(context),
            _ => new FixedWindowRateLimiterOptions { PermitLimit = 20, Window = TimeSpan.FromMinutes(1), QueueLimit = 0 }));
    options.AddPolicy(AuthRateLimitPolicies.PasswordRecovery, context =>
        RateLimitPartition.GetFixedWindowLimiter(InfrastructureConfiguration.GetRateLimitPartitionKey(context),
            _ => new FixedWindowRateLimiterOptions { PermitLimit = 10, Window = TimeSpan.FromMinutes(15), QueueLimit = 0 }));
    options.AddPolicy(AuthRateLimitPolicies.Mfa, context =>
        RateLimitPartition.GetFixedWindowLimiter(InfrastructureConfiguration.GetRateLimitPartitionKey(context),
            _ => new FixedWindowRateLimiterOptions { PermitLimit = 20, Window = TimeSpan.FromMinutes(5), QueueLimit = 0 }));
});
builder.Services.AddScoped<ICustomerTimelineRecorder, CustomerTimelineRecorder>();

// Named client for the open Brønnøysundregisteret (Enhetsregisteret) API used by
// the /lookup/brreg endpoint. The base URL is configurable so tests and other
// environments can point it at a stub.
builder.Services.AddHttpClient(BrregLookupEndpoint.HttpClientName, client =>
{
    client.BaseAddress = new Uri(builder.Configuration["Brreg:BaseUrl"] ?? "https://data.brreg.no");
    client.Timeout = TimeSpan.FromSeconds(5);
});

var app = builder.Build();
// Force creation at startup so a generated secret is logged exactly once.
_ = app.Services.GetRequiredService<BootstrapSecretProvider>();

// Development keeps the existing convenient startup migration behavior. Other
// environments require the explicit opt-in flag so deploys can run migrations as
// a deliberate operation instead of every application instance racing at startup.
if (app.Environment.IsDevelopment() || builder.Configuration.GetValue<bool>("Database:ApplyMigrationsOnStartup"))
{
    await app.MigrateCustomerDatabasesAsync();
}

app.MapOpenApi().WithDocumentPerVersion();

// Serve the built SPA (embedded into wwwroot on publish) from "/". In development
// the frontend runs on the Vite dev server, which proxies /api to this API.
app.UseForwardedHeaders();
app.UseDefaultFiles();
app.UseStaticFiles();
app.UseRouting();
app.UseRateLimiter();
app.UseAuthentication();
app.UseAuthorization();
app.UseAntiforgery();

app.MapAuthEndpoints(workforceOidc);
app.MapVersionedBusinessEndpoints();

// Never let an unrecognized API/auth URL be mistaken for an SPA deep link.
app.Map("/api", () => Results.NotFound());
app.Map("/api/{**path}", () => Results.NotFound());
app.Map("/auth", () => Results.NotFound());
app.Map("/auth/{**path}", () => Results.NotFound());

// Deep links like /customers must fall back to the SPA entry point. API and
// OpenAPI endpoints match their own routes first and are unaffected.
app.MapFallbackToFile("index.html");

app.Run();

/// <summary>
/// Exposes the implicit <c>Program</c> class to the test project so integration
/// tests can bootstrap the API through <c>WebApplicationFactory</c>.
/// </summary>
public partial class Program;
