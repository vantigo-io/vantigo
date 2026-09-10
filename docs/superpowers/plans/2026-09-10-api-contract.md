# API Contract Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Per-module OpenAPI 3.0 files under `openapi/` that describe exactly what the .NET host serves today, proven against exchanges recorded from the .NET integration suites, with Go server interfaces and frontend types generated from them and drift-checked in CI.

**Architecture:** A .NET harness in `Vantigo.Host.Tests` dumps the host's OpenAPI document annotated with `x-vantigo-access`, operationIds and record schemas. A recorder linked into every .NET integration suite captures real request/response pairs. A Go tool splits the dump into module files; curation fills the gaps module by module until every recorded exchange validates (kin-openapi). oapi-codegen generates the Go types and strict server interfaces; openapi-typescript generates the frontend types.

**Tech Stack:** .NET 10 (Microsoft.AspNetCore.OpenApi 10.0.10, Microsoft.OpenApi 2.7.5, xunit, Testcontainers), Go 1.27 (kin-openapi v0.149.0, oapi-codegen v2.8.0 + runtime v1.7.0, gorilla mux router from kin-openapi), openapi-typescript 7.13.0, bun.

**Spec:** `docs/superpowers/specs/2026-09-10-api-contract-design.md`, under the parent `docs/superpowers/specs/2026-09-10-go-backend-port-design.md`.

## Global Constraints

- OpenAPI **3.0.x** in every file under `openapi/` (oapi-codegen v2.8.0 does not support 3.1). The harness sets `OpenApiVersion = OpenApi3_0`.
- Files: `openapi/common.yaml`, `openapi/identity.yaml`, `openapi/customers.yaml`, `openapi/products.yaml`, `openapi/energy.yaml`, `openapi/communications.yaml`. Cross-file references are relative: `common.yaml#/components/schemas/<Name>`.
- Every operation has `operationId` (unique across all files) and `x-vantigo-access` with one of: `anonymous`, `session`, `scim`, `policy:<Name>[+<Name>…]` (names from `ActiveAccount`, `SystemAdmin`, `Owner`, `OwnerManagement`, `Business`, `AuthorizationManagement`, sorted, joined with `+`), `permission:<module>:<verb>[+<module>:<verb>…]` (every listed permission is required — chained `RequirePermission` calls AND together; sorted, joined with `+`).
- Dropped operations (never in `openapi/`): `/api/v1/identity/admin/tenants` and everything under it, `GET /api/v1/identity/tenants/current/capabilities`, `POST /api/v1/identity/session/tenant`, `GET /api/v1/identity/antiforgery`, `POST /api/v1/communications/inbound/mailgun/{channelId}`.
- Paths and JSON shapes are the .NET host's, unchanged. Curation describes; it never redesigns.
- A success response with no body is either a `204` or a 2xx that carries `x-vantigo-empty-body: true` and no `content` (the handler returns `Ok()` with no value). Never document an invented body for it.
- Numeric schemas always carry `type` (`integer` for `int32`/`int64`, `number` for `float`/`double`) without the numeric-string `pattern` the .NET dump adds, and a nullable reference is `allOf: [{$ref: …}]` with `nullable: true` (never a one-element `oneOf`). `contract normalize` applies both; `TestNumericSchemasAreTyped` and the nullable-reference guard in `internal/openapi` enforce them.
- Tool pins: oapi-codegen **v2.8.0**, openapi-typescript **7.13.0**, kin-openapi **v0.149.0**, oapi-codegen runtime **v1.7.0**. CI invokes Go tools with `go run …@<version>`, never through mise's `go:` backend.
- Generated code is committed and never hand-edited; CI regenerates and fails on a diff. Generated `.d.ts` files are excluded from biome.
- The .NET harness and recorder live only in test projects and are removed at the cutover (sub-project 7). No production .NET code changes in this plan.
- Commands assume a mise-activated shell (or prefix `mise exec --`). No gcc locally: `-race` runs in CI or via `golang:1.27` in Docker. Go tests needing PostgreSQL use `docker compose -f docker-compose.test.yml up -d --wait` (this plan adds none).
- Commits follow Conventional Commits and end with the session's `Co-Authored-By` (the authoring model) / `Claude-Session` trailers.

## Deviation from writing-plans convention

Curation tasks (Tasks 6–11) cannot contain their YAML in advance: the content is derived from the harness dump and the .NET endpoint code. Those tasks specify the exact procedure, tools, sources and machine-checked acceptance criteria (the recorded-exchange validation and the structural lint) instead of literal file contents.

## Deviations from the spec, decided here

- **One generated file per module.** The spec names `types.gen.go` and `server.gen.go`; nothing consumes the types separately, so each module gets a single `internal/<module>/gen/api.gen.go` (models + std-http strict server) from one config, and `common.yaml` gets `internal/apicommon/gen/api.gen.go`.
- **oapi-codegen is pinned in `go:generate`, not in mise.** Every `go:generate` line runs `go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0`, so local runs and the CI drift check use the same pin from one place and no `go:` tool goes through mise.
- **Gaps are tracked, then eliminated.** While curation is in progress, `openapi/testdata/known-gaps.txt` lists the operations that do not yet validate. The tests fail on any gap not in the list *and* on any listed operation that now passes, so the list only shrinks; Task 14 deletes it and its support code.

## File Structure

```
apps/host/backend/Vantigo.Host.Tests/Contract/
  ContractAnnotations.cs            pure rules: x-vantigo-access, operationId
  ContractAnnotationsTests.cs       unit tests for the rules
  OpenApiContractExtraction.cs      VANTIGO_OPENAPI_DUMP harness (factory + transformers)
packages/contract-recording/
  ContractRecording.cs              IStartupFilter that records /api exchanges (linked into test projects)
apps/*/backend/*.Tests/*.csproj     MODIFY: link ContractRecording.cs
apps/*/backend/*.Tests/**/*Factory*.cs  MODIFY: services.AddContractRecording()
openapi/
  common.yaml identity.yaml customers.yaml products.yaml energy.yaml communications.yaml
  testdata/exchanges/<module>.jsonl recorded, deduplicated exchanges (committed)
  COVERAGE.md                       operations without a recorded exchange (generated)
apps/server/internal/openapi/
  specs/*.yaml                      embedded copies (go generate)
  openapi.go                        Files(), Load(name), LoadAll()
  openapi_test.go                   drift vs openapi/, validity, x-vantigo-access lint
  exchanges_test.go                 recorded-exchange validation
  cmd/contract/main.go              `split` (dump → module files) and `corpus` (dedupe) and `coverage`
apps/server/internal/apicommon/gen/   oapi-codegen output for common.yaml
apps/server/internal/<module>/gen/    oapi-codegen output per module
apps/server/generate.go             go:generate lines
apps/server/internal/openapi/gen/cfg-*.yaml   oapi-codegen configs
apps/host/frontend/src/api-schema.d.ts, apps/<module>/frontend/src/api-schema.d.ts   openapi-typescript output
tools/openapi/gen-client.ts         runs openapi-typescript per module
package.json                        MODIFY: gen:client script; openapi-typescript devDependency
biome.json                          MODIFY: exclude api-schema.d.ts
mise.toml                           MODIFY: oapi-codegen pin for local go generate
.github/workflows/server-test.yml   MODIFY: Go generate drift
.github/workflows/ci.yml            MODIFY: gen:client drift in the frontend job
CONTRIBUTING.md                     MODIFY: how to change the contract
```

---

### Task 1: Extraction harness

**Files:**
- Create: `apps/host/backend/Vantigo.Host.Tests/Contract/ContractAnnotations.cs`
- Create: `apps/host/backend/Vantigo.Host.Tests/Contract/ContractAnnotationsTests.cs`
- Create: `apps/host/backend/Vantigo.Host.Tests/Contract/OpenApiContractExtraction.cs`

**Interfaces:**
- Produces: `ContractAnnotations.Access(IReadOnlyList<object> metadata, string relativePath) : string`, `ContractAnnotations.OperationId(string httpMethod, string relativePath, string? endpointName) : string`, `ContractAnnotations.EnsureUniqueOperationIds(IEnumerable<string>)` (throws listing duplicates); the test `ExtractContract` that writes the annotated v1 document to `$VANTIGO_OPENAPI_DUMP`.

The planning spike proved this harness boots and dumps (212 operations, OpenAPI 3.0.4, 15 s). This task turns it into the real tool with tested rules.

- [ ] **Step 1: Write the failing rule tests**

Create `apps/host/backend/Vantigo.Host.Tests/Contract/ContractAnnotationsTests.cs`:

```csharp
using Microsoft.AspNetCore.Authorization;

using Vantigo.Contracts.AspNetCore.Authorization;

namespace Vantigo.Host.Tests.Contract;

public sealed class ContractAnnotationsTests
{
    private static string Access(string path, params object[] metadata) => ContractAnnotations.Access(metadata, path);

    [Fact]
    public void Permission_metadata_wins_over_its_backing_policy()
    {
        Assert.Equal("permission:customers:view", Access("api/v1/customers",
            new AuthorizeAttribute("permission:customers:view"),
            new PermissionEndpointConventionExtensions.PermissionMetadata("customers:view")));
    }

    [Fact]
    public void Policies_are_sorted_and_joined()
    {
        Assert.Equal("policy:Owner+OwnerManagement", Access("api/v1/identity/owner/users",
            new AuthorizeAttribute("OwnerManagement"), new AuthorizeAttribute("Owner")));
        Assert.Equal("policy:ActiveAccount", Access("api/v1/identity/account/profile", new AuthorizeAttribute("ActiveAccount")));
    }

    [Fact]
    public void Authorize_without_a_policy_is_session()
    {
        Assert.Equal("session", Access("api/v1/identity/session", new AuthorizeAttribute()));
    }

    [Fact]
    public void Allow_anonymous_and_no_metadata_are_anonymous()
    {
        Assert.Equal("anonymous", Access("api/v1/identity/login", new AllowAnonymousAttribute(), new AuthorizeAttribute("ActiveAccount")));
        Assert.Equal("anonymous", Access("api/v1/identity/login"));
    }

    [Fact]
    public void Scim_paths_are_scim_whatever_their_metadata()
    {
        Assert.Equal("scim", Access("api/v1/identity/scim/v2/Users"));
        Assert.Equal("scim", Access("api/v1/identity/scim/v2/Groups/{id}", new AuthorizeAttribute()));
    }

    [Theory]
    [InlineData("GET", "api/v1/customers", null, "getCustomers")]
    [InlineData("GET", "api/v1/customers/{id}", null, "getCustomersById")]
    [InlineData("PUT", "api/v1/customers/{id}/legal-identity", null, "putCustomersByIdLegalIdentity")]
    [InlineData("DELETE", "api/v1/identity/users/{id:guid}", null, "deleteIdentityUsersById")]
    [InlineData("POST", "api/v1/identity/scim/v2/Users", null, "postIdentityScimV2Users")]
    [InlineData("GET", "api/v1/energy/metering-points/{gsrn}/consumption", null, "getEnergyMeteringPointsByGsrnConsumption")]
    [InlineData("GET", "api/v1/customers/{id}", "GetCustomer", "getCustomer")]
    public void Operation_ids_are_derived_deterministically(string method, string path, string? name, string expected)
    {
        Assert.Equal(expected, ContractAnnotations.OperationId(method, path, name));
    }

    [Fact]
    public void Duplicate_operation_ids_are_rejected_with_every_duplicate_named()
    {
        var error = Assert.Throws<InvalidOperationException>(() =>
            ContractAnnotations.EnsureUniqueOperationIds(["getA", "getB", "getA", "getC", "getB"]));
        Assert.Contains("getA", error.Message);
        Assert.Contains("getB", error.Message);
        Assert.DoesNotContain("getC", error.Message);
    }
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `mise exec -- dotnet test apps/host/backend/Vantigo.Host.Tests/Vantigo.Host.Tests.csproj --filter "FullyQualifiedName~ContractAnnotationsTests"`
Expected: build FAILS with `CS0103: The name 'ContractAnnotations' does not exist in the current context`.

- [ ] **Step 3: Implement the rules**

Create `apps/host/backend/Vantigo.Host.Tests/Contract/ContractAnnotations.cs`:

```csharp
using System.Globalization;
using System.Text;

using Microsoft.AspNetCore.Authorization;

using Vantigo.Contracts.AspNetCore.Authorization;

namespace Vantigo.Host.Tests.Contract;

/// <summary>
/// The rules that turn .NET endpoint metadata into the Go contract's
/// x-vantigo-access and operationId values. Part of the extraction harness for
/// the Go port; removed with the .NET host at the cutover.
/// </summary>
public static class ContractAnnotations
{
    private const string ScimPrefix = "api/v1/identity/scim/v2";
    private const string PermissionPolicyPrefix = "permission:";

    /// <summary>
    /// anonymous | session | scim | policy:A[+B] | permission:module:verb[+module:verb].
    /// SCIM endpoints authenticate with a static bearer token, not a session,
    /// whatever their metadata says. Endpoints with no authorization metadata
    /// at all are public (the host sets no fallback policy).
    /// </summary>
    public static string Access(IReadOnlyList<object> metadata, string relativePath)
    {
        if (relativePath.StartsWith(ScimPrefix, StringComparison.Ordinal))
        {
            return "scim";
        }
        if (metadata.OfType<IAllowAnonymous>().Any())
        {
            return "anonymous";
        }

        var permission = metadata.OfType<PermissionEndpointConventionExtensions.PermissionMetadata>().LastOrDefault();
        if (permission is not null)
        {
            return "permission:" + permission.PermissionKey;
        }

        var authorize = metadata.OfType<IAuthorizeData>().ToList();
        var policies = authorize
            .Select(a => a.Policy)
            .Where(p => !string.IsNullOrEmpty(p) && !p.StartsWith(PermissionPolicyPrefix, StringComparison.Ordinal))
            .Select(p => p!)
            .Distinct(StringComparer.Ordinal)
            .Order(StringComparer.Ordinal)
            .ToList();
        if (policies.Count > 0)
        {
            return "policy:" + string.Join("+", policies);
        }

        return authorize.Count > 0 ? "session" : "anonymous";
    }

