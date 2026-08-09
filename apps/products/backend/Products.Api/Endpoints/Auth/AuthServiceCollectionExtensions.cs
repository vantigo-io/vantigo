using System.Security.Claims;

using Microsoft.AspNetCore.Authentication.Cookies;
using Microsoft.AspNetCore.Authentication.OpenIdConnect;
using Microsoft.AspNetCore.Identity;

using Vantigo.Products.Api.Database.Accounts;

namespace Vantigo.Products.Api.Endpoints.Auth;

internal static class AuthServiceCollectionExtensions
{
    internal static IServiceCollection AddProductIdentity(
        this IServiceCollection services,
        IHostEnvironment environment)
    {
        services
            .AddIdentity<ApplicationUser, IdentityRole<Guid>>(options =>
            {
                options.User.RequireUniqueEmail = true;
                var isDevelopment = environment.IsDevelopment();
                options.Password.RequiredLength = isDevelopment ? 1 : 12;
                options.Password.RequireDigit = !isDevelopment;
                options.Password.RequireUppercase = !isDevelopment;
                options.Password.RequireLowercase = !isDevelopment;
                options.Password.RequireNonAlphanumeric = false;
                options.Lockout.AllowedForNewUsers = true;
                options.Lockout.MaxFailedAccessAttempts = 5;
                options.Lockout.DefaultLockoutTimeSpan = TimeSpan.FromMinutes(15);
            })
            .AddEntityFrameworkStores<AccountsDbContext>()
            .AddDefaultTokenProviders();
        services.Configure<IdentityOptions>(options =>
        {
            options.Tokens.AuthenticatorTokenProvider = TokenOptions.DefaultAuthenticatorProvider;
        });
        services.Configure<DataProtectionTokenProviderOptions>(options =>
        {
            options.TokenLifespan = TimeSpan.FromHours(24);
        });
        services.Configure<SecurityStampValidatorOptions>(options =>
        {
            options.ValidationInterval = TimeSpan.Zero;
        });
        services.ConfigureApplicationCookie(options =>
        {
            options.Cookie.Name = "vantigo.products.auth";
            options.Cookie.HttpOnly = true;
            options.Cookie.SameSite = SameSiteMode.Strict;
            options.Cookie.SecurePolicy = environment.IsDevelopment()
                ? CookieSecurePolicy.SameAsRequest
                : CookieSecurePolicy.Always;
            options.ExpireTimeSpan = TimeSpan.FromHours(8);
            options.SlidingExpiration = true;
            options.Events.OnRedirectToLogin = context =>
            {
                context.Response.StatusCode = StatusCodes.Status401Unauthorized;
                return context.Response.WriteAsJsonAsync(
                    new AuthErrorResponse(new AuthError("unauthenticated", "Authentication is required.")));
            };
            options.Events.OnRedirectToAccessDenied = context =>
            {
                context.Response.StatusCode = StatusCodes.Status403Forbidden;
                return context.Response.WriteAsJsonAsync(
                    new AuthErrorResponse(new AuthError("forbidden", "You do not have permission to access this resource.")));
            };
        });

        return services;
    }

