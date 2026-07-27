using Asp.Versioning;

using Microsoft.EntityFrameworkCore;

using Vantigo.Customers.Api.Database;
using Vantigo.Customers.Api.Endpoints;
using Vantigo.Customers.Api.Endpoints.Lookup;

var builder = WebApplication.CreateBuilder(args);

builder.Services
    .AddApiVersioning(options =>
    {
        options.DefaultApiVersion = new ApiVersion(1);
        options.ApiVersionReader = new UrlSegmentApiVersionReader();
        options.ReportApiVersions = true;
    })
    .AddApiExplorer(options =>
    {
        // Format the group name as "v1" so the OpenAPI documents are exposed at
        // /openapi/v1.json, matching the default Microsoft.AspNetCore.OpenApi behavior.
        options.GroupNameFormat = "'v'VVV";

        // Replace the {version:apiVersion} route template parameter with the actual
        // version number in the generated OpenAPI paths.
        options.SubstituteApiVersionInUrl = true;
    })
    // Asp.Versioning's AddOpenApi must be used (instead of Microsoft.AspNetCore.OpenApi's)
    // to generate versioned OpenAPI documents.
    .AddOpenApi();

builder.Services.AddDbContext<AppDbContext>(options =>
    options.UseNpgsql(builder.Configuration.GetConnectionString("Postgresql")));

// Named client for the open Brønnøysundregisteret (Enhetsregisteret) API used by
// the /lookup/brreg endpoint. The base URL is configurable so tests and other
// environments can point it at a stub.
builder.Services.AddHttpClient(BrregLookupEndpoint.HttpClientName, client =>
{
    client.BaseAddress = new Uri(builder.Configuration["Brreg:BaseUrl"] ?? "https://data.brreg.no");
    client.Timeout = TimeSpan.FromSeconds(5);
});

var app = builder.Build();

if (app.Environment.IsDevelopment())
{
    await using var scope = app.Services.CreateAsyncScope();
    var dbContext = scope.ServiceProvider.GetRequiredService<AppDbContext>();
    await dbContext.Database.MigrateAsync();
}

app.MapOpenApi().WithDocumentPerVersion();

// Serve the built SPA (embedded into wwwroot on publish) from "/". In development
// the frontend runs on the Vite dev server, which proxies /api to this API.
app.UseDefaultFiles();
app.UseStaticFiles();

var api = app.NewVersionedApi()
    .MapGroup("/api/v{version:apiVersion}")
    .HasApiVersion(new ApiVersion(1));

api.MapCustomersEndpoints();
api.MapLookupEndpoints();

// Deep links like /customers must fall back to the SPA entry point. API and
// OpenAPI endpoints match their own routes first and are unaffected.
app.MapFallbackToFile("index.html");

app.Run();

/// <summary>
/// Exposes the implicit <c>Program</c> class to the test project so integration
/// tests can bootstrap the API through <c>WebApplicationFactory</c>.
/// </summary>
public partial class Program;