    /// <summary>
    /// The endpoint's name in camelCase when it has one; otherwise the HTTP
    /// method followed by the PascalCase path segments after api/v1, with each
    /// route parameter rendered as By{Name} and route constraints dropped.
    /// </summary>
    public static string OperationId(string httpMethod, string relativePath, string? endpointName)
    {
        if (!string.IsNullOrEmpty(endpointName))
        {
            return char.ToLowerInvariant(endpointName[0]) + endpointName[1..];
        }

        var builder = new StringBuilder(httpMethod.ToLowerInvariant());
        foreach (var segment in relativePath.Split('/', StringSplitOptions.RemoveEmptyEntries).SkipWhile(s => s is "api" or "v1"))
        {
            if (segment.StartsWith('{'))
            {
                var name = segment.Trim('{', '}').Split(':')[0];
                builder.Append("By").Append(Pascal(name));
            }
            else
            {
                builder.Append(Pascal(segment));
            }
        }
        return builder.ToString();
    }

    public static void EnsureUniqueOperationIds(IEnumerable<string> operationIds)
    {
        var duplicates = operationIds
            .GroupBy(id => id, StringComparer.Ordinal)
            .Where(group => group.Count() > 1)
            .Select(group => group.Key)
            .Order(StringComparer.Ordinal)
            .ToList();
        if (duplicates.Count > 0)
        {
            throw new InvalidOperationException("Duplicate operationIds: " + string.Join(", ", duplicates));
        }
    }

    private static string Pascal(string segment) => string.Concat(
        segment.Split('-', '_', '.')
            .Where(part => part.Length > 0)
            .Select(part => char.ToUpper(part[0], CultureInfo.InvariantCulture) + part[1..]));
}
```

- [ ] **Step 4: Run the rule tests to verify they pass**

Run: `mise exec -- dotnet test apps/host/backend/Vantigo.Host.Tests/Vantigo.Host.Tests.csproj --filter "FullyQualifiedName~ContractAnnotationsTests"`
Expected: `Passed!  - Failed: 0, Passed: 13` (five facts, the seven-case theory, and the duplicates fact).

- [ ] **Step 5: Write the harness**

Create `apps/host/backend/Vantigo.Host.Tests/Contract/OpenApiContractExtraction.cs`:

```csharp
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
                        .SelectMany(item => item.Operations?.Values ?? [])
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
                                    new JsonNodeExtension(JsonValue.Create(type.Assembly.GetName().Name));
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
```

If a Microsoft.OpenApi 2.7.5 member name differs from the code above (for example `document.Components.Schemas`' value type or `OpenApiPathItem.Operations`' type), fix it to the actual 2.7.5 API. The planning spike confirmed `JsonNodeExtension(JsonNode)`, `OpenApiOperation.Extensions`, `OpenApiOperation.OperationId`, `OpenApiOptions.ShouldInclude`/`OpenApiVersion`/`AddOperationTransformer`/`AddDocumentTransformer`, `OpenApiOperationTransformerContext.Description` and `OpenApiDocumentTransformerContext.GetOrCreateSchemaAsync(Type, ApiParameterDescription?, CancellationToken)`.

- [ ] **Step 6: Run the extraction and check the dump**

Run:

```bash
export VANTIGO_OPENAPI_DUMP=/tmp/vantigo-openapi-v1.json
mise exec -- dotnet test apps/host/backend/Vantigo.Host.Tests/Vantigo.Host.Tests.csproj --filter "FullyQualifiedName~OpenApiContractExtraction"
python3 - <<'EOF'
import json, collections
d = json.load(open('/tmp/vantigo-openapi-v1.json'))
ops = [(p, m, o) for p, i in d['paths'].items() for m, o in i.items() if m in ('get','post','put','patch','delete')]
print("openapi", d['openapi'], "operations", len(ops))
print("missing operationId", sum(1 for *_, o in ops if not o.get('operationId')))
print("access", dict(collections.Counter(o['x-vantigo-access'].split(':')[0] for *_, o in ops)))
print("schemas", len(d['components']['schemas']))
EOF
```

Expected: `openapi 3.0.x operations 212`, `missing operationId 0`, access kinds only among `permission, policy, session, scim, anonymous` (no `none`), and substantially more than the spike's 97 schemas (the record types are added). Keep the dump file: Task 3 consumes it. Do not commit it.

- [ ] **Step 7: Confirm the harness is inert by default and commit**

Run: `mise exec -- dotnet test apps/host/backend/Vantigo.Host.Tests/Vantigo.Host.Tests.csproj` with `VANTIGO_OPENAPI_DUMP` unset.
Expected: all Host tests pass; `ExtractContract` passes instantly without starting a container.

```bash
git add apps/host/backend/Vantigo.Host.Tests/Contract
git commit -m "test(contract): add the OpenAPI extraction harness for the Go port

Dumps the host's v1 document as OpenAPI 3.0 with every endpoint, an
x-vantigo-access rule and an operationId per operation, and a schema for
every Request/Response record, when VANTIGO_OPENAPI_DUMP is set."
```

---

### Task 2: Record real exchanges from the .NET integration suites

**Files:**
- Create: `packages/contract-recording/ContractRecording.cs`
- Create: `apps/host/backend/Vantigo.Host.Tests/Contract/ContractRecordingTests.cs`
- Modify: the six test projects that boot the host — `apps/customers/backend/Customers.Module.Tests/Customers.Module.Tests.csproj`, `apps/identity/backend/Identity.Module.Tests/Identity.Module.Tests.csproj`, `apps/communications/backend/Communications.Module.Tests/Communications.Module.Tests.csproj`, `apps/products/backend/Products.Module.Tests/Products.Module.Tests.csproj`, `apps/energy/backend/Energy.Module.Tests/Energy.Module.Tests.csproj`, `apps/host/backend/Vantigo.Host.Tests/Vantigo.Host.Tests.csproj`
- Modify: every `WebApplicationFactory<…Program>` subclass in those projects (today: `CustomersApiFactory`, `FreshCustomersApiFactory`, the factories inside `DevelopmentSeedIntegrationTests.cs` and `PublicOriginIntegrationTests.cs`, `IdentityApiFactory`, `CommunicationsModuleFactory`, `ProductsModuleFactory`, `EnergyApiFactory`, `HealthEndpointsApiFactory`, the one in `ModuleEndpointActivationTests.cs`; find them with `grep -rln 'WebApplicationFactory<' apps --include='*.cs'`)

**Interfaces:**
- Produces: `Vantigo.Testing.ContractRecording.AddContractRecording(this IServiceCollection)` — a no-op unless `VANTIGO_CONTRACT_RECORD` names a directory; then every `/api/…` request handled by the host appends one JSON line to `<dir>/<process-id>.jsonl`:
  `{"method":"GET","path":"/api/v1/customers/7","query":"?page=1","requestContentType":"application/json","requestBody":"…","status":200,"responseContentType":"application/json; charset=utf-8","responseBody":"…"}`
  Bodies are UTF-8 text for JSON and text content types, omitted (`null`) otherwise, and omitted above 256 KiB. No headers other than the two content types are recorded.

- [ ] **Step 1: Write the failing recorder test**

Create `apps/host/backend/Vantigo.Host.Tests/Contract/ContractRecordingTests.cs`:

```csharp
using System.Net.Http.Json;
using System.Text.Json;

using Microsoft.AspNetCore.Builder;
using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Http;
using Microsoft.AspNetCore.TestHost;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;

using Vantigo.Testing;

namespace Vantigo.Host.Tests.Contract;

[Collection(ContractRecordingCollection.Name)]
public sealed class ContractRecordingTests
{
    private static async Task<IHost> StartAsync()
    {
        var host = new HostBuilder().ConfigureWebHost(web => web
            .UseTestServer()
            .ConfigureServices(services => { services.AddRouting(); services.AddContractRecording(); })
            .Configure(app =>
            {
                app.UseRouting();
                app.UseEndpoints(endpoints =>
                {
                    endpoints.MapPost("/api/v1/echo", async (HttpRequest request) =>
                        Results.Json(new { received = await new StreamReader(request.Body).ReadToEndAsync() }, statusCode: 201));
                    endpoints.MapGet("/health/live", () => Results.Ok());
                });
            })).Build();
        await host.StartAsync();
        return host;
    }

    [Fact]
    public async Task Records_api_exchanges_as_json_lines_and_ignores_other_paths()
    {
        var dir = Directory.CreateTempSubdirectory("contract-recording-").FullName;
        Environment.SetEnvironmentVariable("VANTIGO_CONTRACT_RECORD", dir);
        try
        {
            using var host = await StartAsync();
            var client = host.GetTestClient();
            var response = await client.PostAsJsonAsync("/api/v1/echo?x=1", new { name = "Acme" });
            Assert.Equal(201, (int)response.StatusCode);
            Assert.Contains("Acme", await response.Content.ReadAsStringAsync()); // the client still gets the body
            await client.GetAsync("/health/live");
            await host.StopAsync();

            var lines = Directory.GetFiles(dir, "*.jsonl").SelectMany(File.ReadAllLines).ToList();
            var line = Assert.Single(lines);
            using var json = JsonDocument.Parse(line);
            var root = json.RootElement;
            Assert.Equal("POST", root.GetProperty("method").GetString());
            Assert.Equal("/api/v1/echo", root.GetProperty("path").GetString());
            Assert.Equal("?x=1", root.GetProperty("query").GetString());
            Assert.Equal("{\"name\":\"Acme\"}", root.GetProperty("requestBody").GetString());
            Assert.Equal(201, root.GetProperty("status").GetInt32());
            Assert.StartsWith("application/json", root.GetProperty("responseContentType").GetString());
            Assert.Contains("Acme", root.GetProperty("responseBody").GetString());
        }
        finally
        {
            Environment.SetEnvironmentVariable("VANTIGO_CONTRACT_RECORD", null);
            Directory.Delete(dir, recursive: true);
        }
    }

    [Fact]
    public async Task Does_nothing_without_the_environment_variable()
    {
        Environment.SetEnvironmentVariable("VANTIGO_CONTRACT_RECORD", null);
        using var host = await StartAsync();
        var response = await host.GetTestClient().PostAsJsonAsync("/api/v1/echo", new { name = "Acme" });
        Assert.Equal(201, (int)response.StatusCode);
    }
}

[CollectionDefinition(Name, DisableParallelization = true)]
public sealed class ContractRecordingCollection
{
    public const string Name = "ContractRecording";
}
```

- [ ] **Step 2: Link the (not yet existing) recorder and see the build fail**

Add to `apps/host/backend/Vantigo.Host.Tests/Vantigo.Host.Tests.csproj`, and to the other five test csproj files listed above (same relative depth, so the same path works in all six):

```xml
  <ItemGroup>
    <!-- Records /api exchanges for the Go port's contract when VANTIGO_CONTRACT_RECORD is set. -->
    <Compile Include="..\..\..\..\packages\contract-recording\ContractRecording.cs" Link="Contract\ContractRecording.cs" />
  </ItemGroup>
```

Run: `mise exec -- dotnet test apps/host/backend/Vantigo.Host.Tests/Vantigo.Host.Tests.csproj --filter "FullyQualifiedName~ContractRecordingTests"`
Expected: build FAILS (`CS2001: Source file '…ContractRecording.cs' could not be found` or `CS0246: The type or namespace name 'Vantigo.Testing'…`).

- [ ] **Step 3: Implement the recorder**

Create `packages/contract-recording/ContractRecording.cs`:

```csharp
using System.Text;
using System.Text.Json;

using Microsoft.AspNetCore.Builder;
using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Http;
using Microsoft.Extensions.DependencyInjection;

namespace Vantigo.Testing;

/// <summary>
/// Records every /api request/response exchange the host serves during an
/// integration test run, one JSON line per exchange, into
/// $VANTIGO_CONTRACT_RECORD/&lt;process-id&gt;.jsonl. The Go port validates the
/// recordings against its OpenAPI contract. Linked into the .NET test projects
/// only; removed with the .NET host at the cutover.
/// </summary>
public static class ContractRecording
{
    public const string EnvironmentVariable = "VANTIGO_CONTRACT_RECORD";
    private const int MaxBodyBytes = 256 * 1024;
    private static readonly Lock WriteLock = new();

    public static IServiceCollection AddContractRecording(this IServiceCollection services)
    {
        if (!string.IsNullOrEmpty(Environment.GetEnvironmentVariable(EnvironmentVariable)))
        {
            services.AddTransient<IStartupFilter, RecordingStartupFilter>();
        }
        return services;
    }

    private sealed class RecordingStartupFilter : IStartupFilter
    {
        public Action<IApplicationBuilder> Configure(Action<IApplicationBuilder> next) => app =>
        {
            app.Use(RecordAsync);
            next(app);
        };
    }

    private static async Task RecordAsync(HttpContext context, RequestDelegate next)
    {
        var directory = Environment.GetEnvironmentVariable(EnvironmentVariable);
        if (string.IsNullOrEmpty(directory) || !context.Request.Path.StartsWithSegments("/api"))
        {
            await next(context);
            return;
        }

        context.Request.EnableBuffering();
        var requestBody = await ReadTextAsync(context.Request.Body, context.Request.ContentType);
        context.Request.Body.Position = 0;

        var originalBody = context.Response.Body;
        await using var captured = new MemoryStream();
        context.Response.Body = captured;
        try
        {
            await next(context);
        }
        finally
        {
            context.Response.Body = originalBody;
            captured.Position = 0;
            var responseBody = await ReadTextAsync(captured, context.Response.ContentType);
            captured.Position = 0;
            await captured.CopyToAsync(originalBody);

            var line = JsonSerializer.Serialize(new
            {
                method = context.Request.Method,
                path = context.Request.PathBase.Add(context.Request.Path).Value,
                query = context.Request.QueryString.Value ?? string.Empty,
                requestContentType = context.Request.ContentType,
                requestBody,
                status = context.Response.StatusCode,
                responseContentType = context.Response.ContentType,
                responseBody,
            });
            Directory.CreateDirectory(directory);
            var file = Path.Combine(directory, $"{Environment.ProcessId}.jsonl");
            lock (WriteLock)
            {
                File.AppendAllText(file, line + "\n");
            }
        }
    }

    private static async Task<string?> ReadTextAsync(Stream body, string? contentType)
    {
        if (!IsText(contentType) || (body.CanSeek && body.Length > MaxBodyBytes))
        {
            return null;
        }
        using var reader = new StreamReader(body, Encoding.UTF8, detectEncodingFromByteOrderMarks: false, leaveOpen: true);
        var text = await reader.ReadToEndAsync();
        return text.Length == 0 ? null : text;
    }

