using Microsoft.IdentityModel.Protocols.OpenIdConnect;

using Vantigo.Configuration;

namespace Vantigo.Identity.Endpoints.Auth;

internal static class WorkforceOidcClientAssertion
{
    internal const string JwtBearerAssertionType = "urn:ietf:params:oauth:client-assertion-type:jwt-bearer";

    internal static string ReadFresh(WorkforceOidcOptions options)
    {
        if (!string.Equals(options.ClientAuthentication, WorkforceOidcOptions.WorkloadIdentityAuthentication, StringComparison.Ordinal) ||
            string.IsNullOrWhiteSpace(options.WorkloadIdentityTokenFile))
        {
            throw new InvalidOperationException("Workload identity client assertion is not configured.");
        }

        try
        {
            var assertion = File.ReadAllText(options.WorkloadIdentityTokenFile).Trim();
            if (assertion.Length == 0 || assertion.Any(char.IsWhiteSpace))
                throw new InvalidOperationException("The workload identity token file is empty or invalid.");
            return assertion;
        }
        catch (Exception exception) when (exception is IOException or UnauthorizedAccessException)
        {
            throw new InvalidOperationException("The workload identity token file could not be read.", exception);
        }
    }

    internal static void Apply(OpenIdConnectMessage request, WorkforceOidcOptions options)
    {
        request.ClientSecret = null;
        request.ClientAssertionType = JwtBearerAssertionType;
        request.ClientAssertion = ReadFresh(options);
    }
}