    internal static IServiceCollection AddWorkforceOidc(
        this IServiceCollection services,
        WorkforceOidcOptions workforceOidc,
        IHostEnvironment environment)
    {
        services.AddSingleton(workforceOidc);
        services.Configure<CookieAuthenticationOptions>(IdentityConstants.ExternalScheme, options =>
        {
            // The external cookie must survive the provider's top-level callback but is
            // never used as the application session. The completion endpoint consumes and
            // clears it after it has validated the external identity.
            options.Cookie.Name = "vantigo.products.external";
            options.Cookie.HttpOnly = true;
            options.Cookie.SameSite = SameSiteMode.Lax;
            options.Cookie.SecurePolicy = environment.IsDevelopment()
                ? CookieSecurePolicy.SameAsRequest
                : CookieSecurePolicy.Always;
        });
        if (workforceOidc.Enabled)
        {
            services.AddAuthentication()
                .AddOpenIdConnect(WorkforceOidcOptions.Scheme, options =>
                {
                    options.Authority = workforceOidc.Authority;
                    options.ClientId = workforceOidc.ClientId;
                    options.ClientSecret = workforceOidc.ClientSecret;
                    options.SignInScheme = IdentityConstants.ExternalScheme;
                    options.CallbackPath = workforceOidc.CallbackPath;
                    options.ResponseType = "code";
                    options.UsePkce = true;
                    options.RequireHttpsMetadata = !environment.IsDevelopment();
                    options.SaveTokens = false;
                    options.GetClaimsFromUserInfoEndpoint = false;
                    options.MapInboundClaims = false;
                    options.Scope.Clear();
                    options.Scope.Add("openid");
                    options.Scope.Add("profile");
                    options.Scope.Add("email");
                    options.Events.OnRemoteFailure = context =>
                    {
                        context.HandleResponse();
                        context.Response.Redirect("/sign-in?error=oidc_remote_failure");
                        return Task.CompletedTask;
                    };
                    options.Events.OnAuthenticationFailed = context =>
                    {
                        context.HandleResponse();
                        context.Response.Redirect("/sign-in?error=oidc_authentication_failed");
                        return Task.CompletedTask;
                    };
                    options.Events.OnTokenValidated = context =>
                    {
                        // SecurityToken is the issuer value after the built-in OIDC
                        // handler has validated signature, metadata issuer, audience,
                        // nonce, state, and correlation. Do not parse JWT text or trust a
                        // raw iss claim that claim actions may remove or remap.
                        var tokenIssuer = context.SecurityToken?.Issuer;
                        if (!WorkforceOidcOptions.TryNormalizeIssuer(tokenIssuer, out var normalizedIssuer) ||
                            !WorkforceOidcOptions.TryNormalizeIssuer(workforceOidc.Authority, out var configuredIssuer) ||
                            !string.Equals(normalizedIssuer, configuredIssuer, StringComparison.Ordinal))
                        {
                            context.Fail("The validated OIDC issuer does not match the configured authority.");
                            return Task.CompletedTask;
                        }

                        var identity = context.Principal?.Identities.FirstOrDefault();
                        if (identity is null)
                        {
                            context.Fail("The validated OIDC principal is missing.");
                            return Task.CompletedTask;
                        }

                        foreach (var claim in identity.FindAll(WorkforceOidcOptions.ValidatedIssuerClaim).ToArray())
                        {
                            identity.RemoveClaim(claim);
                        }

                        identity.AddClaim(new Claim(WorkforceOidcOptions.ValidatedIssuerClaim, normalizedIssuer!));
                        return Task.CompletedTask;
                    };
                });
        }

        return services;
    }

    internal static IServiceCollection AddProductAuthorization(this IServiceCollection services)
    {
        services.AddAuthorization(options =>
        {
            options.AddPolicy(AuthPolicies.Owner, policy => policy.RequireRole(AuthRoles.Owner));
            options.AddPolicy(AuthPolicies.OwnerManagement, policy =>
                policy.RequireRole(AuthRoles.Owner).AddRequirements(new MfaAuthenticatedRequirement()));
            options.AddPolicy(AuthPolicies.Business, policy => policy.AddRequirements(new BusinessAccessRequirement()));
        });
        services.AddScoped<Microsoft.AspNetCore.Authorization.IAuthorizationHandler, BusinessAccessHandler>();
        services.AddScoped<Microsoft.AspNetCore.Authorization.IAuthorizationHandler, MfaAuthenticatedHandler>();

        return services;
    }

    internal static IServiceCollection AddProductAntiforgery(
        this IServiceCollection services,
        IHostEnvironment environment)
    {
        services.AddAntiforgery(options =>
        {
            options.Cookie.Name = "vantigo.products.csrf";
            // The SPA receives the request token as JSON; keeping the cookie HttpOnly avoids
            // exposing the cookie token to scripts while retaining the double-submit contract.
            options.Cookie.HttpOnly = true;
            options.Cookie.SameSite = SameSiteMode.Strict;
            options.Cookie.SecurePolicy = environment.IsDevelopment()
                ? CookieSecurePolicy.SameAsRequest
                : CookieSecurePolicy.Always;
            options.HeaderName = "X-XSRF-TOKEN";
        });

        return services;
    }
}