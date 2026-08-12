using System.Security.Claims;

using Microsoft.AspNetCore.Authorization;

using Vantigo.Contracts.AspNetCore.Authorization;

namespace Vantigo.Products.Authorization;

internal static class ProductsAuthorization
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