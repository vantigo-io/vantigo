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
    public void Multiple_permissions_are_sorted_deduplicated_and_joined()
    {
        Assert.Equal(
            "permission:products:categories-view+products:pricing-view+products:products-view+products:tax-categories-view+products:variants-view",
            Access("api/v1/products",
                new PermissionEndpointConventionExtensions.PermissionMetadata("products:tax-categories-view"),
                new PermissionEndpointConventionExtensions.PermissionMetadata("products:products-view"),
                new PermissionEndpointConventionExtensions.PermissionMetadata("products:variants-view"),
                new PermissionEndpointConventionExtensions.PermissionMetadata("products:pricing-view"),
                new PermissionEndpointConventionExtensions.PermissionMetadata("products:categories-view"),
                new PermissionEndpointConventionExtensions.PermissionMetadata("products:products-view")));
    }

    [Fact]
    public void Permission_metadata_with_a_non_permission_policy_throws()
    {
        var error = Assert.Throws<InvalidOperationException>(() => Access("api/v1/identity/account/profile",
            new PermissionEndpointConventionExtensions.PermissionMetadata("identity:profile-view"),
            new AuthorizeAttribute("ActiveAccount")));
        Assert.Contains("api/v1/identity/account/profile", error.Message);
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

    private static readonly IReadOnlySet<string> NoAmbiguousIds = new HashSet<string>();

    [Fact]
    public void Nested_type_schema_id_is_prefixed_with_its_declaring_type()
    {
        Assert.Equal("SampleRequest",
            ContractAnnotations.SchemaId(typeof(SampleEndpoint.Request), nameof(SampleEndpoint.Request), NoAmbiguousIds));
        Assert.Equal("SamplesResponse",
            ContractAnnotations.SchemaId(typeof(SamplesEndpoints.Response), nameof(SamplesEndpoints.Response), NoAmbiguousIds));
    }

    [Fact]
    public void Doubly_nested_type_schema_id_carries_both_declaring_prefixes_outermost_first()
    {
        Assert.Equal("OuterInnerResponse",
            ContractAnnotations.SchemaId(typeof(OuterEndpoints.InnerEndpoint.Response), nameof(OuterEndpoints.InnerEndpoint.Response), NoAmbiguousIds));
    }

    [Fact]
    public void Top_level_type_schema_id_is_unchanged()
    {
        Assert.Equal(nameof(TopLevelRecord),
            ContractAnnotations.SchemaId(typeof(TopLevelRecord), nameof(TopLevelRecord), NoAmbiguousIds));
    }

    [Fact]
    public void Ambiguous_schema_ids_are_the_ones_produced_by_more_than_one_type()
    {
        var types = new[]
        {
            typeof(SampleEndpoint.Request),
            typeof(SamplesEndpoints.Response),
            typeof(CollidingEndpoint.Request),
            typeof(CollidingEndpoints.Request),
            typeof(TopLevelRecord),
        };

        var ambiguous = ContractAnnotations.AmbiguousSchemaIds(types);

        Assert.Contains("CollidingRequest", ambiguous);
        Assert.DoesNotContain("SampleRequest", ambiguous);
    }

    [Fact]
    public void Ambiguous_ids_are_prefixed_with_the_module_name()
    {
        var ambiguous = new HashSet<string> { "CollidingRequest" };

        Assert.Equal("HostTestsCollidingRequest",
            ContractAnnotations.SchemaId(typeof(CollidingEndpoint.Request), nameof(CollidingEndpoint.Request), ambiguous));
    }

    [Fact]
    public void Registry_allows_reclaiming_by_the_same_type_and_by_its_nullable_form()
    {
        var registry = new ContractAnnotations.SchemaIdRegistry();

        registry.Claim(typeof(int), "Number");
        registry.Claim(typeof(int), "Number");
        registry.Claim(typeof(int?), "Number");
    }

    [Fact]
    public void Registry_throws_naming_both_types_when_a_different_type_claims_the_same_id()
    {
        var registry = new ContractAnnotations.SchemaIdRegistry();
        registry.Claim(typeof(SampleEndpoint.Request), "Shared");

        var error = Assert.Throws<InvalidOperationException>(() =>
            registry.Claim(typeof(SamplesEndpoints.Response), "Shared"));

        Assert.Contains(typeof(SampleEndpoint.Request).FullName!, error.Message);
        Assert.Contains(typeof(SamplesEndpoints.Response).FullName!, error.Message);
    }
}

internal static class SampleEndpoint
{
    internal sealed record Request(string Name);
}

internal static class SamplesEndpoints
{
    internal sealed record Response(int Id);
}

internal static class OuterEndpoints
{
    internal static class InnerEndpoint
    {
        internal sealed record Response(int Id);
    }
}

internal static class CollidingEndpoint
{
    internal sealed record Request(string Name);
}

internal static class CollidingEndpoints
{
    internal sealed record Request(string Name);
}

internal sealed record TopLevelRecord(int Id);