    private static bool IsText(string? contentType) =>
        contentType is not null &&
        (contentType.Contains("json", StringComparison.OrdinalIgnoreCase) ||
         contentType.StartsWith("text/", StringComparison.OrdinalIgnoreCase) ||
         contentType.Contains("x-www-form-urlencoded", StringComparison.OrdinalIgnoreCase));
}
```

It is dependency-free on purpose: each test project compiles its own copy, so it may use only ASP.NET Core types every test project already has.

- [ ] **Step 4: Run the recorder tests**

Run: `mise exec -- dotnet test apps/host/backend/Vantigo.Host.Tests/Vantigo.Host.Tests.csproj --filter "FullyQualifiedName~ContractRecordingTests"`
Expected: `Passed!  - Failed: 0, Passed: 2`.

- [ ] **Step 5: Hook every factory**

In each factory listed under **Files**, add `services.AddContractRecording();` as the first statement inside its existing `ConfigureServices(services => { … })` lambda (for factories whose lambda is an expression body, turn it into a block), and add `using Vantigo.Testing;` to the file. A factory without a `ConfigureServices` call (e.g. in `PublicOriginIntegrationTests.cs`) gets `builder.ConfigureServices(services => services.AddContractRecording());` inside its `ConfigureWebHost`.

Run: `mise exec -- dotnet build Vantigo.slnx -c Release` then `mise exec -- dotnet test Vantigo.slnx -c Release --no-build`
Expected: the build succeeds and every test passes (recording is off: the variable is unset).

- [ ] **Step 6: Record the corpus**

Start Docker (Testcontainers), then:

```bash
rm -rf /tmp/vantigo-exchanges && mkdir -p /tmp/vantigo-exchanges
VANTIGO_CONTRACT_RECORD=/tmp/vantigo-exchanges mise exec -- dotnet test Vantigo.slnx -c Release --no-build
cat /tmp/vantigo-exchanges/*.jsonl | wc -l
cat /tmp/vantigo-exchanges/*.jsonl | python3 -c "import sys,json,collections; c=collections.Counter(json.loads(l)['path'].split('/')[3] for l in sys.stdin); print(dict(c))"
```

Expected: every suite passes; thousands of lines spread across `identity`, `customers`, `communications`, `products`, `energy`. Keep `/tmp/vantigo-exchanges` for Task 4 (it is deduplicated before anything is committed). If the one known-flaky test fails, re-run.

- [ ] **Step 7: Commit**

```bash
git add packages/contract-recording apps/*/backend/*.Tests
git commit -m "test(contract): record /api exchanges from the .NET integration suites

A recorder linked into every test project that boots the host appends each
/api request/response pair to \$VANTIGO_CONTRACT_RECORD/<pid>.jsonl. Off
unless the variable is set; the Go port validates its contract against the
recordings."
```

---

### Task 3: Split the dump into per-module contract files

**Files:**
- Create: `apps/server/internal/openapi/cmd/contract/main.go`, `apps/server/internal/openapi/cmd/contract/split.go`, `apps/server/internal/openapi/cmd/contract/split_test.go`
- Create (generated by the tool, then committed): `openapi/common.yaml`, `openapi/identity.yaml`, `openapi/customers.yaml`, `openapi/products.yaml`, `openapi/energy.yaml`, `openapi/communications.yaml`

**Interfaces:**
- Consumes: the Task 1 dump (`/tmp/vantigo-openapi-v1.json`).
- Produces: `go run ./internal/openapi/cmd/contract split -in <dump.json> -out ../../openapi` (from `apps/server`), and the six YAML files. Each module file has `openapi: 3.0.3`, `info.title: "Vantigo <Module> API"`, `info.version: "1"`, `servers: [{url: /}]`, its paths, and the schemas only it uses; a schema used by two or more modules, or sourced from `Vantigo.Contracts`, goes to `common.yaml` and every reference to it becomes `common.yaml#/components/schemas/<Name>`. Unreferenced record schemas go to the module named by their `x-vantigo-source` (`Vantigo.Identity` → identity, …, `Vantigo.Contracts` → common). The dropped operations of the Global Constraints never reach any file.

- [ ] **Step 1: Write the failing splitter test**

Create `apps/server/internal/openapi/cmd/contract/split_test.go`:

```go
package main

import (
	"strings"
	"testing"
)

const dump = `{
 "openapi": "3.0.4",
 "info": {"title": "Vantigo API", "version": "1"},
 "paths": {
  "/api/v1/customers/{id}": {"get": {"operationId": "getCustomersById", "x-vantigo-access": "permission:customers:view",
    "responses": {"200": {"description": "OK", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Customer"}}}},
                  "404": {"description": "NF", "content": {"application/problem+json": {"schema": {"$ref": "#/components/schemas/ProblemDetails"}}}}}}},
  "/api/v1/products": {"get": {"operationId": "getProducts", "x-vantigo-access": "permission:products:view",
    "responses": {"400": {"description": "Bad", "content": {"application/problem+json": {"schema": {"$ref": "#/components/schemas/ProblemDetails"}}}}}}},
  "/api/v1/identity/antiforgery": {"get": {"operationId": "getIdentityAntiforgery", "x-vantigo-access": "anonymous", "responses": {"200": {"description": "OK"}}}},
  "/api/v1/identity/admin/tenants/{id}": {"get": {"operationId": "getIdentityAdminTenantsById", "x-vantigo-access": "policy:SystemAdmin", "responses": {"200": {"description": "OK"}}}}
 },
 "components": {"schemas": {
  "Customer": {"type": "object", "properties": {"address": {"$ref": "#/components/schemas/Address"}}},
  "Address": {"type": "object"},
  "ProblemDetails": {"type": "object"},
  "AuthUserResponse": {"type": "object", "x-vantigo-source": "Vantigo.Identity"},
  "PagedMeta": {"type": "object", "x-vantigo-source": "Vantigo.Contracts"}
 }}
}`

func TestSplit(t *testing.T) {
	files, err := split([]byte(dump))
	if err != nil {
		t.Fatal(err)
	}

	has := func(file, fragment string) {
		t.Helper()
		if !strings.Contains(files[file], fragment) {
			t.Errorf("%s is missing %q:\n%s", file, fragment, files[file])
		}
	}
	lacks := func(file, fragment string) {
		t.Helper()
		if strings.Contains(files[file], fragment) {
			t.Errorf("%s must not contain %q:\n%s", file, fragment, files[file])
		}
	}

	for _, name := range []string{"common.yaml", "identity.yaml", "customers.yaml", "products.yaml", "energy.yaml", "communications.yaml"} {
		has(name, "openapi: 3.0.3")
		has(name, "url: /")
	}
	has("customers.yaml", "/api/v1/customers/{id}:")
	has("customers.yaml", "title: Vantigo Customers API")
	// Used by one module: stays in it, with its nested schema.
	has("customers.yaml", "  Customer:")
	has("customers.yaml", "  Address:")
	has("customers.yaml", "$ref: '#/components/schemas/Customer'")
	// Used by two modules: moves to common and is referenced across files.
	has("common.yaml", "  ProblemDetails:")
	has("customers.yaml", "$ref: common.yaml#/components/schemas/ProblemDetails")
	has("products.yaml", "$ref: common.yaml#/components/schemas/ProblemDetails")
	lacks("customers.yaml", "  ProblemDetails:")
	// Unreferenced record schemas follow x-vantigo-source.
	has("identity.yaml", "  AuthUserResponse:")
	has("common.yaml", "  PagedMeta:")
	// Dropped operations never appear.
	lacks("identity.yaml", "antiforgery")
	lacks("identity.yaml", "admin/tenants")
	// Access and operationIds survive.
	has("customers.yaml", "x-vantigo-access: permission:customers:view")
	has("customers.yaml", "operationId: getCustomersById")
}

func TestSplitRejectsAnUnknownModule(t *testing.T) {
	_, err := split([]byte(`{"openapi":"3.0.4","info":{"title":"x","version":"1"},"paths":{"/api/v1/warehouse/x":{"get":{"responses":{"200":{"description":"OK"}}}}}}`))
	if err == nil || !strings.Contains(err.Error(), "warehouse") {
		t.Fatalf("err = %v, want it to name the unknown module", err)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd apps/server && go test ./internal/openapi/cmd/contract/`
Expected: FAIL to compile with `undefined: split`.

- [ ] **Step 3: Implement the splitter**

Create `apps/server/internal/openapi/cmd/contract/main.go`:

```go
// Command contract maintains the Go port's OpenAPI contract files:
//
//	contract split    -in <dump.json> -out <dir>     split the .NET dump into per-module files
//	contract corpus   -in <dir> -spec <dir> -out <dir>  deduplicate recorded exchanges (Task 4)
//	contract coverage -spec <dir> -corpus <dir> -out <file>  list operations without exchanges (Task 4)
//
// Run from apps/server. See docs/superpowers/plans/2026-09-10-api-contract.md.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: contract <split|corpus|coverage> [flags]")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "split":
		err = runSplit(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", os.Args[1])
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runSplit(args []string) error {
	fs := flag.NewFlagSet("split", flag.ContinueOnError)
	in := fs.String("in", "", "the .NET OpenAPI dump (JSON)")
	out := fs.String("out", "", "the directory to write the module files into")
	if err := fs.Parse(args); err != nil {
		return err
	}
	data, err := os.ReadFile(*in)
	if err != nil {
		return err
	}
	files, err := split(data)
	if err != nil {
		return err
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(*out, name), []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}
```

Create `apps/server/internal/openapi/cmd/contract/split.go`:

```go
package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/oasdiff/yaml"
)

var modules = []string{"identity", "customers", "products", "energy", "communications"}

// dropped are removed while splitting: the Go port has no tenancy, no
// antiforgery token endpoint and no inbound Mailgun webhook.
var dropped = []*regexp.Regexp{
	regexp.MustCompile(`^/api/v1/identity/admin/tenants(/|$)`),
	regexp.MustCompile(`^/api/v1/identity/tenants/current/capabilities$`),
	regexp.MustCompile(`^/api/v1/identity/session/tenant$`),
	regexp.MustCompile(`^/api/v1/identity/antiforgery$`),
	regexp.MustCompile(`^/api/v1/communications/inbound/mailgun/`),
}

var modulePath = regexp.MustCompile(`^/api/v1/([a-z-]+)(/|$)`)

const localRef = "#/components/schemas/"

type obj = map[string]any

// split turns the .NET dump into common.yaml plus one file per module.
func split(dump []byte) (map[string]string, error) {
	var doc obj
	if err := json.Unmarshal(dump, &doc); err != nil {
		return nil, fmt.Errorf("parse dump: %w", err)
	}
	schemas, _ := dig(doc, "components", "schemas").(obj)

	moduleOf := map[string]string{}
	paths := map[string]obj{}
	for _, m := range modules {
		paths[m] = obj{}
	}
	for p, item := range asObj(doc["paths"]) {
		if isDropped(p) {
			continue
		}
		match := modulePath.FindStringSubmatch(p)
		if match == nil || paths[match[1]] == nil {
			return nil, fmt.Errorf("path %s belongs to no known module", p)
		}
		paths[match[1]][p] = asObj(item)
	}

	// Which modules reference each schema (transitively)?
	users := map[string]map[string]bool{}
	for _, m := range modules {
		for name := range closure(paths[m], schemas) {
			if users[name] == nil {
				users[name] = map[string]bool{}
			}
			users[name][m] = true
		}
	}
	for name, schema := range schemas {
		switch {
		case len(users[name]) > 1:
			moduleOf[name] = "common"
		case len(users[name]) == 1:
			for m := range users[name] {
				moduleOf[name] = m
			}
		default:
			moduleOf[name] = sourceModule(asObj(schema))
		}
		if sourceModule(asObj(schema)) == "common" {
			moduleOf[name] = "common"
		}
	}
	// A schema a common schema depends on must be common too.
	for changed := true; changed; {
		changed = false
		for name, m := range moduleOf {
			if m != "common" {
				continue
			}
			for dep := range refsIn(schemas[name]) {
				if moduleOf[dep] != "common" {
					moduleOf[dep] = "common"
					changed = true
				}
			}
		}
	}

	files := map[string]string{}
	for _, target := range append([]string{"common"}, modules...) {
		own := obj{}
		for name, m := range moduleOf {
			if m == target {
				own[name] = rewrite(schemas[name], target, moduleOf)
			}
		}
		out := obj{
			"openapi": "3.0.3",
			"info":    obj{"title": title(target), "version": "1"},
			"servers": []any{obj{"url": "/"}},
			"paths":   obj{},
		}
		if target != "common" {
			rewritten := obj{}
			for p, item := range paths[target] {
				rewritten[p] = rewrite(item, target, moduleOf)
			}
			out["paths"] = rewritten
		}
		if len(own) > 0 {
			out["components"] = obj{"schemas": own}
		}
		data, err := json.Marshal(out)
		if err != nil {
			return nil, err
		}
		y, err := yaml.JSONToYAML(data)
		if err != nil {
			return nil, err
		}
		files[target+".yaml"] = string(y)
	}
	return files, nil
}

func isDropped(p string) bool {
	for _, re := range dropped {
		if re.MatchString(p) {
			return true
		}
	}
	return false
}

func title(module string) string {
	if module == "common" {
		return "Vantigo API — shared components"
	}
	return "Vantigo " + strings.ToUpper(module[:1]) + module[1:] + " API"
}

func sourceModule(schema obj) string {
	source, _ := schema["x-vantigo-source"].(string)
	name := strings.ToLower(strings.TrimPrefix(source, "Vantigo."))
	for _, m := range modules {
		if m == name {
			return m
		}
	}
	return "common"
}

// closure returns every schema name reachable from v.
func closure(v any, schemas obj) map[string]bool {
	seen := map[string]bool{}
	var walk func(any)
	walk = func(v any) {
		for name := range refsIn(v) {
			if !seen[name] {
				seen[name] = true
				walk(schemas[name])
			}
		}
	}
	walk(v)
	return seen
}

func refsIn(v any) map[string]bool {
	found := map[string]bool{}
	var walk func(any)
	walk = func(v any) {
		switch t := v.(type) {
		case obj:
			if ref, ok := t["$ref"].(string); ok && strings.HasPrefix(ref, localRef) {
				found[strings.TrimPrefix(ref, localRef)] = true
			}
			for _, child := range t {
				walk(child)
			}
		case []any:
			for _, child := range t {
				walk(child)
			}
		}
	}
	walk(v)
	return found
}

// rewrite deep-copies v, turning references to schemas that live in another
// file into cross-file references.
func rewrite(v any, target string, moduleOf map[string]string) any {
	switch t := v.(type) {
	case obj:
		out := obj{}
		for k, child := range t {
			if k == "$ref" {
				if ref, ok := child.(string); ok && strings.HasPrefix(ref, localRef) {
					name := strings.TrimPrefix(ref, localRef)
					if home := moduleOf[name]; home != target {
						child = home + ".yaml" + localRef + name
					}
				}
				out[k] = child
				continue
			}
			out[k] = rewrite(child, target, moduleOf)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, child := range t {
			out[i] = rewrite(child, target, moduleOf)
		}
		return out
	default:
		return v
	}
}

func dig(v any, keys ...string) any {
	for _, k := range keys {
		v = asObj(v)[k]
	}
	return v
}

func asObj(v any) obj {
	o, _ := v.(obj)
	return o
}
```

Add the dependency: `cd apps/server && go get github.com/oasdiff/yaml@latest && go mod tidy` (it is kin-openapi's own YAML library; `JSONToYAML` emits keys sorted, which keeps the files diff-stable).

- [ ] **Step 4: Run the splitter tests**

Run: `cd apps/server && go test ./internal/openapi/cmd/contract/ -v -count=1 && golangci-lint run`
Expected: both tests PASS; `0 issues.`

- [ ] **Step 5: Split the real dump and inspect**

Run:

```bash
mkdir -p openapi
cd apps/server && go run ./internal/openapi/cmd/contract split -in /tmp/vantigo-openapi-v1.json -out ../../openapi && cd ../..
wc -l openapi/*.yaml
grep -c 'operationId:' openapi/*.yaml
grep -rn 'antiforgery\|admin/tenants\|mailgun\|session/tenant\|tenants/current' openapi/ || echo "no dropped operations"
```

Expected: six files; operation counts summing to 202 (212 minus the 10 dropped); `no dropped operations`.

- [ ] **Step 6: Commit**

```bash
git add apps/server/go.mod apps/server/go.sum apps/server/internal/openapi/cmd openapi
git commit -m "feat(contract): split the extracted contract into per-module files

The contract tool's split command turns the .NET dump into common.yaml plus
one OpenAPI 3.0 file per module, drops the tenancy, antiforgery and Mailgun
operations, and moves shared schemas into common.yaml with cross-file refs.
The files are the raw starting point for curation."
```

---

### Task 4: The Go contract package, the corpus and the gap tracking

**Files:**
- Create: `apps/server/generate.go`
- Create: `apps/server/internal/openapi/openapi.go`, `apps/server/internal/openapi/lint.go`, `apps/server/internal/openapi/exchanges.go`
- Create: `apps/server/internal/openapi/openapi_test.go`, `apps/server/internal/openapi/exchanges_test.go`
- Create (by `go generate`): `apps/server/internal/openapi/specs/*.yaml`
- Modify: `apps/server/internal/openapi/cmd/contract/main.go` (add `corpus` and `coverage`); Create: `apps/server/internal/openapi/cmd/contract/corpus.go`
- Create (by the tools, then committed): `openapi/testdata/exchanges/<module>.jsonl`, `openapi/testdata/known-gaps.txt`, `openapi/COVERAGE.md`

**Interfaces:**
- Consumes: the Task 3 module files; the Task 2 recordings in `/tmp/vantigo-exchanges`.
- Produces:
  - `openapi.Modules = []string{"identity", "customers", "products", "energy", "communications"}`
  - `openapi.Files() fs.FS` — the embedded spec files (`common.yaml` + one per module)
  - `openapi.Load(ctx, name) (*openapi3.T, error)` — loads `<name>.yaml` with its `common.yaml` refs resolved from the embedded files
  - `openapi.Lint(doc *openapi3.T) []Problem` where `Problem{OperationID, Message string}` — access rule missing/malformed, missing operationId, no 2xx response with a body schema or 204
  - `openapi.Exchange` (the recorded JSON line) and `openapi.Validate(ctx, doc, ex) error` — request + response validation, undocumented statuses rejected
  - `contract corpus -in <raw dir> -out <openapi/testdata/exchanges>` and `contract coverage -out <openapi/COVERAGE.md>`
  - `openapi/testdata/known-gaps.txt` — one `operationId` per line, sorted; rewritten by running the tests with `CONTRACT_UPDATE_GAPS=1`

- [ ] **Step 1: Wire go:generate and embed the specs**

Create `apps/server/generate.go`:

```go
// Command go generate refreshes everything derived from the contract in
// ../../openapi. Run from apps/server:
//
//	go generate ./...
//
// Generated files are committed; CI regenerates them and fails on a diff.
package server

// go:embed cannot reach above the module root, so internal/openapi embeds a
// copy of the contract files. openapi/ stays the single source of truth; the
// drift test in internal/openapi fails if the copy is stale.
//go:generate sh -c "rm -f internal/openapi/specs/*.yaml && cp ../../openapi/*.yaml internal/openapi/specs/"
```

Run: `cd apps/server && mkdir -p internal/openapi/specs && go generate ./... && ls internal/openapi/specs`
Expected: the six YAML files.

- [ ] **Step 2: Write the failing package tests**

Create `apps/server/internal/openapi/openapi_test.go`:

```go
package openapi

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

const repoContract = "../../../../openapi"

// TestEmbeddedSpecsMatchTheContract guards the go:generate copy against
// drifting from openapi/, the single source of truth.
func TestEmbeddedSpecsMatchTheContract(t *testing.T) {
	root, err := filepath.Glob(filepath.Join(repoContract, "*.yaml"))
	if err != nil || len(root) == 0 {
		t.Fatalf("no contract files in %s: %v", repoContract, err)
	}
	embedded, _ := fs.Glob(Files(), "*.yaml")
	if len(embedded) != len(root) {
		t.Fatalf("embedded %v, contract %v — run go generate ./... from apps/server", embedded, root)
	}
	for _, path := range root {
		want, _ := os.ReadFile(path)
		got, err := fs.ReadFile(Files(), filepath.Base(path))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s has drifted from its embedded copy — run go generate ./... from apps/server", filepath.Base(path))
		}
	}
}

func TestEveryModuleLoadsAndValidates(t *testing.T) {
	for _, name := range Modules {
		doc, err := Load(context.Background(), name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.HasPrefix(doc.OpenAPI, "3.0.") {
			t.Errorf("%s: openapi %q, want 3.0.x", name, doc.OpenAPI)
		}
		if err := doc.Validate(context.Background(), openapi3.EnableExamplesValidation()); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestOperationIDsAreUniqueAcrossModules(t *testing.T) {
	seen := map[string]string{}
	for _, name := range Modules {
		doc, err := Load(context.Background(), name)
		if err != nil {
			t.Fatal(err)
		}
		for _, op := range operations(doc) {
			if other, dup := seen[op.OperationID]; dup {
				t.Errorf("operationId %s in both %s and %s", op.OperationID, other, name)
			}
			seen[op.OperationID] = name
		}
	}
}

func TestLintFlagsTheStructuralRules(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromData([]byte(`
openapi: 3.0.3
info: {title: t, version: "1"}
paths:
  /a:
    get:
      operationId: good
      x-vantigo-access: permission:customers:view
      responses: {"200": {description: ok, content: {application/json: {schema: {type: object}}}}}
    post:
      operationId: noBody
      x-vantigo-access: session
      responses: {"200": {description: ok}}
    put:
      operationId: badAccess
      x-vantigo-access: policy:Owner+Nobody
      responses: {"204": {description: ok}}
    delete:
      responses: {"204": {description: ok}}
    patch:
      operationId: redirects
      x-vantigo-access: anonymous
      responses: {"302": {description: found, headers: {Location: {schema: {type: string}}}}}
  /b:
    get:
      operationId: redirectsNowhere
      x-vantigo-access: anonymous
      responses: {"302": {description: found}}
    post:
      operationId: emptyOk
      x-vantigo-access: session
      responses: {"200": {description: ok, x-vantigo-empty-body: true}}
`))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, p := range Lint(doc) {
		got[p.OperationID] = p.Message
	}
	for _, id := range []string{"good", "redirects", "emptyOk"} {
		if _, bad := got[id]; bad {
			t.Errorf("%s flagged: %v", id, got[id])
		}
	}
	for _, id := range []string{"noBody", "badAccess", "redirectsNowhere", ""} {
		if _, ok := got[id]; !ok {
			t.Errorf("operation %q not flagged (got %v)", id, got)
		}
	}
}

var accessRule = regexp.MustCompile(`^(anonymous|session|scim|permission:[a-z]+:[a-z-]+(\+[a-z]+:[a-z-]+)*|policy:(ActiveAccount|SystemAdmin|Owner|OwnerManagement|Business|AuthorizationManagement)(\+(ActiveAccount|SystemAdmin|Owner|OwnerManagement|Business|AuthorizationManagement))*)$`)

func TestAccessRuleIsTheContractGrammar(t *testing.T) {
	if accessRule.String() != AccessRule.String() {
		t.Fatalf("AccessRule drifted from the grammar in the plan's Global Constraints")
	}
}
```

Create `apps/server/internal/openapi/exchanges_test.go`:

```go
package openapi

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

const (
	corpusDir = "../../../../openapi/testdata/exchanges"
	gapsFile  = "../../../../openapi/testdata/known-gaps.txt"
)

// TestRecordedExchangesMatchTheContract validates every exchange recorded
// from the .NET suites against the module that owns its path, and checks
// every module against the structural lint. Operations listed in
// known-gaps.txt may fail; anything else failing — or a listed operation
// that now passes — fails the test. CONTRACT_UPDATE_GAPS=1 rewrites the list.
func TestRecordedExchangesMatchTheContract(t *testing.T) {
	ctx := context.Background()
	failing := map[string][]string{} // operationId (or path) -> reasons

	for _, name := range Modules {
		doc, err := Load(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range Lint(doc) {
			failing[p.OperationID] = append(failing[p.OperationID], p.Message)
		}
		for _, ex := range readCorpus(t, filepath.Join(corpusDir, name+".jsonl")) {
			if id, err := Validate(ctx, doc, ex); err != nil {
				failing[id] = append(failing[id], ex.Method+" "+ex.Path+" "+err.Error())
			}
		}
	}

	var ids []string
	for id := range failing {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	if os.Getenv("CONTRACT_UPDATE_GAPS") == "1" {
		if err := os.WriteFile(gapsFile, []byte(strings.Join(ids, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %d known gaps", len(ids))
		return
	}

	known := map[string]bool{}
	if data, err := os.ReadFile(gapsFile); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			if line != "" {
				known[line] = true
			}
		}
	}
	for _, id := range ids {
		if !known[id] {
			t.Errorf("%s does not match the contract:\n  %s", id, strings.Join(failing[id], "\n  "))
		}
	}
	for id := range known {
		if _, still := failing[id]; !still {
			t.Errorf("%s now matches the contract — remove it from openapi/testdata/known-gaps.txt", id)
		}
	}
}

const validateSpec = `
openapi: 3.0.3
info: {title: t, version: "1"}
servers: [{url: /}]
paths:
  /api/v1/things:
    post:
      operationId: postThings
      x-vantigo-access: session
      requestBody:
        required: true
        content:
          application/json:
            schema: {type: object, required: [name], properties: {name: {type: string}}}
      responses:
        "201":
          description: created
          content:
            application/json:
              schema: {type: object, required: [id], properties: {id: {type: integer}}}
        "400":
          description: invalid
          content:
            application/problem+json:
              schema: {type: object, required: [title], properties: {title: {type: string}}}
`

func TestValidate(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromData([]byte(validateSpec))
	if err != nil {
		t.Fatal(err)
	}
	str := func(s string) *string { return &s }
	appJSON, problem := str("application/json"), str("application/problem+json")
	post := func(body string, status int, contentType *string, response string) Exchange {
		return Exchange{Method: "POST", Path: "/api/v1/things", RequestContentType: appJSON, RequestBody: str(body),
			Status: status, ResponseContentType: contentType, ResponseBody: str(response)}
	}
	cases := []struct {
		name string
		ex   Exchange
		ok   bool
	}{
		{"valid exchange", post(`{"name":"a"}`, 201, appJSON, `{"id":1}`), true},
		{"undocumented status", post(`{"name":"a"}`, 409, problem, `{"title":"conflict"}`), false},
		{"response off contract", post(`{"name":"a"}`, 201, appJSON, `{"id":"x"}`), false},
		{"invalid request the server rejected", post(`{}`, 400, problem, `{"title":"bad"}`), true},
		{"invalid request the server accepted", post(`{}`, 201, appJSON, `{"id":1}`), false},
		{"rejection off contract", post(`{}`, 400, problem, `{}`), false},
		{"no matching operation", Exchange{Method: "GET", Path: "/api/v1/nothing", Status: 404}, false},
	}
	for _, c := range cases {
		if _, err := Validate(context.Background(), doc, c.ex); (err == nil) != c.ok {
			t.Errorf("%s: err = %v, want ok = %v", c.name, err, c.ok)
		}
	}
}

func readCorpus(t *testing.T, path string) []Exchange {
	t.Helper()
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var out []Exchange
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<22)
	for scanner.Scan() {
		var ex Exchange
		if err := json.Unmarshal(scanner.Bytes(), &ex); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		out = append(out, ex)
	}
	return out
}
```

- [ ] **Step 3: Run them to verify they fail**

Run: `cd apps/server && go test ./internal/openapi/`
Expected: FAIL to compile (`undefined: Files`, `undefined: Load`, `undefined: Lint`, `undefined: Validate`, `undefined: Exchange`, `undefined: AccessRule`, `undefined: operations`).

- [ ] **Step 4: Implement the package**

Create `apps/server/internal/openapi/openapi.go`:

```go
// Package openapi owns the embedded copy of the API contract (openapi/ at the
// repository root) and loads it for validation. The contract is the single
// source of truth for the Go server's routes and types and for the frontend's
// types; see docs/superpowers/specs/2026-09-10-api-contract-design.md.
package openapi

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// Modules are the contract files that carry paths; common.yaml only holds
// shared components.
var Modules = []string{"identity", "customers", "products", "energy", "communications"}

//go:embed specs/*.yaml
var specs embed.FS

// Files is the embedded contract, rooted at the spec files.
func Files() fs.FS {
	sub, err := fs.Sub(specs, "specs")
	if err != nil {
		panic("openapi: embedded specs missing: " + err.Error())
	}
	return sub
}

// Load loads <name>.yaml with its references into common.yaml resolved from
// the embedded files.
func Load(ctx context.Context, name string) (*openapi3.T, error) {
	files := Files()
	loader := openapi3.NewLoader()
	loader.Context = ctx
	loader.IsExternalRefsAllowed = true
	loader.ReadFromURIFunc = func(_ *openapi3.Loader, location *url.URL) ([]byte, error) {
		return fs.ReadFile(files, path.Clean(strings.TrimPrefix(location.Path, "/")))
	}
	data, err := fs.ReadFile(files, name+".yaml")
	if err != nil {
		return nil, fmt.Errorf("openapi: %w", err)
	}
	doc, err := loader.LoadFromDataWithPath(data, &url.URL{Path: name + ".yaml"})
	if err != nil {
		return nil, fmt.Errorf("openapi: load %s: %w", name, err)
	}
	return doc, nil
}

// operation is one method on one path.
type operation struct {
	Path, Method, OperationID string
	Op                        *openapi3.Operation
}

func operations(doc *openapi3.T) []operation {
	var out []operation
	for p, item := range doc.Paths.Map() {
		for method, op := range item.Operations() {
			out = append(out, operation{Path: p, Method: method, OperationID: op.OperationID, Op: op})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Method < out[j].Method
	})
	return out
}
```

Create `apps/server/internal/openapi/lint.go`:

```go
package openapi

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
)

// AccessRule is the grammar of x-vantigo-access.
var AccessRule = regexp.MustCompile(`^(anonymous|session|scim|permission:[a-z]+:[a-z-]+(\+[a-z]+:[a-z-]+)*|policy:(ActiveAccount|SystemAdmin|Owner|OwnerManagement|Business|AuthorizationManagement)(\+(ActiveAccount|SystemAdmin|Owner|OwnerManagement|Business|AuthorizationManagement))*)$`)

// Problem is one structural rule an operation breaks.
type Problem struct {
	OperationID string
	Message     string
}

// Lint checks the rules every operation must meet: an operationId, a valid
// x-vantigo-access, and a documented success response (see hasSuccessResponse).
func Lint(doc *openapi3.T) []Problem {
	var problems []Problem
	for _, op := range operations(doc) {
		where := strings.ToUpper(op.Method) + " " + op.Path
		if op.OperationID == "" {
			problems = append(problems, Problem{"", where + ": no operationId"})
		}
		access, _ := op.Op.Extensions["x-vantigo-access"].(string)
		if !AccessRule.MatchString(access) {
			problems = append(problems, Problem{op.OperationID, fmt.Sprintf("%s: x-vantigo-access %q is not valid", where, access)})
		}
		if !hasSuccessResponse(op.Op) {
			problems = append(problems, Problem{op.OperationID, where + ": no 2xx response with a body schema, 204, or 3xx with a Location header"})
		}
	}
	return problems
}

// hasSuccessResponse reports whether op documents how it succeeds: a 2xx with
// a body schema, a 204, a 2xx explicitly marked `x-vantigo-empty-body: true`
// (the handler returns Ok() with no value — marked, so an uncurated bare
// "200 OK" still fails), or — for redirect endpoints such as the OIDC flow —
// a 3xx that declares its Location header.
func hasSuccessResponse(op *openapi3.Operation) bool {
	if op.Responses == nil {
		return false
	}
	for code, ref := range op.Responses.Map() {
		if ref == nil || ref.Value == nil {
			continue
		}
		switch {
		case code == "204":
			return true
		case strings.HasPrefix(code, "2"):
			if len(ref.Value.Content) == 0 && ref.Value.Extensions["x-vantigo-empty-body"] == true {
				return true
			}
			for _, media := range ref.Value.Content {
				if media != nil && media.Schema != nil {
					return true
				}
			}
		case strings.HasPrefix(code, "3"):
			if _, ok := ref.Value.Headers["Location"]; ok {
				return true
			}
		}
	}
	return false
}
```

Create `apps/server/internal/openapi/exchanges.go`:

```go
package openapi

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/gorillamux"
)

// Exchange is one request/response pair recorded from the .NET integration
// suites (packages/contract-recording). Bodies are nil when they were not
// text or were too large to record.
type Exchange struct {
	Method              string  `json:"method"`
	Path                string  `json:"path"`
	Query               string  `json:"query"`
	RequestContentType  *string `json:"requestContentType"`
	RequestBody         *string `json:"requestBody"`
	Status              int     `json:"status"`
	ResponseContentType *string `json:"responseContentType"`
	ResponseBody        *string `json:"responseBody"`
}

// Validate checks ex against doc and returns the operationId it matched (the
// path when it matched none). Undocumented response statuses are errors.
func Validate(ctx context.Context, doc *openapi3.T, ex Exchange) (string, error) {
	router, err := gorillamux.NewRouter(doc)
	if err != nil {
		return ex.Path, err
	}
	req := httptest.NewRequest(ex.Method, ex.Path+ex.Query, strings.NewReader(deref(ex.RequestBody)))
	if ex.RequestContentType != nil {
		req.Header.Set("Content-Type", *ex.RequestContentType)
	}
	route, params, err := router.FindRoute(req)
	if err != nil {
		return ex.Method + " " + ex.Path, fmt.Errorf("no operation matches: %w", err)
	}

	options := &openapi3filter.Options{
		AuthenticationFunc:    openapi3filter.NoopAuthenticationFunc,
		IncludeResponseStatus: true,
		MultiError:            true,
		ExcludeRequestBody:    ex.RequestBody == nil,
		ExcludeResponseBody:   ex.ResponseBody == nil,
	}
	input := &openapi3filter.RequestValidationInput{Request: req, PathParams: params, Route: route, Options: options}
	id := route.Operation.OperationID
	// The .NET suites send invalid requests on purpose. A request the contract
	// rejects is consistent only when the server rejected it too (4xx); the
	// rejection response must then still match what the contract documents.
	// Never loosen a schema to make such an exchange pass.
	if err := openapi3filter.ValidateRequest(ctx, input); err != nil && (ex.Status < 400 || ex.Status >= 500) {
		return id, fmt.Errorf("request the contract rejects was answered %d: %w", ex.Status, err)
	}
	header := http.Header{}
	if ex.ResponseContentType != nil {
		header.Set("Content-Type", *ex.ResponseContentType)
	}
	if err := openapi3filter.ValidateResponse(ctx, &openapi3filter.ResponseValidationInput{
		RequestValidationInput: input,
		Status:                 ex.Status,
		Header:                 header,
		Body:                   io.NopCloser(bytes.NewReader([]byte(deref(ex.ResponseBody)))),
		Options:                options,
	}); err != nil {
		return id, fmt.Errorf("response %d: %w", ex.Status, err)
	}
	return id, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
```

Add the dependency: `cd apps/server && go get github.com/getkin/kin-openapi@v0.149.0 && go mod tidy`.

Building a router per exchange is slow for thousands of exchanges; if the test takes more than a few seconds, cache one router per document inside `Validate` (a package-level `sync.Map` keyed by `*openapi3.T`).

- [ ] **Step 5: Run the package tests (the corpus is still empty)**

Run: `cd apps/server && go test ./internal/openapi/ -run 'TestEmbedded|TestEveryModule|TestOperationIDs|TestLint|TestAccessRule|TestValidate' -v -count=1`
Expected: all PASS (if kin-openapi has no body decoder for `application/problem+json`, register `openapi3filter.JSONBodyDecoder` for it in an `init()` in exchanges.go — the .NET host answers errors with that content type). If `TestEveryModuleLoadsAndValidates` fails on the raw split files, fix the *splitter* (Task 3 code) or add the smallest structural fix to the YAML, and say which in the report — the raw dump must at least be a valid OpenAPI 3.0 document set before curation starts.

- [ ] **Step 6: Add the corpus and coverage commands**

Create `apps/server/internal/openapi/cmd/contract/corpus.go`:

```go
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/gorillamux"

	"github.com/vantigo-io/vantigo/server/internal/openapi"
)

// perKey caps how many exchanges are kept per operation and status.
const perKey = 3

// runCorpus deduplicates the raw recordings into one committed JSONL file
// per module: at most perKey exchanges per (operation, status), 5xx dropped,
// exchanges on dropped operations discarded, and requests to routes that do
// not exist (matching no operation, answered 4xx — the suites probe unknown
// paths and API versions) discarded. Any other exchange that matches no
// operation is an error: a path the contract lost.
func runCorpus(args []string) error {
	fs := flag.NewFlagSet("corpus", flag.ContinueOnError)
	in := fs.String("in", "", "directory of raw *.jsonl recordings")
	out := fs.String("out", "", "openapi/testdata/exchanges")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()
	routersByModule := map[string]routers.Router{}
	for _, name := range openapi.Modules {
		doc, err := openapi.Load(ctx, name)
		if err != nil {
			return err
		}
		r, err := gorillamux.NewRouter(doc)
		if err != nil {
			return err
		}
		routersByModule[name] = r
	}

	raw, err := filepath.Glob(filepath.Join(*in, "*.jsonl"))
	if err != nil {
		return err
	}
	kept := map[string][]string{} // module -> lines
	counts := map[string]int{}    // module|operationId|status -> kept
	var unmatched []string
	for _, file := range raw {
		f, err := os.Open(file)
		if err != nil {
			return err
		}
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 1<<20), 1<<22)
		for scanner.Scan() {
			line := scanner.Text()
			var ex openapi.Exchange
			if err := json.Unmarshal([]byte(line), &ex); err != nil {
				_ = f.Close()
				return fmt.Errorf("%s: %w", file, err)
			}
			if ex.Status >= 500 || isDropped(ex.Path) {
				continue
			}
			rejected := ex.Status >= 400 && ex.Status < 500
			match := modulePath.FindStringSubmatch(ex.Path)
			if match == nil || routersByModule[match[1]] == nil {
				if !rejected {
					unmatched = append(unmatched, ex.Method+" "+ex.Path)
				}
				continue
			}
			route, _, err := routersByModule[match[1]].FindRoute(httptest.NewRequest(ex.Method, ex.Path, nil))
			if err != nil {
				if !rejected {
					unmatched = append(unmatched, ex.Method+" "+ex.Path)
				}
				continue
			}
			key := fmt.Sprintf("%s|%s|%d", match[1], route.Operation.OperationID, ex.Status)
			if counts[key] >= perKey {
				continue
			}
			counts[key]++
			kept[match[1]] = append(kept[match[1]], line)
		}
		_ = f.Close()
		if err := scanner.Err(); err != nil {
			return err
		}
	}
	if len(unmatched) > 0 {
		sort.Strings(unmatched)
		return fmt.Errorf("%d recorded exchanges match no contract operation, e.g. %s", len(unmatched), strings.Join(unmatched[:min(5, len(unmatched))], "; "))
	}
	if err := os.MkdirAll(*out, 0o755); err != nil {
		return err
	}
	for _, name := range openapi.Modules {
		lines := kept[name]
		sort.Strings(lines) // deterministic output
		if err := os.WriteFile(filepath.Join(*out, name+".jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// runCoverage writes the operations that no recorded exchange exercises.
func runCoverage(args []string) error {
	fs := flag.NewFlagSet("coverage", flag.ContinueOnError)
	corpus := fs.String("corpus", "", "openapi/testdata/exchanges")
	out := fs.String("out", "", "openapi/COVERAGE.md")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx := context.Background()
	var b strings.Builder
	b.WriteString("# Contract coverage\n\nOperations no recorded .NET exchange exercises. Their contract comes from the endpoint code alone; review them by hand. Regenerate with `go run ./internal/openapi/cmd/contract coverage` from apps/server.\n")
	total, uncovered := 0, 0
	for _, name := range openapi.Modules {
		doc, err := openapi.Load(ctx, name)
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		r, err := gorillamux.NewRouter(doc)
		if err != nil {
			return err
		}
		data, _ := os.ReadFile(filepath.Join(*corpus, name+".jsonl"))
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			var ex openapi.Exchange
			if line == "" || json.Unmarshal([]byte(line), &ex) != nil {
				continue
			}
			if route, _, err := r.FindRoute(httptest.NewRequest(ex.Method, ex.Path, nil)); err == nil {
				seen[route.Operation.OperationID] = true
			}
		}
		var missing []string
		for p, item := range doc.Paths.Map() {
			for method, op := range item.Operations() {
				total++
				if !seen[op.OperationID] {
					missing = append(missing, fmt.Sprintf("- `%s %s` (%s)", method, p, op.OperationID))
				}
			}
		}
		sort.Strings(missing)
		uncovered += len(missing)
		fmt.Fprintf(&b, "\n## %s (%d uncovered)\n\n%s\n", name, len(missing), strings.Join(missing, "\n"))
	}
	fmt.Fprintf(&b, "\nTotal: %d of %d operations have no recorded exchange.\n", uncovered, total)
	return os.WriteFile(*out, []byte(b.String()), 0o644)
}
```

`modulePath` and `isDropped` come from `split.go` in the same package.

In `main.go`, add the two commands to the switch:

```go
	case "corpus":
		err = runCorpus(os.Args[2:])
	case "coverage":
		err = runCoverage(os.Args[2:])
```

- [ ] **Step 7: Build the corpus, the gap list and the coverage report**

Run:

```bash
cd apps/server
go run ./internal/openapi/cmd/contract corpus -in /tmp/vantigo-exchanges -out ../../openapi/testdata/exchanges
wc -l ../../openapi/testdata/exchanges/*.jsonl; du -sh ../../openapi/testdata/exchanges
go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md
tail -1 ../../openapi/COVERAGE.md
CONTRACT_UPDATE_GAPS=1 go test ./internal/openapi/ -run TestRecordedExchanges -count=1 -v | tail -3
wc -l ../../openapi/testdata/known-gaps.txt
go test ./internal/openapi/... -count=1 && golangci-lint run
```

Expected: the corpus command succeeds (if it reports exchanges matching no operation, that is a real gap — a path the split lost; fix the splitter, re-run Task 3's split, and re-run this step); a few hundred lines per module and a corpus of at most a few MB; the coverage report's total line; a gap list of roughly the 142 operations without response schemas plus any whose recorded exchanges do not yet validate; every `internal/openapi` test PASS; `0 issues.`

Scan the corpus once for anything that is not test data (`grep -iE 'password|secret|token' ../../openapi/testdata/exchanges/*.jsonl | head`): the .NET suites use fixed fake credentials, which are fine to commit; stop and report if anything looks like a real secret.

- [ ] **Step 8: Commit**

```bash
git add apps/server/generate.go apps/server/go.mod apps/server/go.sum apps/server/internal/openapi openapi
git commit -m "feat(contract): validate the contract against recorded .NET exchanges

internal/openapi embeds the contract, lints every operation (operationId,
x-vantigo-access, a documented success body) and validates the deduplicated
exchanges recorded from the .NET suites with kin-openapi. known-gaps.txt
lists what curation still has to fix; it may only shrink."
```

---

### Task 5: Prove oapi-codegen across files (common + energy)

**Files:**
- Create: `apps/server/internal/openapi/gen/cfg-common.yaml`, `apps/server/internal/openapi/gen/cfg-energy.yaml`
- Modify: `apps/server/generate.go`
- Create (generated): `apps/server/internal/apicommon/gen/api.gen.go`, `apps/server/internal/energy/gen/api.gen.go`
- Create: `apps/server/internal/energy/gen/gen_test.go`

**Interfaces:**
- Produces: `internal/apicommon/gen` (package `gen`, the shared component types) and `internal/energy/gen` (package `gen`: models, `StrictServerInterface`, `NewStrictHandler`, `HandlerFromMux`/`HandlerWithOptions` for net/http). References from `energy.yaml` into `common.yaml` compile as imports of `internal/apicommon/gen`.

This proves the riskiest part of the Go side before any curation: cross-file `$ref`s through `import-mapping`.

- [ ] **Step 1: Write the configs**

Create `apps/server/internal/openapi/gen/cfg-common.yaml`:

```yaml
# oapi-codegen: the shared components in openapi/common.yaml. Other modules'
# configs map common.yaml to this package via import-mapping.
package: gen
output: internal/apicommon/gen/api.gen.go
generate:
  models: true
output-options:
  skip-prune: true
```

Create `apps/server/internal/openapi/gen/cfg-energy.yaml`:

```yaml
# oapi-codegen: models and the strict net/http server interface for
# openapi/energy.yaml. References into common.yaml become imports of
# internal/apicommon/gen.
package: gen
output: internal/energy/gen/api.gen.go
generate:
  models: true
  std-http-server: true
  strict-server: true
import-mapping:
  common.yaml: github.com/vantigo-io/vantigo/server/internal/apicommon/gen
output-options:
  skip-prune: true
```

- [ ] **Step 2: Add the generate lines**

Append to `apps/server/generate.go`:

```go
// The contract's generated Go code. oapi-codegen is pinned here, in one place,
// and runs the same way locally and in CI's drift check.
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-common.yaml ../../openapi/common.yaml
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-energy.yaml ../../openapi/energy.yaml
```

- [ ] **Step 3: Write a compile-level test**

Create `apps/server/internal/energy/gen/gen_test.go`:

```go
package gen

import (
	"net/http"
	"testing"
)

// The generated strict server must mount on a std-lib mux; this fails to
// compile if the generator's output or its common.yaml import mapping breaks.
func TestGeneratedServerMountsOnServeMux(t *testing.T) {
	var server StrictServerInterface // nil: only the types are under test
	handler := HandlerFromMux(NewStrictHandler(server, nil), http.NewServeMux())
	if handler == nil {
		t.Fatal("HandlerFromMux returned nil")
	}
}
```

- [ ] **Step 4: Generate, build and test**

Run:

```bash
cd apps/server
mkdir -p internal/apicommon/gen internal/energy/gen
go generate ./... && go mod tidy
go build ./... && go test ./internal/energy/... ./internal/openapi/... -count=1 && golangci-lint run
grep -c 'apicommon/gen' internal/energy/gen/api.gen.go
```

Expected: generation succeeds; the build and tests pass; the energy file imports `internal/apicommon/gen` at least once (energy's error responses reference the shared problem schema). If oapi-codegen rejects the raw energy schemas, record the exact error and fix `energy.yaml` minimally (this is the first taste of Task 6's curation) — do not change the generator. golangci-lint must stay clean: exclude generated files by adding to `apps/server/.golangci.yml`:

```yaml
  exclusions:
    paths:
      - internal/.*/gen
