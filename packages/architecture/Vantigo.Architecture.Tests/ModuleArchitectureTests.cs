using System.Reflection;
using System.Runtime.CompilerServices;

using NetArchTest.Rules;

using Vantigo.Contracts;

namespace Vantigo.Architecture.Tests;

public sealed class ModuleArchitectureTests
{
    private static readonly IReadOnlyList<ModuleDefinition> Modules =
    [
        new("Products", "Vantigo.Products", typeof(Vantigo.Products.Endpoints.ProductsModuleEndpointExtensions).Assembly),
        new("Customers", "Vantigo.Customers", typeof(Vantigo.Customers.Endpoints.VersionedBusinessEndpointExtensions).Assembly),
        new("Communications", "Vantigo.Communications", typeof(Vantigo.Communications.Endpoints.VersionedBusinessEndpointExtensions).Assembly),
        new("Energy", "Vantigo.Energy", typeof(Vantigo.Energy.Endpoints.EnergyModuleEndpointExtensions).Assembly),
    ];

    private static readonly IReadOnlySet<string> AllowedVantigoDependencies =
        new HashSet<string>(StringComparer.Ordinal)
        {
            "Vantigo.Configuration",
            "Vantigo.Contracts",
            "Vantigo.Contracts.AspNetCore",
        };

    public static IEnumerable<object[]> ModulePairs =>
        from source in Modules
        from target in Modules
        where source != target
        select new object[] { source, target };

    [Theory]
    [MemberData(nameof(ModulePairs))]
    public void Module_does_not_depend_on_another_module(ModuleDefinition source, ModuleDefinition target)
    {
        var result = Types.InAssembly(source.Assembly)
            .ShouldNot()
            .HaveDependencyOn(target.RootNamespace)
            .GetResult();

        Assert.True(result.IsSuccessful, FormatFailure(result, $"{source.Name} must not depend on {target.Name}"));
    }

    [Fact]
    public void Module_dependencies_are_limited_to_the_shared_architecture_contract()
    {
        foreach (var module in Modules)
        {
            var unexpectedDependencies = module.Assembly
                .GetReferencedAssemblies()
                .Select(reference => reference.Name)
                .Where(name => name is not null && name.StartsWith("Vantigo.", StringComparison.Ordinal))
                .Where(name => !string.Equals(name, module.Assembly.GetName().Name, StringComparison.Ordinal))
                .Where(name => name is not null && !AllowedVantigoDependencies.Contains(name))
                .OrderBy(name => name, StringComparer.Ordinal)
                .ToArray();

            Assert.True(
                unexpectedDependencies.Length == 0,
                $"{module.Name} has unexpected Vantigo.* assembly dependencies: {string.Join(", ", unexpectedDependencies)}");
        }
    }

    [Fact]
    public void Contracts_do_not_depend_on_modules_or_web_and_database_frameworks()
    {
        var contracts = Types.InAssembly(typeof(IModuleMarker).Assembly);

        foreach (var forbiddenDependency in new[]
                 {
                     "Vantigo.Products",
                     "Vantigo.Customers",
                     "Vantigo.Communications",
                     "Microsoft.EntityFrameworkCore",
                     "Microsoft.AspNetCore",
                 })
        {
            var result = contracts.ShouldNot().HaveDependencyOn(forbiddenDependency).GetResult();
            Assert.True(result.IsSuccessful, FormatFailure(result, $"Vantigo.Contracts must not depend on {forbiddenDependency}"));
        }
    }

    [Fact]
    public void Endpoint_and_database_types_are_internal()
    {
        foreach (var module in Modules)
        {
            var publicBoundaryTypes = module.Assembly
                .GetTypes()
                .Where(type => type.Namespace is not null)
                .Where(type => type.Namespace!.Contains(".Endpoints", StringComparison.Ordinal)
                    || type.Namespace!.Contains(".Database", StringComparison.Ordinal))
                .Where(type => type.IsPublic || type.IsNestedPublic)
                .Where(type => !AllowedPublicBoundaryTypes.Contains(type.FullName ?? string.Empty))
                .Select(type => type.FullName ?? type.Name)
                .OrderBy(name => name, StringComparer.Ordinal)
                .ToArray();

            Assert.True(
                publicBoundaryTypes.Length == 0,
                $"{module.Name} exposes endpoint/database types publicly: {string.Join(", ", publicBoundaryTypes)}");
        }
    }

    [Fact]
    public void Domain_types_do_not_depend_on_aspnet_core()
    {
        foreach (var module in Modules)
        {
            var result = Types.InAssembly(module.Assembly)
                .That()
                .ResideInNamespaceMatching($"^{RegexEscape(module.RootNamespace)}\\.Domain(\\.|$).*")
                .ShouldNot()
                .HaveDependencyOn("Microsoft.AspNetCore")
                .GetResult();

            Assert.True(result.IsSuccessful, FormatFailure(result, $"{module.Name} domain types must not depend on ASP.NET Core"));
        }
    }

    [Fact]
    public void Modules_only_grant_internals_visibility_to_their_own_test_assembly()
    {
        foreach (var module in Modules)
        {
            var friendAssemblies = module.Assembly
                .GetCustomAttributes<InternalsVisibleToAttribute>()
                .Select(attribute => attribute.AssemblyName.Split(',')[0].Trim())
                .ToArray();

            var expectedFriend = $"Vantigo.{module.Name}.Module.Tests";
            Assert.All(friendAssemblies, friend => Assert.Equal(expectedFriend, friend));
        }
    }

