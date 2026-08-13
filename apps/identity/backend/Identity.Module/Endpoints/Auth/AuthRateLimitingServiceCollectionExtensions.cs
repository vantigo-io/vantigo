using System.Threading.RateLimiting;

using Microsoft.AspNetCore.Http;
using Microsoft.AspNetCore.RateLimiting;
using Microsoft.Extensions.DependencyInjection;

namespace Vantigo.Identity.Endpoints.Auth;

public static class AuthRateLimitingServiceCollectionExtensions
{
    public static IServiceCollection AddVantigoAuthenticationRateLimiting(this IServiceCollection services)
    {
        services.AddRateLimiter(options =>
        {
            options.RejectionStatusCode = StatusCodes.Status429TooManyRequests;
            options.OnRejected = async (context, cancellationToken) =>
            {
                if (context.Lease.TryGetMetadata(MetadataName.RetryAfter, out var retryAfter) && retryAfter is TimeSpan retryAfterDuration)
                {
                    context.HttpContext.Response.Headers.RetryAfter = ((int)Math.Ceiling(retryAfterDuration.TotalSeconds)).ToString();
                }

                context.HttpContext.Response.ContentType = "application/json";
                await context.HttpContext.Response.WriteAsJsonAsync(
                    new AuthErrorResponse(new AuthError("rate_limited", "Too many authentication attempts. Please try again later.")),
                    cancellationToken);
            };
            options.AddPolicy(AuthRateLimitPolicies.Login, context =>
                RateLimitPartition.GetFixedWindowLimiter(
                    GetRateLimitPartitionKey(context),
                    _ => new FixedWindowRateLimiterOptions
                    {
                        PermitLimit = 100,
                        Window = TimeSpan.FromMinutes(1),
                        QueueLimit = 0,
                        AutoReplenishment = true,
                    }));
            options.AddPolicy(AuthRateLimitPolicies.Bootstrap, context =>
                RateLimitPartition.GetFixedWindowLimiter(
                    GetRateLimitPartitionKey(context),
                    _ => new FixedWindowRateLimiterOptions
                    {
                        PermitLimit = 20,
                        Window = TimeSpan.FromMinutes(1),
                        QueueLimit = 0,
                        AutoReplenishment = true,
                    }));
            options.AddPolicy(AuthRateLimitPolicies.Invitations, context =>
                RateLimitPartition.GetFixedWindowLimiter(GetRateLimitPartitionKey(context),
                    _ => new FixedWindowRateLimiterOptions { PermitLimit = 30, Window = TimeSpan.FromMinutes(1), QueueLimit = 0 }));
            options.AddPolicy(AuthRateLimitPolicies.InvitationAcceptance, context =>
                RateLimitPartition.GetFixedWindowLimiter(GetRateLimitPartitionKey(context),
                    _ => new FixedWindowRateLimiterOptions { PermitLimit = 20, Window = TimeSpan.FromMinutes(1), QueueLimit = 0 }));
            options.AddPolicy(AuthRateLimitPolicies.PasswordRecovery, context =>
                RateLimitPartition.GetFixedWindowLimiter(GetRateLimitPartitionKey(context),
                    _ => new FixedWindowRateLimiterOptions { PermitLimit = 10, Window = TimeSpan.FromMinutes(15), QueueLimit = 0 }));
            options.AddPolicy(AuthRateLimitPolicies.Mfa, context =>
                RateLimitPartition.GetFixedWindowLimiter(GetRateLimitPartitionKey(context),
                    _ => new FixedWindowRateLimiterOptions { PermitLimit = 20, Window = TimeSpan.FromMinutes(5), QueueLimit = 0 }));
            options.AddPolicy(AuthRateLimitPolicies.PasskeyLogin, context =>
                RateLimitPartition.GetFixedWindowLimiter(GetRateLimitPartitionKey(context),
                    _ => new FixedWindowRateLimiterOptions { PermitLimit = 30, Window = TimeSpan.FromMinutes(5), QueueLimit = 0 }));
            options.AddPolicy(AuthRateLimitPolicies.UserManagement, context =>
                RateLimitPartition.GetFixedWindowLimiter(GetRateLimitPartitionKey(context),
                    _ => new FixedWindowRateLimiterOptions { PermitLimit = 30, Window = TimeSpan.FromMinutes(1), QueueLimit = 0 }));
            options.AddPolicy(AuthRateLimitPolicies.OwnerAvatarRead, context =>
                RateLimitPartition.GetFixedWindowLimiter(GetRateLimitPartitionKey(context),
                    _ => new FixedWindowRateLimiterOptions { PermitLimit = 300, Window = TimeSpan.FromMinutes(1), QueueLimit = 0 }));
        });

        return services;
    }

    private static string GetRateLimitPartitionKey(HttpContext context) =>
        context.Connection.RemoteIpAddress?.ToString() ?? "unknown";
}