```

(under the existing `linters:` key, as Pjokk does for its generated packages).

- [ ] **Step 5: Commit**

```bash
git add apps/server
git commit -m "build(contract): generate Go code from the contract, starting with energy

oapi-codegen v2.8.0 runs from go:generate; common.yaml becomes
internal/apicommon/gen and module files import it through import-mapping,
proven here on energy before curation starts."
```

---

### Tasks 6–11: Curation

Each curation task owns one slice of the contract and ends when every operation in that slice passes the lint and every recorded exchange for it validates. The procedure is repeated in each task because each task is handed to its implementer alone.

### Task 6: Curate the energy contract

**Files:**
- Modify: `openapi/energy.yaml` (and `openapi/common.yaml` only to add a shared component energy needs)
- Modify: `openapi/testdata/known-gaps.txt` (remove energy's entries — never add any)
- Regenerate: `apps/server/internal/openapi/specs/*`, `apps/server/internal/energy/gen/api.gen.go`, `openapi/COVERAGE.md`

**Interfaces:**
- Consumes: the Task 4 lint and exchange validation; the record schemas the harness put in `energy.yaml`/`common.yaml`; the endpoint code in `apps/energy/backend/Energy.Module/Endpoints/`.
- Produces: an `energy.yaml` whose 20 operations all pass `openapi.Lint` and validate against every recorded energy exchange.

Energy is the smallest module with the fewest gaps (all 20 operations already had 2xx schemas in the spike), so it establishes the conventions the later tasks follow.

- [ ] **Step 1: Make energy's gaps visible**

Delete every energy operationId from `openapi/testdata/known-gaps.txt` (they match `^(get|post|put|patch|delete)Energy`; the test names any stragglers). Run: `cd apps/server && go generate ./... && go test ./internal/openapi/ -run TestRecordedExchanges -count=1`
Expected: FAIL, listing each energy operation that breaks a lint rule or has a recorded exchange that does not validate, with the reason.

- [ ] **Step 2: Curate, operation by operation**

For each failing operation:
1. Find the handler: `grep -rn '"<route fragment>"' apps/energy/backend/Energy.Module --include='*.cs'` and read it to the end, including every `TypedResults.*`/`Results.*` it can return.
2. Document every status the handler returns: 2xx with the body schema (reference the generated record schema, e.g. `$ref: '#/components/schemas/MeteringPointResponse'`, or `common.yaml#/components/schemas/<Name>` for shared ones), `204` with no content, and each error status with `application/problem+json` referencing the shared problem schema (`ProblemDetails`, or `HttpValidationProblemDetails` for 400s with field errors) as the recorded exchanges show.
3. Add or fix the request body (`required: true` when the handler requires one) and the path/query parameters, with the types .NET binds (`format: uuid` for `Guid`, `format: date-time` for `DateTimeOffset`, `format: int64` for `long`).
4. Where the endpoint code and a recorded exchange disagree, the recording wins; note the case in your report.

Rules: never change a path, a property name, a status code or an enum value — the contract describes what .NET serves. Mark properties `nullable: true` when .NET serializes `null`. Keep `x-vantigo-access` and `operationId` exactly as extracted. Delete record schemas nothing references once you are done; keep `energy.yaml` in the key order the splitter produced (sorted) so diffs stay readable. A recorded exchange whose request the contract rejects passes when the server answered 4xx and the documented rejection response matches; never loosen a request schema, parameter or `required` list to accept an invalid recorded request — document the rejection response instead. New or edited schemas follow the Global Constraints' numeric and nullable-reference conventions (`type: integer`/`number`; `allOf` + `nullable: true`); the guard tests fail otherwise.

- [ ] **Step 3: Verify**

Run:

```bash
cd apps/server
go generate ./... && go build ./... && go test ./internal/openapi/... ./internal/energy/... -count=1 && golangci-lint run
grep -cE '^(get|post|put|patch|delete)Energy' ../../openapi/testdata/known-gaps.txt || true
go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md
```

Expected: every test PASS (no energy failures, no stale gap entries), the energy generated code still builds, `0` energy lines left in `known-gaps.txt`, `0 issues.`

- [ ] **Step 4: Commit**

```bash
git add openapi apps/server
git commit -m "feat(contract): curate the energy contract

Every energy operation documents its responses, request bodies and
parameters as the .NET host serves them, and every recorded energy
exchange validates."
```

---

### Task 7: Curate the products contract

**Files:**
- Modify: `openapi/products.yaml` (and `openapi/common.yaml` only to add a shared component)
- Modify: `openapi/testdata/known-gaps.txt` (remove products' entries — never add any)
- Regenerate: `apps/server/internal/openapi/specs/*`, `openapi/COVERAGE.md`

**Interfaces:**
- Consumes: the Task 4 lint and exchange validation; the conventions Task 6 established in `energy.yaml` (read it first); the endpoint code in `apps/products/backend/Products.Module/Endpoints/`.
- Produces: a `products.yaml` whose 26 operations all pass the lint and validate against every recorded products exchange.

- [ ] **Step 1: Make products' gaps visible**

Delete every products operationId from `openapi/testdata/known-gaps.txt` (they match `^(get|post|put|patch|delete)Products`, plus the named operations `getProduct`, `getCategory` and `getTaxCategory`, whose operationIds come from endpoint names). Run: `cd apps/server && go generate ./... && go test ./internal/openapi/ -run TestRecordedExchanges -count=1`
Expected: FAIL, listing each products operation that breaks a lint rule or has a recorded exchange that does not validate.

- [ ] **Step 2: Curate, operation by operation**

For each failing operation:
1. Find the handler: `grep -rn '"<route fragment>"' apps/products/backend/Products.Module --include='*.cs'` and read it to the end, including every result it can return.
2. Document every status: 2xx with the body schema (reference the generated record schema, or `common.yaml#/components/schemas/<Name>`), `204` with no content, and each error status with `application/problem+json` referencing the shared problem schema, as the recorded exchanges show.
3. Add or fix request bodies (`required: true` when required) and parameters with the types .NET binds (`format: uuid`, `format: date-time`, `format: int64`, `format: decimal` is not OpenAPI — prices are `type: number`).
4. Where code and a recorded exchange disagree, the recording wins; note it in your report.

Rules: never change a path, property name, status code or enum value. Variant `optionValues` is a JSON object of string to string (`additionalProperties: {type: string}`), as `docs/products.md` describes. `nullable: true` where .NET serializes `null`. Keep `x-vantigo-access` and `operationId` as extracted. Delete unreferenced record schemas; keep keys sorted. A recorded exchange whose request the contract rejects passes when the server answered 4xx and the documented rejection response matches; never loosen a request schema, parameter or `required` list to accept an invalid recorded request — document the rejection response instead. New or edited schemas follow the Global Constraints' numeric and nullable-reference conventions (`type: integer`/`number`; `allOf` + `nullable: true`); the guard tests fail otherwise.

- [ ] **Step 3: Verify**

Run:

```bash
cd apps/server
go generate ./... && go build ./... && go test ./internal/openapi/... -count=1 && golangci-lint run
grep -cE '^((get|post|put|patch|delete)Products|getProduct$|getCategory$|getTaxCategory$)' ../../openapi/testdata/known-gaps.txt || true
go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md
```

Expected: every test PASS, `0` products lines left in `known-gaps.txt`, `0 issues.`

- [ ] **Step 4: Commit**

```bash
git add openapi apps/server
git commit -m "feat(contract): curate the products contract"
```

---

### Task 8: Curate the customers contract

**Files:**
- Modify: `openapi/customers.yaml` (and `openapi/common.yaml` only to add a shared component)
- Modify: `openapi/testdata/known-gaps.txt` (remove customers' entries — never add any)
- Regenerate: `apps/server/internal/openapi/specs/*`, `openapi/COVERAGE.md`

**Interfaces:**
- Consumes: the Task 4 lint and exchange validation; the conventions in `energy.yaml` and `products.yaml`; the endpoint code in `apps/customers/backend/Customers.Module/Endpoints/`; `CustomersEndpointsTests.cs`' record structs (`Customer`, `CustomerList`, `Pagination`, `ValidationProblem`) as a second source of truth for shapes.
- Produces: a `customers.yaml` whose 29 operations all pass the lint and validate against every recorded customers exchange.

- [ ] **Step 1: Make customers' gaps visible**

Delete every customers operationId from `openapi/testdata/known-gaps.txt` (`^(get|post|put|patch|delete)Customers`, plus the named operations `getCustomer` and `getContact`, whose operationIds come from endpoint names). Run: `cd apps/server && go generate ./... && go test ./internal/openapi/ -run TestRecordedExchanges -count=1`
Expected: FAIL, listing each customers operation that breaks a lint rule or whose recorded exchanges do not validate.

- [ ] **Step 2: Curate, operation by operation**

For each failing operation:
1. Find the handler: `grep -rn '"<route fragment>"' apps/customers/backend/Customers.Module --include='*.cs'` and read it to the end, including every result it can return (the Brreg lookup endpoint also returns the upstream-unavailable status it maps to).
2. Document every status: 2xx with the body schema (reference the generated record schema, or a `common.yaml` one — the paginated list envelope `{data, pagination}` is shared with products and belongs in `common.yaml`; the harness module-prefixed `PaginationMetadata` because three CLR types share the name — merge `CustomersPaginationMetadata`, `ProductsPaginationMetadata` and `EnergyPaginationMetadata` into one `common.yaml#/components/schemas/PaginationMetadata` if their JSON shapes are identical, otherwise keep them apart and say why), `204` with no content, and each error status with `application/problem+json` referencing the shared problem schema, as the recorded exchanges show.
3. Add or fix request bodies and parameters with the types .NET binds (`format: int64` for customer numbers, `format: uuid` for contact ids).
4. Where code and a recording disagree, the recording wins; note it.

Rules: never change a path, property name, status code or enum value. `nullable: true` where .NET serializes `null`. Keep `x-vantigo-access` and `operationId` as extracted. Delete unreferenced record schemas; keep keys sorted. A recorded exchange whose request the contract rejects passes when the server answered 4xx and the documented rejection response matches; never loosen a request schema, parameter or `required` list to accept an invalid recorded request — document the rejection response instead. New or edited schemas follow the Global Constraints' numeric and nullable-reference conventions (`type: integer`/`number`; `allOf` + `nullable: true`); the guard tests fail otherwise.

- [ ] **Step 3: Verify**

Run:

```bash
cd apps/server
go generate ./... && go build ./... && go test ./internal/openapi/... -count=1 && golangci-lint run
grep -cE '^((get|post|put|patch|delete)Customers|getCustomer$|getContact$)' ../../openapi/testdata/known-gaps.txt || true
go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md
```

Expected: every test PASS, `0` customers lines left, `0 issues.`

- [ ] **Step 4: Commit**

```bash
git add openapi apps/server
git commit -m "feat(contract): curate the customers contract"
```

---

### Task 9: Curate the communications contract

**Files:**
- Modify: `openapi/communications.yaml` (and `openapi/common.yaml` only to add a shared component)
- Modify: `openapi/testdata/known-gaps.txt` (remove communications' entries — never add any)
- Regenerate: `apps/server/internal/openapi/specs/*`, `openapi/COVERAGE.md`

**Interfaces:**
- Consumes: the Task 4 lint and exchange validation; the conventions in the already-curated files; the endpoint code in `apps/communications/backend/Communications.Module/Endpoints/`; `docs/communications.md` for the attachment and delivery models.
- Produces: a `communications.yaml` whose 28 operations (29 minus the dropped Mailgun webhook) all pass the lint and validate against every recorded communications exchange.

- [ ] **Step 1: Make communications' gaps visible**

Delete every communications operationId from `openapi/testdata/known-gaps.txt` (`^(get|post|put|patch|delete)Communications`; the test names any stragglers). Run: `cd apps/server && go generate ./... && go test ./internal/openapi/ -run TestRecordedExchanges -count=1`
Expected: FAIL, listing each communications operation that breaks a lint rule or whose recorded exchanges do not validate (26 lacked response schemas in the spike).

- [ ] **Step 2: Curate, operation by operation**

For each failing operation:
1. Find the handler: `grep -rn '"<route fragment>"' apps/communications/backend/Communications.Module --include='*.cs'` and read it to the end, including every result it can return.
2. Document every status: 2xx with the body schema (reference the generated record schema, or a `common.yaml` one), `204` with no content, and each error status with `application/problem+json` referencing the shared problem schema, as the recorded exchanges show.
3. Request bodies: attachment upload is `multipart/form-data` (document the parts the handler reads: the file part as `type: string, format: binary`, plus any fields); attachment download is `application/octet-stream` (`type: string, format: binary`). Parameters with the types .NET binds.
4. Where code and a recording disagree, the recording wins; note it.

Rules: never change a path, property name, status code or enum value. Attachment `scanStatus`/`ready` fields stay (sub-project 5 decides their future). Communications' paginated lists carry the same pagination metadata as customers and products (`page`, `pageSize`, `totalCount`, `totalPages`, `hasNextPage`, `hasPreviousPage`, all required): reference `common.yaml#/components/schemas/PaginationMetadata` (Task 8 moves it there) instead of adding a module copy. `nullable: true` where .NET serializes `null`. Keep `x-vantigo-access` and `operationId` as extracted. Delete unreferenced record schemas; keep keys sorted. A recorded exchange whose request the contract rejects passes when the server answered 4xx and the documented rejection response matches; never loosen a request schema, parameter or `required` list to accept an invalid recorded request — document the rejection response instead. New or edited schemas follow the Global Constraints' numeric and nullable-reference conventions (`type: integer`/`number`; `allOf` + `nullable: true`); the guard tests fail otherwise.

- [ ] **Step 3: Verify**

Run:

```bash
cd apps/server
go generate ./... && go build ./... && go test ./internal/openapi/... -count=1 && golangci-lint run
grep -cE '^(get|post|put|patch|delete)Communications' ../../openapi/testdata/known-gaps.txt || true
go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md
```

Expected: every test PASS, `0` communications lines left, `0 issues.`

- [ ] **Step 4: Commit**

```bash
git add openapi apps/server
git commit -m "feat(contract): curate the communications contract"
```

---

### Task 10: Curate the identity contract — sign-in, session and account

**Files:**
- Modify: `openapi/identity.yaml` (and `openapi/common.yaml` only to add a shared component)
- Modify: `openapi/testdata/known-gaps.txt` (remove this slice's entries — never add any)
- Regenerate: `apps/server/internal/openapi/specs/*`, `openapi/COVERAGE.md`

**Interfaces:**
- Consumes: the Task 4 lint and exchange validation; the conventions in the already-curated files; `apps/identity/backend/Identity.Module/Endpoints/Auth/AuthEndpoints.cs`, `AuthAccountEndpoints.cs`, `AccountSettingsEndpoints.cs`, `SessionEndpoints.cs`, `WorkforceOidcEndpoints.cs`, `SystemMaintenanceEndpoints.cs`, and the response records in `AuthModels.cs`.
- Produces: every identity operation whose path is under `/api/v1/identity/` **except** `/owner/…`, `/access/…`, `/scim/…`, and the invitation-management and authorization-management routes (those are Task 11) passes the lint and validates against its recorded exchanges. That is: bootstrap and bootstrap-status, setup, login (password, 2FA, passkey begin/complete), logout, session, providers, OIDC challenge/complete, password recovery request/reset, invitation validate/accept, account profile/password/avatar/MFA/recovery codes/passkeys/sessions, system status and maintenance.

- [ ] **Step 1: Make this slice's gaps visible**

Delete from `openapi/testdata/known-gaps.txt` every identity operationId in this slice (identity entries match `^(get|post|put|patch|delete)Identity`; leave every `…IdentityOwner…`, `…IdentityAccess…` and `…IdentityScim…` entry for Task 11 — invitation management lives under `/owner/invitations` and authorization management under `/access`, while the public `/invitations/accept` and `/invitations/validate` are this task's). Run: `cd apps/server && go generate ./... && go test ./internal/openapi/ -run TestRecordedExchanges -count=1`
Expected: FAIL, listing each operation in this slice that breaks a lint rule or whose recorded exchanges do not validate.

- [ ] **Step 2: Curate, operation by operation**

For each failing operation:
1. Find the handler in the files named above and read it to the end. Identity handlers return `Task<IResult>` with `TypedResults.Ok(<Record>)`, `TypedResults.Json(…, statusCode: …)` and an `Error(status, code, message)` helper; the records live in `AuthModels.cs` and their generated schemas are already in `identity.yaml`.
2. Document every status: 2xx with the record schema, `204` with no content, and each error status with the body the `Error` helper actually writes — check its implementation and the recordings; the helpers in `AuthEndpoints.cs`, `AccountSettingsEndpoints.cs` and `AuthAccountEndpoints.cs` (and the rate limiter's 429) write `{"error":{"code","message","fields"}}`, which is `common.yaml#/components/schemas/AuthErrorResponse` — reference it, do not add a copy; `SystemMaintenanceEndpoints.cs`' helper writes a bare `{"code","message"}` 400 — add that shape once to `identity.yaml` as `CodeMessageError` (Task 11's `IdentityControlPlaneEndpoints.cs` writes it too) — plus 429 rate-limit responses where the endpoint has a rate-limit policy (body `{"error":{"code":"rate_limited","message":…}}`, header `Retry-After`).
3. The OIDC challenge/complete endpoints redirect: document `302` with a `Location` header (Task 4 added the middleware-served `GET`/`POST /api/v1/identity/oidc/callback` the same way; refine its description if needed). Avatar download is binary (`image/*`, `type: string, format: binary`); avatar upload is whatever content type the handler reads.
4. Request bodies and parameters with the types .NET binds; where code and a recording disagree, the recording wins; note it.

Rules: never change a path, property name, status code or enum value. `nullable: true` where .NET serializes `null`. Keep `x-vantigo-access` and `operationId` as extracted. Delete unreferenced record schemas only once Task 11 is done too (they may be shared) — leave them for now. A recorded exchange whose request the contract rejects passes when the server answered 4xx and the documented rejection response matches; never loosen a request schema, parameter or `required` list to accept an invalid recorded request — document the rejection response instead. New or edited schemas follow the Global Constraints' numeric and nullable-reference conventions (`type: integer`/`number`; `allOf` + `nullable: true`); the guard tests fail otherwise.

- [ ] **Step 3: Verify**

Run:

```bash
cd apps/server
go generate ./... && go build ./... && go test ./internal/openapi/... -count=1 && golangci-lint run
grep -E '^(get|post|put|patch|delete)Identity' ../../openapi/testdata/known-gaps.txt | grep -vE '^(get|post|put|patch|delete)Identity(Owner|Access|Scim)' || echo "slice clean"
go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md
```

Expected: every test PASS, `slice clean`, `0 issues.` (The split is by path prefix: `/owner/…`, `/access/…` and `/scim/…` are Task 11's; the goal is that no operation in *this* slice remains listed.)

- [ ] **Step 4: Commit**

```bash
git add openapi apps/server
git commit -m "feat(contract): curate identity sign-in, session and account"
```

---

### Task 11: Curate the identity contract — administration and SCIM

**Files:**
- Modify: `openapi/identity.yaml` (and `openapi/common.yaml` only to add a shared component)
- Modify: `openapi/testdata/known-gaps.txt` (remove the remaining identity entries — never add any)
- Regenerate: `apps/server/internal/openapi/specs/*`, `openapi/COVERAGE.md`

**Interfaces:**
- Consumes: the Task 4 lint and exchange validation; Task 10's conventions for identity (`AuthErrorResponse` for the `{"error":{…}}` helpers, `CodeMessageError` for the bare `{code, message}` body, rate-limit responses); `apps/identity/backend/Identity.Module/Endpoints/Auth/AuthorizationManagementEndpoints.cs`, `IdentityControlPlaneEndpoints.cs`, `ScimProtocolEndpoints.cs`, the owner/user/invitation-management handlers in `AuthAccountEndpoints.cs`, and `Services/ScimProtocolService.cs`.
- Produces: an `identity.yaml` in which every remaining operation — owner user management, invitation management, MFA reset, access (`/access/me`), roles, permissions catalog, access groups and their members and role mappings, delegations, the authorization audit, and the 13 SCIM endpoints — passes the lint and validates; `known-gaps.txt` holds no identity entries.

- [ ] **Step 1: Make the remaining identity gaps visible**

Delete every remaining identity operationId from `openapi/testdata/known-gaps.txt`. Run: `cd apps/server && go generate ./... && go test ./internal/openapi/ -run TestRecordedExchanges -count=1`
Expected: FAIL, listing each remaining identity operation that breaks a lint rule or whose recorded exchanges do not validate.

- [ ] **Step 2: Curate, operation by operation**

For each failing operation:
1. Find the handler in the files named above and read it to the end, including every result it can return.
2. Document every status: 2xx with the record schema, `204`, and each error status with the body it writes (`AuthErrorResponse`, `CodeMessageError` for `IdentityControlPlaneEndpoints.cs`' bare `{code, message}` helper, or the problem schema, as Task 10 established), 409s from the authorization mutation conflicts, and 429s where a rate-limit policy applies.
3. SCIM endpoints speak SCIM, not the app's JSON: request and response media type `application/scim+json`, the SCIM resource schemas the handler builds (User, Group, ListResponse, PatchOp, Error with `schemas`, `detail`, `status`), `ETag`/`If-Match` headers where the handler reads or writes them. Document them from `ScimProtocolService.cs` and the recordings; keep `x-vantigo-access: scim`. If kin-openapi has no body decoder for `application/scim+json`, register `openapi3filter.JSONBodyDecoder` for it in exchanges.go (next to any `application/problem+json` registration) and add a `TestValidate` case for it.
4. Request bodies and parameters with the types .NET binds; where code and a recording disagree, the recording wins; note it.

Rules: never change a path, property name, status code or enum value. `nullable: true` where .NET serializes `null`. Keep `x-vantigo-access` and `operationId` as extracted. Now that identity is complete, delete every schema in `identity.yaml` that nothing references, and — every module being curated — every schema in `common.yaml` that no module file references (the split left `TenantCreateRequest`, `TenantSwitchRequest` and `TenantUpdateRequest` there, reachable only from the dropped tenancy operations). A recorded exchange whose request the contract rejects passes when the server answered 4xx and the documented rejection response matches; never loosen a request schema, parameter or `required` list to accept an invalid recorded request — document the rejection response instead. New or edited schemas follow the Global Constraints' numeric and nullable-reference conventions (`type: integer`/`number`; `allOf` + `nullable: true`); the guard tests fail otherwise.

- [ ] **Step 3: Verify**

Run:

```bash
cd apps/server
go generate ./... && go build ./... && go test ./internal/openapi/... -count=1 && golangci-lint run
cat ../../openapi/testdata/known-gaps.txt
go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md
tail -1 ../../openapi/COVERAGE.md
```

Expected: every test PASS; `known-gaps.txt` is **empty** (every module is now curated); the coverage total line.

- [ ] **Step 4: Commit**

```bash
git add openapi apps/server
git commit -m "feat(contract): curate identity administration and SCIM

known-gaps.txt is empty: every operation in every module documents its
responses and every recorded exchange validates."
```

---

### Task 12: Generate Go code for every module and check it in CI

**Files:**
- Create: `apps/server/internal/openapi/gen/cfg-identity.yaml`, `cfg-customers.yaml`, `cfg-products.yaml`, `cfg-communications.yaml`
- Modify: `apps/server/generate.go`
- Create (generated): `apps/server/internal/{identity,customers,products,communications}/gen/api.gen.go`
- Create: `apps/server/internal/{identity,customers,products,communications}/gen/gen_test.go`
- Modify: `.github/workflows/server-test.yml`

**Interfaces:**
- Consumes: the curated contract; Task 5's `cfg-energy.yaml` pattern.
- Produces: `internal/<module>/gen` for all five modules (models, `StrictServerInterface`, `NewStrictHandler`, `HandlerFromMux`), and a CI step that fails when generated code is stale.

- [ ] **Step 1: Write the four configs**

For each of `identity`, `customers`, `products`, `communications`, create `apps/server/internal/openapi/gen/cfg-<module>.yaml` with exactly this content, substituting the module name in `output` and the comment:

```yaml
# oapi-codegen: models and the strict net/http server interface for
# openapi/<module>.yaml. References into common.yaml become imports of
# internal/apicommon/gen.
package: gen
output: internal/<module>/gen/api.gen.go
generate:
  models: true
  std-http-server: true
  strict-server: true
import-mapping:
  common.yaml: github.com/vantigo-io/vantigo/server/internal/apicommon/gen
output-options:
  skip-prune: true
```

- [ ] **Step 2: Write the four compile tests**

For each of the four modules, create `apps/server/internal/<module>/gen/gen_test.go`:

```go
package gen

import (
	"net/http"
	"testing"
)

// The generated strict server must mount on a std-lib mux; this fails to
// compile if the generator's output or its common.yaml import mapping breaks.
func TestGeneratedServerMountsOnServeMux(t *testing.T) {
	var server StrictServerInterface // nil: only the types are under test
	handler := HandlerFromMux(NewStrictHandler(server, nil), http.NewServeMux())
	if handler == nil {
		t.Fatal("HandlerFromMux returned nil")
	}
}
```

Run: `cd apps/server && go test ./internal/identity/... ./internal/customers/... ./internal/products/... ./internal/communications/...`
Expected: FAIL (`undefined: StrictServerInterface` — nothing generated yet).

- [ ] **Step 3: Generate**

Append to `apps/server/generate.go`, after the energy line:

```go
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-identity.yaml ../../openapi/identity.yaml
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-customers.yaml ../../openapi/customers.yaml
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-products.yaml ../../openapi/products.yaml
//go:generate go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.0 -config internal/openapi/gen/cfg-communications.yaml ../../openapi/communications.yaml
```

Run:

```bash
cd apps/server
go generate ./... && go mod tidy && go build ./... && go test ./... -count=1 && golangci-lint run
```

Expected: generation succeeds for every module; the whole module builds; every test PASS; `0 issues.` If oapi-codegen rejects a construct in a curated file (for example an `allOf` it cannot merge or a `oneOf` without a discriminator), change the YAML to an equivalent form the generator accepts **without changing the wire shape**, re-run the Task 4 tests to prove the recorded exchanges still validate, and list each such change in your report.

- [ ] **Step 4: Check generated code in CI**

In `.github/workflows/server-test.yml`, add after the `🔍 golangci-lint` step:

```yaml
      - name: 🧬 Generated code is up to date
        working-directory: apps/server
        run: |
          go generate ./...
          git diff --exit-code -- . ../../openapi || {
            echo "::error::Generated code or embedded contract copies are stale. Run 'go generate ./...' in apps/server and commit the result."
            exit 1
          }
```

Run: `actionlint .github/workflows/server-*.yml`
Expected: no output.

- [ ] **Step 5: Commit**

```bash
git add apps/server .github/workflows/server-test.yml
git commit -m "build(contract): generate Go server interfaces for every module

Each module gets models and a strict net/http server interface from its
contract file; CI regenerates and fails when anything is stale."
```

---

### Task 13: Generate frontend types from the contract

**Files:**
- Create: `tools/openapi/gen-client.ts`, `tools/openapi/gen-client.test.ts`
- Modify: `package.json` (script `gen:client`, devDependency `openapi-typescript` `7.13.0`)
- Modify: `biome.json` (exclude `**/api-schema.d.ts`)
- Create (generated): `apps/host/frontend/src/api-schema.d.ts`, `apps/customers/frontend/src/api-schema.d.ts`, `apps/products/frontend/src/api-schema.d.ts`, `apps/energy/frontend/src/api-schema.d.ts`, `apps/communications/frontend/src/api-schema.d.ts`
- Modify: `.github/workflows/ci.yml` (drift step in the `build_and_test_frontend` job)

**Interfaces:**
- Produces: `bun run gen:client` — regenerates the five `api-schema.d.ts` files from the contract; `generateClients(outputRoot: string): Promise<string[]>` exported from `tools/openapi/gen-client.ts` (returns the files written).

- [ ] **Step 1: Add the tool dependency**

Run: `bun add -d -E openapi-typescript@7.13.0`
Expected: `package.json` gains `"openapi-typescript": "7.13.0"` under `devDependencies`; `bun.lock` updates.

- [ ] **Step 2: Write the failing test**

Create `tools/openapi/gen-client.test.ts`:

```ts
import { afterEach, describe, expect, it } from "bun:test";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { generateClients, targets } from "./gen-client";

