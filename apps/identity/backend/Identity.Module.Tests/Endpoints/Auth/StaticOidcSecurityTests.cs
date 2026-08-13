using System.Security.Claims;

using Microsoft.IdentityModel.Protocols.OpenIdConnect;

using Vantigo.Configuration;
using Vantigo.Identity.Endpoints.Auth;

namespace Vantigo.Identity.Tests.Endpoints.Auth;

public sealed class StaticOidcSecurityTests
{
    private const string TenantId = "00000000-0000-0000-0000-000000000000";
    private const string ClientId = "11111111-1111-1111-1111-111111111111";
    private const string ObjectId = "22222222-2222-2222-2222-222222222222";
    private const string Authority = "https://login.microsoftonline.com/00000000-0000-0000-0000-000000000000/v2.0";

    [Fact]
    public void EntraPolicyRequiresConfiguredTenantObjectAndAuthorizedParty()
    {
        var options = new WorkforceOidcOptions(
            true, WorkforceOidcOptions.EntraProvider, Authority, ClientId,
            WorkforceOidcOptions.ClientSecretAuthentication, "secret", null, [],
            "Entra", WorkforceOidcOptions.DefaultCallbackPath, TenantId);

        var valid = Principal(
            ("tid", TenantId), ("oid", ObjectId), ("aud", ClientId),
            ("aud", "another-audience"), ("azp", ClientId));
        Assert.True(StaticOidcClaimValidation.Validate(valid, options).Succeeded);

        var wrongTenant = Principal(("tid", "33333333-3333-3333-3333-333333333333"), ("oid", ObjectId));
        Assert.False(StaticOidcClaimValidation.Validate(wrongTenant, options).Succeeded);

        var wrongAuthorizedParty = Principal(
            ("tid", TenantId), ("oid", ObjectId), ("aud", ClientId),
            ("aud", "another-audience"), ("azp", "33333333-3333-3333-3333-333333333333"));
        Assert.False(StaticOidcClaimValidation.Validate(wrongAuthorizedParty, options).Succeeded);
    }

    [Fact]
    public void GooglePolicyRequiresVerifiedAllowedHostedDomain()
    {
        var options = new WorkforceOidcOptions(
            true, WorkforceOidcOptions.GoogleProvider, "https://accounts.google.com",
            "client.apps.googleusercontent.com", WorkforceOidcOptions.ClientSecretAuthentication,
            "secret", null, ["example.com"], "Google", WorkforceOidcOptions.DefaultCallbackPath, null);

        var valid = Principal(
            ("email", "person@example.com"), ("email_verified", "true"), ("hd", "example.com"));
        Assert.True(StaticOidcClaimValidation.Validate(valid, options).Succeeded);

        var unverified = Principal(
            ("email", "person@example.com"), ("email_verified", "false"), ("hd", "example.com"));
        Assert.False(StaticOidcClaimValidation.Validate(unverified, options).Succeeded);

        var mismatchedDomain = Principal(
            ("email", "person@example.com"), ("email_verified", "true"), ("hd", "other.example"));
        Assert.False(StaticOidcClaimValidation.Validate(mismatchedDomain, options).Succeeded);
    }

    [Fact]
    public void WorkloadAssertionIsReadFreshAndNeverSendsAClientSecret()
    {
        var path = Path.Combine(Path.GetTempPath(), $"vantigo-oidc-assertion-{Guid.NewGuid():N}");
        try
        {
            File.WriteAllText(path, "first-assertion");
            var options = new WorkforceOidcOptions(
                true, WorkforceOidcOptions.EntraProvider, Authority, ClientId,
                WorkforceOidcOptions.WorkloadIdentityAuthentication, null, path, [],
                "Entra", WorkforceOidcOptions.DefaultCallbackPath, TenantId);

            var first = new OpenIdConnectMessage { ClientSecret = "must-be-removed" };
            WorkforceOidcClientAssertion.Apply(first, options);
            Assert.Null(first.ClientSecret);
            Assert.Equal("first-assertion", first.ClientAssertion);

            File.WriteAllText(path, "second-assertion");
            var second = new OpenIdConnectMessage();
            WorkforceOidcClientAssertion.Apply(second, options);
            Assert.Equal("second-assertion", second.ClientAssertion);
            Assert.Equal(WorkforceOidcClientAssertion.JwtBearerAssertionType, second.ClientAssertionType);
        }
        finally
        {
            if (File.Exists(path)) File.Delete(path);
        }
    }

    private static ClaimsPrincipal Principal(params (string Type, string Value)[] claims) =>
        new(new ClaimsIdentity(claims.Select(item => new Claim(item.Type, item.Value)), "oidc"));
}