using Microsoft.AspNetCore.Authentication.Cookies;
using Microsoft.AspNetCore.Authorization;
using Microsoft.AspNetCore.Identity;

using Vantigo.Communications.Api.Database.Accounts;

namespace Vantigo.Communications.Api.Endpoints.Auth;

internal static class AuthServiceCollectionExtensions
{
    internal static IServiceCollection AddCommunicationsIdentity(this IServiceCollection services, IHostEnvironment environment)
    {
        services.AddIdentity<ApplicationUser, IdentityRole<Guid>>(options =>
        {
            var isDevelopment = environment.IsDevelopment();
            options.User.RequireUniqueEmail = true;
            options.Password.RequiredLength = isDevelopment ? 1 : 12;
            options.Password.RequireDigit = !isDevelopment;
            options.Password.RequireUppercase = !isDevelopment;
            options.Password.RequireLowercase = !isDevelopment;
            options.Password.RequireNonAlphanumeric = false;
            options.Lockout.AllowedForNewUsers = true;
            options.Lockout.MaxFailedAccessAttempts = 5;
            options.Lockout.DefaultLockoutTimeSpan = TimeSpan.FromMinutes(15);
        }).AddEntityFrameworkStores<AccountsDbContext>().AddDefaultTokenProviders();
        services.Configure<SecurityStampValidatorOptions>(options => options.ValidationInterval = TimeSpan.Zero);
        services.ConfigureApplicationCookie(options =>
        {
            options.Cookie.Name = "vantigo.communications.auth";
            options.Cookie.HttpOnly = true;
            options.Cookie.SameSite = SameSiteMode.Strict;
            options.Cookie.SecurePolicy = environment.IsDevelopment() ? CookieSecurePolicy.SameAsRequest : CookieSecurePolicy.Always;
            options.ExpireTimeSpan = TimeSpan.FromHours(8);
            options.SlidingExpiration = true;
            options.Events.OnRedirectToLogin = context => WriteAuthError(context.Response, StatusCodes.Status401Unauthorized,
                "unauthenticated", "Authentication is required.");
            options.Events.OnRedirectToAccessDenied = context => WriteAuthError(context.Response, StatusCodes.Status403Forbidden,
                "forbidden", "You do not have permission to access this resource.");
        });
        services.AddAuthorization(options =>
        {
            options.AddPolicy(AuthPolicies.Owner, policy => policy.RequireRole(AuthRoles.Owner));
            options.AddPolicy(AuthPolicies.Business, policy => policy.AddRequirements(new BusinessAccessRequirement()));
        });
        services.AddScoped<IAuthorizationHandler, BusinessAccessHandler>();
        services.AddAntiforgery(options =>
        {
            options.Cookie.Name = "vantigo.communications.csrf";
            options.Cookie.HttpOnly = true;
            options.Cookie.SameSite = SameSiteMode.Strict;
            options.Cookie.SecurePolicy = environment.IsDevelopment() ? CookieSecurePolicy.SameAsRequest : CookieSecurePolicy.Always;
            options.HeaderName = "X-XSRF-TOKEN";
        });
        return services;
    }

    private static Task WriteAuthError(HttpResponse response, int status, string code, string message)
    {
        response.StatusCode = status;
        return response.WriteAsJsonAsync(new AuthErrorResponse(new AuthError(code, message)));
    }
}