const roots: string[] = [];
afterEach(async () => {
  await Promise.all(roots.splice(0).map((root) => rm(root, { recursive: true, force: true })));
});

describe("gen:client", () => {
  it("maps every module to its frontend package", () => {
    expect(Object.fromEntries(targets.map((t) => [t.module, t.output]))).toEqual({
      identity: "apps/host/frontend/src/api-schema.d.ts",
      customers: "apps/customers/frontend/src/api-schema.d.ts",
      products: "apps/products/frontend/src/api-schema.d.ts",
      energy: "apps/energy/frontend/src/api-schema.d.ts",
      communications: "apps/communications/frontend/src/api-schema.d.ts",
    });
  });

  it("writes typed paths for every module, resolving common.yaml references", async () => {
    const root = await mkdtemp(join(tmpdir(), "gen-client-"));
    roots.push(root);
    const written = await generateClients(root);
    expect(written).toHaveLength(5);
    const customers = await readFile(join(root, "apps/customers/frontend/src/api-schema.d.ts"), "utf8");
    expect(customers).toContain("export interface paths");
    expect(customers).toContain('"/api/v1/customers"');
    expect(customers.startsWith("// Generated by `bun run gen:client`")).toBe(true);
  });
});
```

Run: `bun test tools/openapi`
Expected: FAIL (`Cannot find module './gen-client'`).

- [ ] **Step 3: Implement the generator**

Create `tools/openapi/gen-client.ts`:

```ts
import { mkdir, writeFile } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import openapiTS, { astToString } from "openapi-typescript";

