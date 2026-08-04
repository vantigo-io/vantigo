using Microsoft.AspNetCore.Authorization;

namespace Vantigo.Communications.Api.Endpoints.Auth;

internal sealed class BusinessAccessRequirement : IAuthorizationRequirement;

internal sealed class BusinessAccessHandler : AuthorizationHandler<BusinessAccessRequirement>
{
    protected override Task HandleRequirementAsync(AuthorizationHandlerContext context, BusinessAccessRequirement requirement)
    {
        if (context.User.Identity?.IsAuthenticated == true &&
            (context.User.IsInRole(AuthRoles.Owner) || context.User.IsInRole(AuthRoles.User)))
            context.Succeed(requirement);
        return Task.CompletedTask;
    }
}