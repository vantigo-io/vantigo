using System.Reflection;
using System.Text.Json.Nodes;

using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Mvc.Testing;
using Microsoft.AspNetCore.OpenApi;
using Microsoft.AspNetCore.Routing;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Microsoft.OpenApi;

using Testcontainers.PostgreSql;

namespace Vantigo.Host.Tests.Contract;

/// <summary>
/// Extraction harness for the Go port's API contract (sub-project 2). Boots
/// the host with every module and single tenancy and writes the v1 OpenAPI
/// document — OpenAPI 3.0, every endpoint included, annotated with
/// x-vantigo-access and operationIds, with a component schema for every
/// *Request/*Response type in the module assemblies — to the file named by
/// VANTIGO_OPENAPI_DUMP. Without that variable the test does nothing.
/// Removed with the .NET host at the cutover.
/// </summary>
public sealed class OpenApiContractExtraction
{
    [Fact]
    public async Task ExtractContract()
    {
        var output = Environment.GetEnvironmentVariable("VANTIGO_OPENAPI_DUMP");
        if (string.IsNullOrEmpty(output))
        {
            return;
        }

        await using var factory = new ContractExtractionFactory();
        await factory.StartDatabaseAsync();
        using var client = factory.CreateClient();
        var json = await client.GetStringAsync("/openapi/v1.json");
        await File.WriteAllTextAsync(output, json);
    }
}

internal sealed class ContractExtractionFactory : WebApplicationFactory<Program>
{
    private static readonly string[] ModuleAssemblies =
        ["Vantigo.Identity", "Vantigo.Customers", "Vantigo.Products", "Vantigo.Energy", "Vantigo.Communications", "Vantigo.Contracts"];

    private readonly PostgreSqlContainer _postgres = new PostgreSqlBuilder("postgres:17-alpine")
        .WithDatabase("vantigo")
        .Build();

    private string _runtimeConnectionString = string.Empty;

    public async Task StartDatabaseAsync()
    {
        await _postgres.StartAsync();
        _runtimeConnectionString = await Vantigo.Tenancy.EntityFramework.TenantRuntimeRoleSql
            .ProvisionAsync(_postgres.GetConnectionString());
    }

    public override async ValueTask DisposeAsync()
    {
        await base.DisposeAsync();
        await _postgres.DisposeAsync();
    }

    protected override void ConfigureWebHost(IWebHostBuilder builder)
    {
        builder.UseEnvironment(Environments.Development);
        foreach (var module in new[] { "Customers", "Communications", "Products", "Energy" })
        {
            builder.UseSetting($"Modules:{module}:Enabled", "true");
        }

        builder.ConfigureAppConfiguration((_, configuration) => configuration.AddInMemoryCollection(new Dictionary<string, string?>
        {
            ["ConnectionStrings:vantigo"] = _runtimeConnectionString,
            ["ConnectionStrings:migrations"] = _postgres.GetConnectionString(),
            ["Development:Seed:Enabled"] = "false",
            ["Authentication:Bootstrap:Secret"] = "contract-extraction-bootstrap-secret",
            ["Authentication:PasswordReset:ResetUrl"] = "http://test.local/reset?email={email}&token={token}",
            ["Authentication:Invitations:AcceptUrl"] = "http://test.local/invitations?token={token}",
        }));

        builder.ConfigureServices(services =>
        {
            services.AddSingleton(new HostTestStartupPreparation(ApplyMigrations: true, SeedDevelopmentData: false));
            services.ConfigureAll<OpenApiOptions>(options =>
            {
                options.OpenApiVersion = OpenApiSpecVersion.OpenApi3_0;
                options.ShouldInclude = _ => true;

                options.AddOperationTransformer((operation, context, _) =>
                {
                    var description = context.Description;
                    var metadata = description.ActionDescriptor.EndpointMetadata.ToList();
                    var path = description.RelativePath ?? string.Empty;
                    operation.Extensions ??= new Dictionary<string, IOpenApiExtension>();
                    operation.Extensions["x-vantigo-access"] =
                        new JsonNodeExtension(JsonValue.Create(ContractAnnotations.Access(metadata, path)));
                    operation.OperationId = ContractAnnotations.OperationId(
                        description.HttpMethod ?? "GET",
                        path,
                        metadata.OfType<IEndpointNameMetadata>().FirstOrDefault()?.EndpointName);
                    return Task.CompletedTask;
                });

                options.AddDocumentTransformer(async (document, context, cancellationToken) =>
                {
                    document.Info = new OpenApiInfo { Title = "Vantigo API", Version = "1" };
                    document.Servers = [];

                    ContractAnnotations.EnsureUniqueOperationIds(document.Paths.Values
                        .SelectMany(item => item.Operations is null ? Enumerable.Empty<OpenApiOperation>() : item.Operations.Values)
                        .Select(operation => operation.OperationId ?? string.Empty));

                    document.Components ??= new OpenApiComponents();
                    document.Components.Schemas ??= new Dictionary<string, IOpenApiSchema>();
                    foreach (var type in RecordTypes())
                    {
                        var key = document.Components.Schemas.ContainsKey(type.Name)
                            ? type.Assembly.GetName().Name!.Replace("Vantigo.", string.Empty, StringComparison.Ordinal) + type.Name
                            : type.Name;
                        if (!document.Components.Schemas.ContainsKey(key))
                        {
                            var schema = await context.GetOrCreateSchemaAsync(type, null, cancellationToken);
                            // Tells the splitter (Task 3) which module file an as-yet
                            // unreferenced record schema belongs in.
                            if (schema is OpenApiSchema concrete)
                            {
                                concrete.Extensions ??= new Dictionary<string, IOpenApiExtension>();
                                concrete.Extensions["x-vantigo-source"] =
                                    new JsonNodeExtension(JsonValue.Create(type.Assembly.GetName().Name!));
                            }
                            document.Components.Schemas[key] = schema;
                        }
                    }
                });
            });
        });
    }

    private static IEnumerable<Type> RecordTypes() => AppDomain.CurrentDomain.GetAssemblies()
        .Where(assembly => ModuleAssemblies.Contains(assembly.GetName().Name))
        .SelectMany(assembly => assembly.GetTypes())
        .Where(type => type is { IsClass: true, IsAbstract: false, IsGenericTypeDefinition: false } or { IsValueType: true, IsEnum: false, IsGenericTypeDefinition: false })
        .Where(type => type.Name.EndsWith("Response", StringComparison.Ordinal) || type.Name.EndsWith("Request", StringComparison.Ordinal))
        .Where(type => !type.IsNested || type.DeclaringType?.IsPublic != false)
        .OrderBy(type => type.FullName, StringComparer.Ordinal);
}