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