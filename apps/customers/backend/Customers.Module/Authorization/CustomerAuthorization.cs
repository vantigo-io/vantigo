using System.Security.Claims;

using Microsoft.AspNetCore.Authorization;

using Vantigo.Contracts.AspNetCore.Authorization;

namespace Vantigo.Customers.Authorization;

internal static class CustomerAuthorization
{
    internal static async Task<bool> HasPermissionAsync(
        IAuthorizationService authorization,
        ClaimsPrincipal principal,
        string permission)
    {
        var result = await authorization.AuthorizeAsync(
            principal,
            resource: null,
            policyName: PermissionEndpointConventionExtensions.PermissionPolicyName(permission));
        return result.Succeeded;
    }
}