using System.Security.Claims;

using Microsoft.AspNetCore.Authentication;
using Microsoft.AspNetCore.Authentication.Cookies;
using Microsoft.AspNetCore.Authentication.OpenIdConnect;
using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Options;

using Vantigo.Configuration;
using Vantigo.Identity.Authorization;
using Vantigo.Identity.Database.Accounts;
using Vantigo.Identity.Services;

namespace Vantigo.Identity.Endpoints.Auth;

public static class AuthServiceCollectionExtensions
{
    public static IServiceCollection AddVantigoIdentity(
        this IServiceCollection services,
        IHostEnvironment environment)
    {
        services.AddMemoryCache();
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
        services.AddOptions<IdentityPasskeyOptions>().Configure<IOptions<AppPublicOriginOptions>>((options, originOptions) =>
        {
            // Never derive RP identity from Host: forwarded/misconfigured Host
            // headers must not be able to retarget a ceremony.
            var trustedOrigin = originOptions.Value.Normalized;
            if (trustedOrigin is null)
            {
                if (!environment.IsDevelopment())
                {
                    throw new InvalidOperationException(
                        $"{AppPublicOriginOptions.ConfigurationKey} must be configured before passkey sign-in can be used.");
                }

                trustedOrigin = "http://localhost";
            }
            options.ServerDomain = new Uri(trustedOrigin).Host;
            options.UserVerificationRequirement = "required";
            options.ResidentKeyRequirement = "required";
            options.AuthenticatorTimeout = TimeSpan.FromMinutes(5);
            options.ValidateOrigin = context => new ValueTask<bool>(
                string.Equals(context.Origin, trustedOrigin, StringComparison.OrdinalIgnoreCase));
        });
        services.AddScoped<IPasskeyHandler<ApplicationUser>, PasskeyHandler<ApplicationUser>>();
        services.Configure<IdentityOptions>(options =>
        {
            options.Tokens.AuthenticatorTokenProvider = TokenOptions.DefaultAuthenticatorProvider;
        });
        services.Configure<DataProtectionTokenProviderOptions>(options =>
        {
            options.TokenLifespan = TimeSpan.FromHours(24);
        });
        services.AddOptions<SecurityStampValidatorOptions>().Configure<IOptions<VantigoAuthenticationOptions>>((options, authentication) =>
        {
            // This interval only governs how often Identity rebuilds the cookie
            // principal from the database. It used to be zero, which meant a database
            // round trip and a Set-Cookie on every single request. Revocation no
            // longer depends on it: SessionValidationService checks the security
            // stamp itself on every validation, off a briefly cached read that is
            // always re-verified before a session is rejected.
            options.ValidationInterval = authentication.Value.Sessions.PrincipalRefreshInterval;
            options.OnRefreshingPrincipal = context =>
            {
                var currentMfa = context.CurrentPrincipal?.Claims.Where(MfaClaims.Is).ToArray() ?? [];
                if (currentMfa.Length == 0)
                {
                    return Task.CompletedTask;
                }

                var identity = context.NewPrincipal?.Identity as ClaimsIdentity;
                if (identity is null)
                {
                    return Task.CompletedTask;
                }

                foreach (var claim in currentMfa)
                {
                    identity.AddClaim(claim);
                }

                return Task.CompletedTask;
            };
        });
        services.ConfigureApplicationCookie(options =>
        {
            options.Cookie.Name = "vantigo.identity.auth";
            options.Cookie.HttpOnly = true;
            options.Cookie.SameSite = SameSiteMode.Strict;
            options.Cookie.SecurePolicy = environment.IsDevelopment()
                ? CookieSecurePolicy.SameAsRequest
                : CookieSecurePolicy.Always;
            // ExpireTimeSpan is set from configuration by SessionCookiePostConfigure,
            // which also bounds the session beyond this sliding window.
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
        services.AddScoped<SessionValidationService>();
        services.AddSingleton<IPostConfigureOptions<CookieAuthenticationOptions>, SessionCookiePostConfigure>();

        return services;
    }

    public static IServiceCollection AddWorkforceOidc(
        this IServiceCollection services,
        IHostEnvironment environment)
    {
        services.Configure<CookieAuthenticationOptions>(IdentityConstants.ExternalScheme, options =>
        {
            // The external cookie must survive the provider's top-level callback but is
            // never used as the application session. The completion endpoint consumes and
            // clears it after it has validated the external identity.
            options.Cookie.Name = "vantigo.identity.external";
            options.Cookie.HttpOnly = true;
            options.Cookie.SameSite = SameSiteMode.Lax;
            options.Cookie.SecurePolicy = environment.IsDevelopment()
                ? CookieSecurePolicy.SameAsRequest
                : CookieSecurePolicy.Always;
        });

        services.AddOpenIdConnectAuthentication(environment);

        return services;
    }

    private static IServiceCollection AddOpenIdConnectAuthentication(
        this IServiceCollection services,
        IHostEnvironment environment)
    {
        services.AddAuthentication()
            .AddOpenIdConnect(WorkforceOidcOptions.Scheme, options =>
            {
                options.SignInScheme = IdentityConstants.ExternalScheme;
                options.ResponseType = "code";
                options.UsePkce = true;
                options.RequireHttpsMetadata = !environment.IsDevelopment();
                options.SaveTokens = false;
                options.GetClaimsFromUserInfoEndpoint = false;
                options.MapInboundClaims = false;
                options.PushedAuthorizationBehavior = PushedAuthorizationBehavior.Disable;
                options.Scope.Clear();
                options.Scope.Add("openid");
                options.Scope.Add("profile");
                options.Scope.Add("email");
            });
        services.AddOptions<OpenIdConnectOptions>(WorkforceOidcOptions.Scheme)
            .Configure<WorkforceOidcOptions>((options, workforceOidc) =>
            {
                if (!workforceOidc.Enabled)
                {
                    // The scheme is intentionally present in every composition
                    // graph, but disabled configuration must still satisfy the
                    // framework's option validator if the authentication
                    // middleware materializes the scheme.
                    options.Authority = "https://disabled.vantigo.invalid";
                    options.ClientId = "disabled";
                    options.CallbackPath = WorkforceOidcOptions.DefaultCallbackPath;
                    return;
                }

                options.Authority = workforceOidc.Authority;
                options.ClientId = workforceOidc.ClientId;
                options.ClientSecret = workforceOidc.ClientSecret;
                options.CallbackPath = workforceOidc.CallbackPath;
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
                options.Events.OnAuthorizationCodeReceived = context =>
                {
                    if (!string.Equals(workforceOidc.ClientAuthentication,
                            WorkforceOidcOptions.WorkloadIdentityAuthentication, StringComparison.Ordinal))
                    {
                        return Task.CompletedTask;
                    }

                    try
                    {
                        if (context.TokenEndpointRequest is null)
                        {
                            context.Fail("The OIDC token request could not be prepared.");
                            return Task.CompletedTask;
                        }

                        // Read the projected token for every redemption. Never
                        // cache it and never configure a client secret in this mode.
                        WorkforceOidcClientAssertion.Apply(context.TokenEndpointRequest, workforceOidc);
                    }
                    catch (InvalidOperationException exception)
                    {
                        context.Fail(exception.Message);
                    }

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

                    if (context.Principal is null)
                    {
                        context.Fail("The validated OIDC principal is missing.");
                        return Task.CompletedTask;
                    }

                    var claimValidation = StaticOidcClaimValidation.Validate(context.Principal, workforceOidc);
                    if (!claimValidation.Succeeded)
                    {
                        context.Fail(claimValidation.Error ?? "The OIDC claims are invalid.");
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

        return services;
    }

    public static IServiceCollection AddVantigoAuthorization(this IServiceCollection services)
    {
        services.AddAuthorization(options =>
        {
            options.AddPolicy("ActiveAccount", policy => policy.AddRequirements(new ActiveAccountRequirement()));
            // Owner and SystemAdmin are the two privileged roles: a password
            // alone must never be enough to reach identity or tenant
            // control-plane endpoints (https://github.com/vantigo-io/vantigo/issues/15).
            // The MFA enrollment endpoints deliberately require only
            // "ActiveAccount" so a session that has not enrolled yet can still
            // reach them; see AuthAccountEndpoints.selfMfa/accountMfa.
            options.AddPolicy(AuthPolicies.SystemAdmin, policy => policy.RequireRole(AuthRoles.SystemAdmin)
                .AddRequirements(new ActiveAccountRequirement(), new MfaAuthenticatedRequirement()));
            options.AddPolicy(AuthPolicies.Owner, policy => policy.RequireRole(AuthRoles.Owner)
                .AddRequirements(new ActiveAccountRequirement(), new MfaAuthenticatedRequirement()));
            options.AddPolicy(AuthPolicies.OwnerManagement, policy =>
                policy.RequireRole(AuthRoles.Owner)
                    .AddRequirements(new ActiveAccountRequirement(), new MfaAuthenticatedRequirement()));
            options.AddPolicy(AuthPolicies.Business, policy => policy.AddRequirements(
                new ActiveAccountRequirement(), new BusinessAccessRequirement()));
            options.AddPolicy(AuthPolicies.AuthorizationManagement, policy => policy
                .AddRequirements(new ActiveAccountRequirement(), new MfaAuthenticatedRequirement(),
                    new AuthorizationManagementRequirement()));
        });
        services.AddScoped<Microsoft.AspNetCore.Authorization.IAuthorizationHandler, ActiveAccountHandler>();
        services.AddScoped<Microsoft.AspNetCore.Authorization.IAuthorizationHandler, BusinessAccessHandler>();
        services.AddScoped<Microsoft.AspNetCore.Authorization.IAuthorizationHandler, MfaAuthenticatedHandler>();
        services.AddScoped<Microsoft.AspNetCore.Authorization.IAuthorizationHandler, PermissionAuthorizationHandler>();
        services.AddScoped<Microsoft.AspNetCore.Authorization.IAuthorizationHandler, AuthorizationManagementHandler>();
        services.AddScoped<AuthorizationMutationService>();
        services.AddScoped<AuthorizationAuditWriter>();
        services.AddHttpContextAccessor();
        services.AddScoped<ScimProtocolService>();
        services.AddScoped<ScimIngressEndpointFilter>();
        services.AddSingleton<ScimIngressRateLimiter>();
        services.AddScoped<ScimLifecycleService>();
        services.AddScoped<ScimTokenService>();
        services.AddScoped<AccessGroupManagementService>();
        services.AddScoped<OperationalEventService>();
        services.AddScoped<StaticScimStateInitializer>();
        services.AddSingleton<Microsoft.AspNetCore.Authorization.IAuthorizationPolicyProvider, PermissionPolicyProvider>();

        return services;
    }

    /// <summary>
    /// Owns everything the application cookie has to check on validation: the
    /// account's effective disabled state, the security stamp that server-side
    /// revocation turns over, and the absolute and idle session bounds. It extends
    /// the event chain rather than replacing it, so Identity's own security stamp
    /// validation still runs first, and it opens one request scope for a single
    /// consolidated database read instead of one per check.
    /// </summary>
    private sealed class SessionCookiePostConfigure(
        IServiceScopeFactory scopeFactory,
        IOptions<VantigoAuthenticationOptions> authenticationOptions) : IPostConfigureOptions<CookieAuthenticationOptions>
    {
        public void PostConfigure(string? name, CookieAuthenticationOptions options)
        {
            if (!string.Equals(name, IdentityConstants.ApplicationScheme, StringComparison.Ordinal) ||
                options.Events is null)
            {
                return;
            }

            options.ExpireTimeSpan = authenticationOptions.Value.Sessions.IdleTimeout;

            var priorSigningIn = options.Events.OnSigningIn;
            options.Events.OnSigningIn = async context =>
            {
                if (priorSigningIn is not null)
                {
                    await priorSigningIn(context);
                }

                SessionValidationService.CarryForwardSessionStart(context);
            };

            var priorValidation = options.Events.OnValidatePrincipal;
            options.Events.OnValidatePrincipal = async context =>
            {
                if (priorValidation is not null)
                {
                    await priorValidation(context);
                }

                if (context.Principal?.Identity?.IsAuthenticated != true)
                {
                    return;
                }

                await using var scope = scopeFactory.CreateAsyncScope();
                var sessions = scope.ServiceProvider.GetRequiredService<SessionValidationService>();
                await sessions.ValidateAsync(context);
            };
        }
    }

    public static IServiceCollection AddVantigoAntiforgery(
        this IServiceCollection services,
        IHostEnvironment environment)
    {
        services.AddAntiforgery(options =>
        {
            options.Cookie.Name = "vantigo.identity.csrf";
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