const repositoryRoot = resolve(import.meta.dir, "../..");

/** Which frontend package owns each contract module's generated types. */
export const targets = [
  { module: "identity", output: "apps/host/frontend/src/api-schema.d.ts" },
  { module: "customers", output: "apps/customers/frontend/src/api-schema.d.ts" },
  { module: "products", output: "apps/products/frontend/src/api-schema.d.ts" },
  { module: "energy", output: "apps/energy/frontend/src/api-schema.d.ts" },
  { module: "communications", output: "apps/communications/frontend/src/api-schema.d.ts" },
] as const;

const header =
  "// Generated by `bun run gen:client` from openapi/<module>.yaml — do not edit.\n" +
  "// CI regenerates this file and fails when it is stale.\n\n";

/** Regenerates every module's types under outputRoot and returns the files written. */
export const generateClients = async (outputRoot: string = repositoryRoot): Promise<string[]> => {
  const written: string[] = [];
  for (const target of targets) {
    const source = new URL(`file://${join(repositoryRoot, "openapi", `${target.module}.yaml`)}`);
    const ast = await openapiTS(source);
    const file = join(outputRoot, target.output);
    await mkdir(dirname(file), { recursive: true });
    await writeFile(file, header.replace("<module>", target.module) + astToString(ast));
    written.push(file);
  }
  return written;
};

