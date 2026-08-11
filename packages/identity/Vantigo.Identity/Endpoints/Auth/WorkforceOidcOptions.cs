using Microsoft.AspNetCore.Hosting;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.Options;

using Vantigo.Configuration;

namespace Vantigo.Identity.Endpoints.Auth;

// Operates on the canonical WorkforceOidcOptions type defined in
// Vantigo.Configuration; keeps this file present so existing identity
// namespaces continue to compile without edits.
public static class WorkforceOidcOptionsExtensions
{
    public static WorkforceOidcOptions Load(IConfiguration configuration, IHostEnvironment environment)
    {
        var authOptions = configuration.GetSection("Authentication").Get<VantigoAuthenticationOptions>() ?? new();
        return WorkforceOidcOptions.FromAuthenticationOptions(authOptions, environment);
    }
}