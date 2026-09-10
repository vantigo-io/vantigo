using System.Reflection;
using System.Runtime.CompilerServices;
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

                var schemaRegistry = new ContractAnnotations.SchemaIdRegistry();
                var ambiguousSchemaIds = new Lazy<IReadOnlySet<string>>(() => ContractAnnotations.AmbiguousSchemaIds(CandidateSchemaTypes()));

                options.CreateSchemaReferenceId = info =>
                {
                    var id = OpenApiOptions.CreateDefaultSchemaReferenceId(info);
                    if (id is null)
                    {
                        return null;
                    }
                    var final = ModuleAssemblies.Contains(info.Type.Assembly.GetName().Name)
                        ? ContractAnnotations.SchemaId(info.Type, id, ambiguousSchemaIds.Value)
                        : id;
                    schemaRegistry.Claim(info.Type, final);
                    return final;
                };

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
                        var key = ContractAnnotations.SchemaId(type, type.Name, ambiguousSchemaIds.Value);
                        schemaRegistry.Claim(type, key);
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

    /// <summary>
    /// Every type in the module assemblies that can produce a schema — the
    /// population <see cref="ContractAnnotations.AmbiguousSchemaIds"/> scans to
    /// decide which nested-type schema ids still collide after the declaring-type
    /// prefix and need a module prefix too. Excludes generic type definitions and
    /// compiler-generated types (closures, iterator/async state machines, etc.),
    /// which the JSON/OpenAPI pipeline never turns into schemas.
    /// </summary>
    private static IEnumerable<Type> CandidateSchemaTypes() => AppDomain.CurrentDomain.GetAssemblies()
        .Where(assembly => ModuleAssemblies.Contains(assembly.GetName().Name))
        .SelectMany(assembly => assembly.GetTypes())
        .Where(type => !type.IsGenericTypeDefinition)
        .Where(type => type.IsClass || type.IsValueType)
        .Where(type => !typeof(Delegate).IsAssignableFrom(type))
        .Where(type => !IsCompilerGenerated(type))
        .Where(IsPublicOrInternalAtEveryNestingLevel);

    private static IEnumerable<Type> RecordTypes() => CandidateSchemaTypes()
        .Where(type => type is { IsClass: true, IsAbstract: false } or { IsValueType: true, IsEnum: false })
        .Where(type => type.Name.EndsWith("Response", StringComparison.Ordinal) || type.Name.EndsWith("Request", StringComparison.Ordinal))
        .OrderBy(type => type.FullName, StringComparer.Ordinal);

    /// <summary>
    /// True for a top-level type (whatever its own visibility) and for a nested
    /// type that, together with every type it is nested in, is internal or
    /// public — i.e. reachable from the module's own code, which is true of
    /// nearly every endpoint-local Request/Response DTO since endpoint classes
    /// are declared `internal static`.
    /// </summary>
    private static bool IsPublicOrInternalAtEveryNestingLevel(Type type)
    {
        for (var current = type; current is not null && current.IsNested; current = current.DeclaringType)
        {
            if (!(current.IsNestedPublic || current.IsNestedAssembly || current.IsNestedFamORAssem))
            {
                return false;
            }
        }
        return true;
    }

    private static bool IsCompilerGenerated(Type type) => Attribute.IsDefined(type, typeof(CompilerGeneratedAttribute));
}