if (import.meta.main) {
  const files = await generateClients();
  for (const file of files) console.log(`wrote ${file}`);
}
```

Add to `package.json` `scripts`, after `"toolchain:test"`:

```json
    "gen:client": "bun run tools/openapi/gen-client.ts",
    "gen:client:test": "bun test tools/openapi",
```

- [ ] **Step 4: Run the test, then generate the real files**

Run:

```bash
bun test tools/openapi
bun run gen:client
```

Expected: 2 tests pass; five `wrote …/api-schema.d.ts` lines.

- [ ] **Step 5: Keep formatters and linters off the generated files**

In `biome.json`, add `"!**/api-schema.d.ts"` to `files.includes`, after `"!**/routeTree.gen.ts"`.

Run: `bun run format:check && bun run frontend:lint && bun run frontend:test && bun run frontend:build`
Expected: all pass. If a frontend package's ESLint flags its generated `api-schema.d.ts`, add `"src/api-schema.d.ts"` to that package's ESLint `ignores` (the same way each package already ignores `routeTree.gen.ts` or generated files) and re-run.

- [ ] **Step 6: Check generated types in CI**

In `.github/workflows/ci.yml`, in the `build_and_test_frontend` job, add after the `🧰 Install frontend dependencies` step:

```yaml
      - name: 🧬 Frontend API types are up to date
        run: |
          bun run gen:client:test
          bun run gen:client
          git diff --exit-code -- '**/api-schema.d.ts' || {
            echo "::error::Frontend API types are stale. Run 'bun run gen:client' and commit the result."
            exit 1
          }
