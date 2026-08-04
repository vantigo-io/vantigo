using System.Security.Claims;
using System.Threading.RateLimiting;

using Asp.Versioning;

using Microsoft.AspNetCore.Authentication.Cookies;
using Microsoft.AspNetCore.Authentication.OpenIdConnect;
using Microsoft.AspNetCore.Identity;
using Microsoft.AspNetCore.RateLimiting;

using Vantigo.Customers.Api;
using Vantigo.Customers.Api.Database;
using Vantigo.Customers.Api.Database.Accounts;
using Vantigo.Customers.Api.Endpoints;
using Vantigo.Customers.Api.Endpoints.Auth;
using Vantigo.Customers.Api.Endpoints.Lookup;
using Vantigo.Customers.Api.Services;

var builder = WebApplication.CreateBuilder(args);
var workforceOidc = WorkforceOidcOptions.Load(builder.Configuration, builder.Environment);

builder.Services
    .AddApiVersioning(options =>
    {
        options.DefaultApiVersion = new ApiVersion(1);
        options.ApiVersionReader = new UrlSegmentApiVersionReader();
        options.ReportApiVersions = true;
    })
    .AddApiExplorer(options =>
    {
        // Format the group name as "v1" so the OpenAPI documents are exposed at
        // /openapi/v1.json, matching the default Microsoft.AspNetCore.OpenApi behavior.
        options.GroupNameFormat = "'v'VVV";

        // Replace the {version:apiVersion} route template parameter with the actual
        // version number in the generated OpenAPI paths.
        options.SubstituteApiVersionInUrl = true;
    })
    // Asp.Versioning's AddOpenApi must be used (instead of Microsoft.AspNetCore.OpenApi's)
    // to generate versioned OpenAPI documents.
    .AddOpenApi();

builder.Services.AddCustomerDatabases(builder.Configuration);
builder.Services.AddSingleton<BootstrapSecretProvider>();
InfrastructureConfiguration.AddConfiguredDataProtection(builder.Services, builder.Configuration, builder.Environment);
InfrastructureConfiguration.ConfigureForwardedHeaders(builder.Services, builder.Configuration);
builder.Services
    .AddIdentity<ApplicationUser, IdentityRole<Guid>>(options =>
    {
        options.User.RequireUniqueEmail = true;
        options.Password.RequiredLength = 12;
        options.Password.RequireDigit = true;
        options.Password.RequireUppercase = true;
        options.Password.RequireLowercase = true;
        options.Password.RequireNonAlphanumeric = false;
        options.Lockout.AllowedForNewUsers = true;
        options.Lockout.MaxFailedAccessAttempts = 5;
        options.Lockout.DefaultLockoutTimeSpan = TimeSpan.FromMinutes(15);
    })
    .AddEntityFrameworkStores<AccountsDbContext>()
    .AddDefaultTokenProviders();