    private static readonly IReadOnlySet<string> AllowedPublicBoundaryTypes =
        new HashSet<string>(StringComparer.Ordinal)
        {
            // Public database configuration, generated migrations, DbContexts, and design-time factories
            // are consumed by the host, ASP.NET Core DI, or EF tooling.
            "Vantigo.Products.Database.Products.ProductsDbContext",
            "Vantigo.Products.Database.Products.ProductsDbContextFactory",
            "Vantigo.Products.Database.ProductDatabaseConfiguration",
            "Vantigo.Products.Database.Products.Migrations.InitialProductsSchema",
            "Vantigo.Products.Database.Products.Migrations.IntroduceTaxCategories",
            "Vantigo.Products.Database.Products.Migrations.SplitProductVariants",
            "Vantigo.Products.Endpoints.ProductsModuleEndpointExtensions",
            "Vantigo.Customers.Database.Customers.CustomersDbContext",
            "Vantigo.Customers.Database.Customers.CustomersDbContextFactory",
            "Vantigo.Customers.Database.CustomerDatabaseConfiguration",
            "Vantigo.Customers.Database.DevelopmentSeed.DevelopmentDataSeeder",
            "Vantigo.Customers.Database.Customers.Migrations.InitialCustomersSchema",
            "Vantigo.Customers.Endpoints.VersionedBusinessEndpointExtensions",
            "Vantigo.Communications.Database.Communications.CommunicationsDbContext",
            "Vantigo.Communications.Database.Communications.CommunicationsDbContextFactory",
            "Vantigo.Communications.Database.CommunicationsDatabaseConfiguration",
            "Vantigo.Communications.Database.Communications.Migrations.InitialCommunicationsSchemaV3",
            "Vantigo.Communications.Endpoints.VersionedBusinessEndpointExtensions",
            // Communications currently exposes its HTTP request/response contracts and EF entities;
            // the endpoint binder and EF model builder consume these public types.
            "Vantigo.Communications.Database.Communications.EmailMessage",
            "Vantigo.Communications.Database.Communications.ExternalEntityLink",
            "Vantigo.Communications.Database.Communications.IdempotencyRecord",
            "Vantigo.Communications.Database.Communications.MailboxProviderCredential",
            "Vantigo.Communications.Database.Communications.MessageEvent",
            "Vantigo.Communications.Database.Communications.OutboxJob",
            "Vantigo.Communications.Database.Communications.RecipientDelivery",
            "Vantigo.Communications.Database.Communications.SharedMailbox",
            "Vantigo.Communications.Database.Communications.Suppression",
            "Vantigo.Communications.Endpoints.CommunicationError",
            "Vantigo.Communications.Endpoints.CommunicationErrorResponse",
            "Vantigo.Communications.Endpoints.CreateEmailRequest",
            "Vantigo.Communications.Endpoints.CreateMailboxRequest",
            "Vantigo.Communications.Endpoints.CreateSuppressionRequest",
            "Vantigo.Communications.Endpoints.DeliveryResponse",
            "Vantigo.Communications.Endpoints.EmailCreateResponse",
            "Vantigo.Communications.Endpoints.EmailRecipientRequest",
            "Vantigo.Communications.Endpoints.ExternalEntityLinkRequest",
            "Vantigo.Communications.Endpoints.ExternalEntityLinkResponse",
            "Vantigo.Communications.Endpoints.MailboxResponse",
            "Vantigo.Communications.Endpoints.MailboxSettingsSummary",
            "Vantigo.Communications.Endpoints.MailboxSummaryResponse",
            "Vantigo.Communications.Endpoints.MailgunMailboxCredentialRequest",
            "Vantigo.Communications.Endpoints.MessageDetailResponse",
            "Vantigo.Communications.Endpoints.MessageEventResponse",
            "Vantigo.Communications.Endpoints.MessageListItem",
            "Vantigo.Communications.Endpoints.ResendMessageRequest",
            "Vantigo.Communications.Endpoints.ResendMessageResponse",
            "Vantigo.Communications.Endpoints.SmtpMailboxCredentialRequest",
            "Vantigo.Communications.Endpoints.SuppressionResponse",
            "Vantigo.Communications.Endpoints.UpdateMailboxRequest",
            "Vantigo.Energy.Database.Energy.EnergyDbContext",
            "Vantigo.Energy.Database.Energy.EnergyDbContextFactory",
            "Vantigo.Energy.Database.Energy.Migrations.InitialEnergySchema",
            "Vantigo.Energy.Database.Energy.Migrations.IntroduceMeters",
            "Vantigo.Energy.Database.EnergyDatabaseConfiguration",
            "Vantigo.Energy.Endpoints.EnergyModuleEndpointExtensions",
        };

    private static string RegexEscape(string value) =>
        value.Replace(".", "\\.", StringComparison.Ordinal);

    private static string FormatFailure(TestResult result, string message) =>
        result.IsSuccessful
            ? message
            : $"{message}. Failing types: {string.Join(", ", result.FailingTypes ?? [])}";

    public sealed record ModuleDefinition(string Name, string RootNamespace, Assembly Assembly);
}