```

Run: `actionlint .github/workflows/ci.yml .github/workflows/server-*.yml`
Expected: no new findings for the added step (report any pre-existing `ci.yml` findings without fixing them — that file belongs to the .NET pipeline).

- [ ] **Step 7: Commit**

```bash
git add tools/openapi package.json bun.lock biome.json apps/*/frontend/src/api-schema.d.ts apps/*/frontend/eslint.config.* .github/workflows/ci.yml
git commit -m "build(contract): generate frontend API types from the contract

bun run gen:client writes api-schema.d.ts into each module's frontend
package with openapi-typescript 7.13.0; CI regenerates and fails when they
are stale. No call sites change yet."
```

---

### Task 14: Retire the gap tracking and document the workflow

**Files:**
- Modify: `apps/server/internal/openapi/exchanges_test.go` (remove the known-gaps mechanism)
- Delete: `openapi/testdata/known-gaps.txt`
- Regenerate: `openapi/COVERAGE.md`
- Modify: `CONTRIBUTING.md` (the "Go server (port in progress)" section)

**Interfaces:**
- Produces: `TestRecordedExchangesMatchTheContract` fails on any lint problem or any recorded exchange that does not validate, with no allow-list.

- [ ] **Step 1: Remove the allow-list from the test**

In `exchanges_test.go`, delete the `gapsFile` constant, the `CONTRACT_UPDATE_GAPS` branch and the `known` map, and replace the final reporting with a plain failure per failing operation:

```go
	for _, id := range ids {
		t.Errorf("%s does not match the contract:\n  %s", id, strings.Join(failing[id], "\n  "))
	}
```

Update the doc comment to say every operation must lint clean and every recorded exchange must validate. Delete `openapi/testdata/known-gaps.txt`.

Run: `cd apps/server && go test ./internal/openapi/... -count=1 && golangci-lint run`
Expected: PASS (the list was already empty after Task 11); `0 issues.`

- [ ] **Step 2: Refresh the coverage report**

Run: `cd apps/server && go run ./internal/openapi/cmd/contract coverage -corpus ../../openapi/testdata/exchanges -out ../../openapi/COVERAGE.md && tail -1 ../../openapi/COVERAGE.md`
Expected: the total line; commit whatever it reports.

- [ ] **Step 3: Document how to change the contract**

In `CONTRIBUTING.md`'s "Go server (port in progress)" section, add at its end:

````markdown
### The API contract

`openapi/*.yaml` is the single source of truth for the API: one OpenAPI 3.0 file per module plus `common.yaml` for shared components. Every operation needs an `operationId` and an `x-vantigo-access` rule (`anonymous`, `session`, `scim`, `policy:<Name>[+<Name>…]` or `permission:<module>:<verb>[+<module>:<verb>…]`, every listed name required).

To change the API, edit the YAML, then regenerate and test:

```bash
cd apps/server && go generate ./... && go test ./internal/openapi/... && cd ../..
bun run gen:client
```

`go generate` refreshes the embedded copies and the oapi-codegen server interfaces; `gen:client` refreshes each frontend package's `api-schema.d.ts`. CI fails when either is stale.

`internal/openapi` validates every exchange in `openapi/testdata/exchanges/`, recorded from the .NET integration suites, against the contract. While the .NET host still exists, re-record after changing a .NET endpoint:

```bash
VANTIGO_CONTRACT_RECORD=/tmp/vantigo-exchanges dotnet test Vantigo.slnx
cd apps/server && go run ./internal/openapi/cmd/contract corpus -in /tmp/vantigo-exchanges -out ../../openapi/testdata/exchanges
```

`openapi/COVERAGE.md` lists the operations no recorded exchange exercises.
````

- [ ] **Step 4: Final verification**

Run:

```bash
cd apps/server && go generate ./... && git diff --exit-code -- . ../../openapi && go build ./... && go test -count=1 ./... && golangci-lint run && cd ../..
bun run gen:client && git diff --exit-code -- '**/api-schema.d.ts'
bun run format:check && bun run frontend:lint && bun run frontend:test && bun run toolchain:check
actionlint .github/workflows/server-*.yml
```

Expected: no diffs, every test PASS, `0 issues.`, lint and format clean.

- [ ] **Step 5: Commit**

```bash
git add apps/server openapi CONTRIBUTING.md
git commit -m "docs(contract): retire gap tracking and document the contract workflow

Every operation now lints clean and every recorded exchange validates, so
the known-gaps allow-list is gone. CONTRIBUTING explains how to change the
contract, regenerate, and re-record exchanges."
```

---

## Self-review against the spec

| Spec requirement | Task |
|---|---|
| Extraction harness: OpenAPI 3.0, every endpoint, `x-vantigo-access` from metadata (incl. `scim`, sorted `policy:A+B`), operationIds, record schemas | 1 |
| Recorded exchanges from the .NET suites, deduplicated corpus committed | 2, 4 |
| Per-module files + `common.yaml`, relative refs, drops | 3 |
| Embedded copies + loader with relative refs; drift, validity, access-grammar, uniqueness tests; exchange validation | 4 |
| `COVERAGE.md` for operations without exchanges | 4, 6–11, 14 |
| oapi-codegen v2.8.0 per module with `import-mapping` to `internal/apicommon/gen`; proven early | 5, 12 |
| Curation to "every operation documents its responses; every exchange validates" | 6–11 |
| Go drift check in CI | 12 |
| Frontend types via openapi-typescript 7.13.0 into each module package; biome exclusion; CI drift | 13 |
| Out of scope respected: no handlers, no validation mounting, no spec endpoint, no call-site migration | — (no task touches them) |

Recorded deviations: one generated file per module; oapi-codegen pinned in `go:generate` rather than mise; the known-gaps allow-list during curation (retired in Task 14).