builder.Services.AddSingleton(workforceOidc);
builder.Services.Configure<CookieAuthenticationOptions>(IdentityConstants.ExternalScheme, options =>
{
    // The external cookie must survive the provider's top-level callback but is
    // never used as the application session. The completion endpoint consumes and
    // clears it after it has validated the external identity.
    options.Cookie.Name = "vantigo.customers.external";
    options.Cookie.HttpOnly = true;
    options.Cookie.SameSite = SameSiteMode.Lax;
    options.Cookie.SecurePolicy = builder.Environment.IsDevelopment()
        ? CookieSecurePolicy.SameAsRequest
        : CookieSecurePolicy.Always;
});
if (workforceOidc.Enabled)
{
    builder.Services.AddAuthentication()
        .AddOpenIdConnect(WorkforceOidcOptions.Scheme, options =>
        {
            options.Authority = workforceOidc.Authority;
            options.ClientId = workforceOidc.ClientId;
            options.ClientSecret = workforceOidc.ClientSecret;
            options.SignInScheme = IdentityConstants.ExternalScheme;
            options.CallbackPath = workforceOidc.CallbackPath;
            options.ResponseType = "code";
            options.UsePkce = true;
            options.RequireHttpsMetadata = !builder.Environment.IsDevelopment();
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
builder.Services.Configure<IdentityOptions>(options =>
{
    options.Tokens.AuthenticatorTokenProvider = TokenOptions.DefaultAuthenticatorProvider;
});
builder.Services.Configure<DataProtectionTokenProviderOptions>(options =>
{
    options.TokenLifespan = TimeSpan.FromHours(24);
});
builder.Services.Configure<SecurityStampValidatorOptions>(options =>
{
    options.ValidationInterval = TimeSpan.Zero;
});
builder.Services.Configure<EmailOptions>(builder.Configuration.GetSection("Email"));
if (string.Equals(builder.Configuration["Email:Provider"], "Smtp", StringComparison.OrdinalIgnoreCase))
{
    builder.Services.AddSingleton<IApplicationEmailSender, SmtpApplicationEmailSender>();
}
else
{
    builder.Services.AddSingleton<IApplicationEmailSender, LoggingApplicationEmailSender>();
}
builder.Services.AddAuthorization(options =>
{
    options.AddPolicy(AuthPolicies.Owner, policy => policy.RequireRole(AuthRoles.Owner));
    options.AddPolicy(AuthPolicies.OwnerManagement, policy =>
        policy.RequireRole(AuthRoles.Owner).AddRequirements(new MfaAuthenticatedRequirement()));
    options.AddPolicy(AuthPolicies.Business, policy => policy.AddRequirements(new BusinessAccessRequirement()));
});
builder.Services.AddScoped<Microsoft.AspNetCore.Authorization.IAuthorizationHandler, BusinessAccessHandler>();
builder.Services.AddScoped<Microsoft.AspNetCore.Authorization.IAuthorizationHandler, MfaAuthenticatedHandler>();
builder.Services.ConfigureApplicationCookie(options =>
{
    options.Cookie.Name = "vantigo.customers.auth";
    options.Cookie.HttpOnly = true;
    options.Cookie.SameSite = SameSiteMode.Strict;
    options.Cookie.SecurePolicy = builder.Environment.IsDevelopment()
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
builder.Services.AddAntiforgery(options =>
{
    options.Cookie.Name = "vantigo.customers.csrf";
    // The SPA receives the request token as JSON; keeping the cookie HttpOnly avoids
    // exposing the cookie token to scripts while retaining the double-submit contract.
    options.Cookie.HttpOnly = true;
    options.Cookie.SameSite = SameSiteMode.Strict;
    options.Cookie.SecurePolicy = builder.Environment.IsDevelopment()
        ? CookieSecurePolicy.SameAsRequest
        : CookieSecurePolicy.Always;
    options.HeaderName = "X-XSRF-TOKEN";
});
builder.Services.AddRateLimiter(options =>
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
            InfrastructureConfiguration.GetRateLimitPartitionKey(context),
            _ => new FixedWindowRateLimiterOptions
            {
                PermitLimit = 100,
                Window = TimeSpan.FromMinutes(1),
                QueueLimit = 0,
                AutoReplenishment = true,
            }));
    options.AddPolicy(AuthRateLimitPolicies.Bootstrap, context =>
        RateLimitPartition.GetFixedWindowLimiter(
            InfrastructureConfiguration.GetRateLimitPartitionKey(context),
            _ => new FixedWindowRateLimiterOptions
            {
                PermitLimit = 20,
                Window = TimeSpan.FromMinutes(1),
                QueueLimit = 0,
                AutoReplenishment = true,
            }));
    options.AddPolicy(AuthRateLimitPolicies.Invitations, context =>
        RateLimitPartition.GetFixedWindowLimiter(InfrastructureConfiguration.GetRateLimitPartitionKey(context),
            _ => new FixedWindowRateLimiterOptions { PermitLimit = 30, Window = TimeSpan.FromMinutes(1), QueueLimit = 0 }));
    options.AddPolicy(AuthRateLimitPolicies.InvitationAcceptance, context =>
        RateLimitPartition.GetFixedWindowLimiter(InfrastructureConfiguration.GetRateLimitPartitionKey(context),
            _ => new FixedWindowRateLimiterOptions { PermitLimit = 20, Window = TimeSpan.FromMinutes(1), QueueLimit = 0 }));
    options.AddPolicy(AuthRateLimitPolicies.PasswordRecovery, context =>
        RateLimitPartition.GetFixedWindowLimiter(InfrastructureConfiguration.GetRateLimitPartitionKey(context),
            _ => new FixedWindowRateLimiterOptions { PermitLimit = 10, Window = TimeSpan.FromMinutes(15), QueueLimit = 0 }));
    options.AddPolicy(AuthRateLimitPolicies.Mfa, context =>
        RateLimitPartition.GetFixedWindowLimiter(InfrastructureConfiguration.GetRateLimitPartitionKey(context),
            _ => new FixedWindowRateLimiterOptions { PermitLimit = 20, Window = TimeSpan.FromMinutes(5), QueueLimit = 0 }));
});
builder.Services.AddScoped<ICustomerTimelineRecorder, CustomerTimelineRecorder>();

// Named client for the open Brønnøysundregisteret (Enhetsregisteret) API used by
// the /lookup/brreg endpoint. The base URL is configurable so tests and other
// environments can point it at a stub.
builder.Services.AddHttpClient(BrregLookupEndpoint.HttpClientName, client =>
{
    client.BaseAddress = new Uri(builder.Configuration["Brreg:BaseUrl"] ?? "https://data.brreg.no");
    client.Timeout = TimeSpan.FromSeconds(5);
});

var app = builder.Build();
// Force creation at startup so a generated secret is logged exactly once.
_ = app.Services.GetRequiredService<BootstrapSecretProvider>();

// Development keeps the existing convenient startup migration behavior. Other
// environments require the explicit opt-in flag so deploys can run migrations as
// a deliberate operation instead of every application instance racing at startup.
if (app.Environment.IsDevelopment() || builder.Configuration.GetValue<bool>("Database:ApplyMigrationsOnStartup"))
{
    await app.MigrateCustomerDatabasesAsync();
}

app.MapOpenApi().WithDocumentPerVersion();

// Serve the built SPA (embedded into wwwroot on publish) from "/". In development
// the frontend runs on the Vite dev server, which proxies /api to this API.
app.UseForwardedHeaders();
app.UseDefaultFiles();
app.UseStaticFiles();
app.UseRouting();
app.UseRateLimiter();
app.UseAuthentication();
app.UseAuthorization();
app.UseAntiforgery();

app.MapAuthEndpoints(workforceOidc);

var api = app.NewVersionedApi()
    .MapGroup("/api/v{version:apiVersion}")
    .HasApiVersion(new ApiVersion(1))
    .RequireAuthorization(AuthPolicies.Business);

api.MapCustomersEndpoints();
api.MapContactsEndpoints();
api.MapLookupEndpoints();

// Never let an unrecognized API/auth URL be mistaken for an SPA deep link.
app.Map("/api", () => Results.NotFound());
app.Map("/api/{**path}", () => Results.NotFound());
app.Map("/auth", () => Results.NotFound());
app.Map("/auth/{**path}", () => Results.NotFound());

// Deep links like /customers must fall back to the SPA entry point. API and
// OpenAPI endpoints match their own routes first and are unaffected.
app.MapFallbackToFile("index.html");

app.Run();

/// <summary>
/// Exposes the implicit <c>Program</c> class to the test project so integration
/// tests can bootstrap the API through <c>WebApplicationFactory</c>.
/// </summary>
public partial class Program;
