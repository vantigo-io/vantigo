using System.IdentityModel.Tokens.Jwt;
using System.Net;
using System.Net.Sockets;
using System.Security.Claims;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;

using Microsoft.AspNetCore.Authentication;
using Microsoft.AspNetCore.DataProtection;
using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Storage;
using Microsoft.IdentityModel.Tokens;

using Vantigo.Configuration;
using Vantigo.Identity.Authorization;
using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Identity.Services;

public static class DynamicFederationAuthentication
{
    public const string Scheme = "VantigoFederationOidc";
    public const string CallbackPath = "/api/v1/identity/federation/callback";
    public const string ConnectionIdItem = "federation:connection-id";
    public const string ConfigurationVersionItem = "federation:configuration-version";
    public const string ReturnPathItem = "federation:return-path";
}

public sealed record FederationProviderDiscoveryResponse(Guid Id, string DisplayName, string ProviderKind);

public sealed class DynamicFederationOidcService(
    AccountsDbContext dbContext,
    IDataProtectionProvider dataProtectionProvider,
    IFederationClientSecretResolver secretResolver,
    IHttpClientFactory httpClientFactory,
    IFederationHostAddressResolver hostAddressResolver,
    UserManager<ApplicationUser> userManager,
    SignInManager<ApplicationUser> signInManager,
    ScimLifecycleService scimLifecycleService,
    AuthorizationAuditWriter auditWriter,
    IServiceScopeFactory scopeFactory,
    IHostEnvironment environment,
    AppPublicUrls publicUrls)
{
    private const string StatePurpose = "Vantigo.Identity.DynamicFederationOidc.State.v1";
    private static readonly TimeSpan StateLifetime = TimeSpan.FromMinutes(10);

    internal static Func<Exception?>? JitConflictInjector { get; set; }
    internal static Func<CancellationToken, Task<Exception?>>? JitConflictInjectorAsync { get; set; }
    internal static Action<string>? JitRecoveryObserver { get; set; }

    public Task<List<FederationProviderDiscoveryResponse>> ListProvidersAsync(CancellationToken cancellationToken) =>
        dbContext.FederationConnections.AsNoTracking()
            .Where(connection => connection.IsEnabled &&
                connection.ValidationState == FederationConnectionValidationState.Succeeded &&
                connection.ValidatedConfigurationVersion == connection.ConfigurationVersion &&
                connection.ValidatedIssuer != null && connection.ValidatedAuthorizationEndpoint != null &&
                connection.ValidatedTokenEndpoint != null && connection.ValidatedJwksUri != null)
            .OrderBy(connection => connection.DisplayName)
            .ThenBy(connection => connection.Id)
            .Select(connection => new FederationProviderDiscoveryResponse(
                connection.Id,
                connection.DisplayName,
                connection.ProviderKind.ToString()))
            .ToListAsync(cancellationToken);

    public Task<FederationConnection?> GetCurrentConnectionAsync(Guid id, CancellationToken cancellationToken) =>
        dbContext.FederationConnections.AsNoTracking()
            .SingleOrDefaultAsync(connection => connection.Id == id && connection.IsEnabled &&
                connection.ValidationState == FederationConnectionValidationState.Succeeded &&
                connection.ValidatedConfigurationVersion == connection.ConfigurationVersion &&
                connection.ValidatedIssuer != null && connection.ValidatedAuthorizationEndpoint != null &&
                connection.ValidatedTokenEndpoint != null && connection.ValidatedJwksUri != null, cancellationToken);

    public async Task<DynamicFederationChallenge?> CreateChallengeAsync(
        AuthenticationProperties properties,
        HttpContext httpContext,
        CancellationToken cancellationToken)
    {
        if (!Guid.TryParse(properties.Items.TryGetValue(DynamicFederationAuthentication.ConnectionIdItem, out var connectionIdValue) ? connectionIdValue : null, out var connectionId) ||
            !int.TryParse(properties.Items.TryGetValue(DynamicFederationAuthentication.ConfigurationVersionItem, out var versionValue) ? versionValue : null, out var version) ||
            !TryValidateReturnPath(properties.Items.TryGetValue(DynamicFederationAuthentication.ReturnPathItem, out var returnPathValue) ? returnPathValue : null, out var returnPath))
        {
            return null;
        }

        var connection = await dbContext.FederationConnections.AsNoTracking()
            .SingleOrDefaultAsync(item => item.Id == connectionId && item.IsEnabled &&
                item.ValidationState == FederationConnectionValidationState.Succeeded &&
                item.ValidatedConfigurationVersion == item.ConfigurationVersion &&
                item.ValidatedIssuer != null && item.ValidatedAuthorizationEndpoint != null &&
                item.ValidatedTokenEndpoint != null && item.ValidatedJwksUri != null, cancellationToken);
        if (connection is null || connection.ConfigurationVersion != version ||
            string.IsNullOrWhiteSpace(connection.ClientSecretReference) ||
            secretResolver.Resolve(connection.ClientSecretReference).Status != FederationClientSecretResolutionStatus.Configured ||
            !TryGetHttpsEndpoint(connection.ValidatedAuthorizationEndpoint, out var authorizationEndpoint) ||
            !TryGetHttpsEndpoint(connection.ValidatedTokenEndpoint, out _) ||
            !TryGetHttpsEndpoint(connection.ValidatedJwksUri, out _) ||
            !WorkforceOidcOptions.TryNormalizeIssuer(connection.ValidatedIssuer, out var issuer))
        {
            return null;
        }

        if (!await FederationEndpointPolicy.IsPublicEndpointAsync(authorizationEndpoint!, hostAddressResolver, cancellationToken))
        {
            return null;
        }

        var callbackUrl = GetCallbackUrl(httpContext);
        if (callbackUrl is null)
        {
            return null;
        }

        var nonce = RandomToken(32);
        var correlationId = RandomToken(24);
        var correlationCookieName = $"vantigo.identity.federation.correlation.{correlationId}";
        var verifier = RandomToken(48);
        var stateId = Guid.NewGuid();
        var stateIdValue = stateId.ToString("N");
        var state = ProtectState(new FederationOidcState(
            connection.Id,
            connection.ConfigurationVersion,
            returnPath!,
            nonce,
            verifier,
            DateTimeOffset.UtcNow,
            correlationCookieName,
            stateIdValue));

        await CleanupExpiredStatesAsync(cancellationToken);
        dbContext.FederationOidcStates.Add(new Database.Accounts.FederationOidcState
        {
            StateId = stateId,
            StateHash = HashOpaque(stateIdValue),
            IssuedAt = DateTimeOffset.UtcNow,
            ExpiresAt = DateTimeOffset.UtcNow.Add(StateLifetime),
        });
        await dbContext.SaveChangesAsync(cancellationToken);

        var challengeCode = Base64Url(SHA256.HashData(Encoding.ASCII.GetBytes(verifier)));
        var query = new Dictionary<string, string?>
        {
            ["client_id"] = connection.ClientId,
            ["response_type"] = "code",
            ["redirect_uri"] = callbackUrl,
            ["scope"] = "openid profile email",
            ["state"] = state,
            ["nonce"] = nonce,
            ["code_challenge"] = challengeCode,
            ["code_challenge_method"] = "S256",
        };
        var authorizationUrl = Microsoft.AspNetCore.WebUtilities.QueryHelpers.AddQueryString(
            authorizationEndpoint!, query);

        return new DynamicFederationChallenge(
            authorizationUrl,
            nonce,
            correlationCookieName,
            new CookieOptions
            {
                HttpOnly = true,
                SameSite = SameSiteMode.Lax,
                Secure = !environment.IsDevelopment(),
                IsEssential = true,
                MaxAge = StateLifetime,
                Path = "/",
            });
    }

    public async Task<IResult> CompleteAsync(HttpContext httpContext, CancellationToken cancellationToken)
    {
        var stateValue = httpContext.Request.Query["state"].ToString();
        var code = httpContext.Request.Query["code"].ToString();
        if (string.IsNullOrWhiteSpace(stateValue) || string.IsNullOrWhiteSpace(code) ||
            httpContext.Request.Query.ContainsKey("error"))
        {
            return Failure();
        }

        FederationOidcState? state;
        try
        {
            state = UnprotectState(stateValue);
        }
        catch (Exception)
        {
            return Failure();
        }

        if (state is not null)
        {
            // The state is protected, so this is the only flow cookie name the
            // server can derive. Never clear another concurrent federation flow.
            httpContext.Response.Cookies.Delete(state.CorrelationCookieName, new CookieOptions { Path = "/" });
        }

        if (state is null || DateTimeOffset.UtcNow - state.IssuedAt > StateLifetime ||
            !TryValidateReturnPath(state.ReturnPath, out var returnPath) ||
            !FixedTimeEquals(httpContext.Request.Cookies[state.CorrelationCookieName], state.Nonce) ||
            !Guid.TryParseExact(state.StateId, "N", out var stateId) ||
            !await ConsumeStateAsync(stateId, cancellationToken))
        {
            return Failure();
        }

        var connection = await dbContext.FederationConnections.AsNoTracking()
            .SingleOrDefaultAsync(item => item.Id == state.ConnectionId && item.IsEnabled &&
                item.ValidationState == FederationConnectionValidationState.Succeeded &&
                item.ValidatedConfigurationVersion == item.ConfigurationVersion &&
                item.ValidatedIssuer != null && item.ValidatedAuthorizationEndpoint != null &&
                item.ValidatedTokenEndpoint != null && item.ValidatedJwksUri != null, cancellationToken);
        if (connection is null || connection.ConfigurationVersion != state.ConfigurationVersion ||
            string.IsNullOrWhiteSpace(connection.ClientSecretReference) ||
            !TryGetHttpsEndpoint(connection.ValidatedTokenEndpoint, out var tokenEndpoint) ||
            !TryGetHttpsEndpoint(connection.ValidatedJwksUri, out var jwksEndpoint) ||
            !WorkforceOidcOptions.TryNormalizeIssuer(connection.ValidatedIssuer, out var issuer))
        {
            return Failure();
        }

        var secret = secretResolver.Resolve(connection.ClientSecretReference);
        if (secret.Status != FederationClientSecretResolutionStatus.Configured || secret.Secret is null)
        {
            return Failure();
        }

        var callbackUrl = GetCallbackUrl(httpContext);
        if (callbackUrl is null)
        {
            return Failure();
        }

        string? token;
        IReadOnlyCollection<SecurityKey>? signingKeys;
        try
        {
            token = await ExchangeCodeAsync(tokenEndpoint!, connection, secret.Secret, code, state, callbackUrl, cancellationToken);
            signingKeys = token is null ? null : await GetSigningKeysAsync(jwksEndpoint!, cancellationToken);
        }
        catch (Exception)
        {
            return Failure();
        }
        if (token is null)
        {
            return Failure();
        }

        if (signingKeys is null || !TryValidateIdToken(token, signingKeys, connection, issuer!, state.Nonce, out var principal))
        {
            return Failure();
        }

        var identity = ReadExternalIdentity(principal!, connection, issuer!);
        if (identity is null || !AllowedDomain(identity, connection))
        {
            return Failure();
        }

        // Entra's directory object identity is the authoritative SCIM
        // correlation. Resolve it before the existing federated-login and JIT
        // paths so an SCIM-provisioned account can never be bypassed by an
        // email collision or a second local user.
        if (connection.ProviderKind == FederationProviderKind.Entra &&
            identity.DirectoryTenantId is not null && identity.DirectoryObjectId is { } objectId &&
            Guid.TryParse(identity.DirectoryTenantId, out var tenantId))
        {
            var resolution = await scimLifecycleService.ResolveEntraMappingForFederationAsync(
                connection.Id, identity.Issuer, tenantId, objectId, cancellationToken);
            if (resolution.Kind is ScimEntraMappingResolutionKind.Disabled or
                ScimEntraMappingResolutionKind.OwnerRejected or
                ScimEntraMappingResolutionKind.Ambiguous)
            {
                return Failure();
            }

            if (resolution.IsActive)
            {
                var attached = await scimLifecycleService.AttachEntraIdentityToMappingAsync(
                    resolution.Mapping!, connection.Id, identity.Issuer, identity.Subject,
                    tenantId, objectId, cancellationToken);
                return attached is null
                    ? Failure()
                    : await SignInLinkedIdentityAsync(httpContext, connection, identity, attached, returnPath!, cancellationToken);
            }
        }

        var linked = await dbContext.FederatedIdentities
            .SingleOrDefaultAsync(item => item.ConnectionId == connection.Id && item.Issuer == identity.Issuer &&
                item.Subject == identity.Subject, cancellationToken);
        if (linked is not null)
        {
            return await SignInLinkedIdentityAsync(httpContext, connection, identity, linked, returnPath!, cancellationToken);
        }

        if (connection.JitCreationMode != JitCreationMode.CreateUser)
        {
            return Failure();
        }

        if (identity.Email is not null && await userManager.FindByEmailAsync(identity.Email) is not null)
        {
            return Failure();
        }

        IDbContextTransaction? jitTransaction = null;
        try
        {
            jitTransaction = await dbContext.Database.BeginTransactionAsync(
                System.Data.IsolationLevel.Serializable, cancellationToken);

            if (JitConflictInjectorAsync is not null &&
                await JitConflictInjectorAsync(cancellationToken) is { } transactionConflict)
            {
                throw transactionConflict;
            }

            // Recheck inside the transaction so two first-login requests cannot
            // provision two local accounts for one provider identity.
            var raced = await dbContext.FederatedIdentities.SingleOrDefaultAsync(item =>
                item.ConnectionId == connection.Id && item.Issuer == identity.Issuer && item.Subject == identity.Subject,
                cancellationToken);
            if (raced is not null)
            {
                await jitTransaction.RollbackAsync(cancellationToken);
                return Failure();
            }

            if (identity.Email is not null && await userManager.FindByEmailAsync(identity.Email) is not null)
            {
                await jitTransaction.RollbackAsync(cancellationToken);
                return Failure();
            }

            var user = new ApplicationUser
            {
                UserName = StableUserName(connection.Id, identity.Issuer, identity.Subject),
                Email = identity.Email ?? CreateOpaqueEmail(connection.Id, identity.Issuer, identity.Subject),
                EmailConfirmed = identity.EmailVerified,
                DisplayName = identity.DisplayName,
            };
            var createResult = await userManager.CreateAsync(user);
            if (!createResult.Succeeded)
            {
                await jitTransaction.RollbackAsync(cancellationToken);
                return Failure();
            }

            var directoryTenantId = identity.DirectoryTenantId;
            var directoryObjectId = identity.DirectoryObjectId;
            var provider = FederationLoginProvider(connection.Id, identity.Issuer);
            var loginResult = await userManager.AddLoginAsync(user, new UserLoginInfo(provider, identity.Subject, connection.DisplayName));
            if (!loginResult.Succeeded)
            {
                await jitTransaction.RollbackAsync(cancellationToken);
                return Failure();
            }
            dbContext.FederatedIdentities.Add(new FederatedIdentity
            {
                ConnectionId = connection.Id,
                Issuer = identity.Issuer,
                Subject = identity.Subject,
                DirectoryTenantId = directoryTenantId,
                DirectoryObjectId = directoryObjectId,
                UserId = user.Id,
                CreatedAt = DateTimeOffset.UtcNow,
                UpdatedAt = DateTimeOffset.UtcNow,
            });
            await dbContext.SaveChangesAsync(cancellationToken);
            await auditWriter.WriteAsync(dbContext, httpContext, null, user.Id, null,
                "federation.identity-jit-created",
                new { Identity = (object?)null },
                new
                {
                    ConnectionId = connection.Id,
                    UserId = user.Id,
                    Source = "oidc-jit",
                    DirectoryTenantId = directoryTenantId,
                    DirectoryObjectId = directoryObjectId
                }, cancellationToken);
            if (JitConflictInjector?.Invoke() is { } injected)
            {
                throw injected;
            }
            await jitTransaction.CommitAsync(cancellationToken);
            await jitTransaction.DisposeAsync();
            jitTransaction = null;
            var result = await signInManager.ExternalLoginSignInAsync(provider, identity.Subject, false, false);
            return result.Succeeded ? TypedResults.Redirect(returnPath!) :
                result.RequiresTwoFactor ? TypedResults.Redirect("/sign-in?mfa=federation") : Failure();
        }
        catch (Exception exception) when (IsExpectedJitConflict(exception))
        {
            if (jitTransaction is not null)
            {
                try { await jitTransaction.RollbackAsync(CancellationToken.None); }
                catch (Exception) { }
                try { await jitTransaction.DisposeAsync(); }
                catch (Exception) { }
                jitTransaction = null;
            }
            return await RecoverWinningIdentityAsync(httpContext, connection, identity, returnPath!, cancellationToken);
        }
        catch (Exception)
        {
            if (jitTransaction is not null)
            {
                try { await jitTransaction.RollbackAsync(CancellationToken.None); }
                catch (Exception) { }
                try { await jitTransaction.DisposeAsync(); }
                catch (Exception) { }
            }
            return Failure();
        }
    }

    private async Task<string?> ExchangeCodeAsync(
        string tokenEndpoint,
        FederationConnection connection,
        string secret,
        string code,
        FederationOidcState state,
        string callbackUrl,
        CancellationToken cancellationToken)
    {
        if (!await FederationEndpointPolicy.IsPublicEndpointAsync(tokenEndpoint, hostAddressResolver, cancellationToken))
        {
            return null;
        }

        using var request = new HttpRequestMessage(HttpMethod.Post, tokenEndpoint)
        {
            Content = new FormUrlEncodedContent(new Dictionary<string, string>
            {
                ["grant_type"] = "authorization_code",
                ["code"] = code,
                ["redirect_uri"] = callbackUrl,
                ["client_id"] = connection.ClientId,
                ["client_secret"] = secret,
                ["code_verifier"] = state.CodeVerifier,
            }),
        };
        request.Headers.Accept.ParseAdd("application/json");
        using var response = await httpClientFactory.CreateClient(OidcFederationDiscoveryValidator.HttpClientName)
            .SendAsync(request, HttpCompletionOption.ResponseHeadersRead, cancellationToken);
        if (!response.IsSuccessStatusCode)
        {
            return null;
        }

        try
        {
            using var document = JsonDocument.Parse(await FederationEndpointPolicy.ReadBoundedAsync(response.Content, cancellationToken));
            return document.RootElement.TryGetProperty("id_token", out var idToken) &&
                idToken.ValueKind == JsonValueKind.String && !string.IsNullOrWhiteSpace(idToken.GetString())
                ? idToken.GetString()
                : null;
        }
        catch (Exception)
        {
            return null;
        }
    }

    private async Task<IReadOnlyCollection<SecurityKey>?> GetSigningKeysAsync(
        string jwksEndpoint,
        CancellationToken cancellationToken)
    {
        if (!await FederationEndpointPolicy.IsPublicEndpointAsync(jwksEndpoint, hostAddressResolver, cancellationToken))
        {
            return null;
        }

        using var request = new HttpRequestMessage(HttpMethod.Get, jwksEndpoint);
        request.Headers.Accept.ParseAdd("application/json");
        using var response = await httpClientFactory.CreateClient(OidcFederationDiscoveryValidator.HttpClientName)
            .SendAsync(request, HttpCompletionOption.ResponseHeadersRead, cancellationToken);
        if (!response.IsSuccessStatusCode)
        {
            return null;
        }

        try
        {
            var json = Encoding.UTF8.GetString(await FederationEndpointPolicy.ReadBoundedAsync(response.Content, cancellationToken));
            return new JsonWebKeySet(json).GetSigningKeys().ToArray();
        }
        catch (Exception)
        {
            return null;
        }
    }

    internal static bool TryValidateIdToken(
        string idToken,
        IReadOnlyCollection<SecurityKey> signingKeys,
        FederationConnection connection,
        string issuer,
        string nonce,
        out ClaimsPrincipal? principal)
    {
        principal = null;
        try
        {
            var handler = new JwtSecurityTokenHandler { MapInboundClaims = false };
            var parameters = new TokenValidationParameters
            {
                ValidateIssuer = true,
                ValidIssuer = issuer,
                IssuerValidator = (value, _, _) => string.Equals(value, issuer, StringComparison.Ordinal)
                    ? value
                    : throw new SecurityTokenInvalidIssuerException(),
                ValidateAudience = true,
                ValidAudience = connection.ClientId,
                ValidateIssuerSigningKey = true,
                IssuerSigningKeys = signingKeys,
                RequireSignedTokens = true,
                ValidateLifetime = true,
                ClockSkew = TimeSpan.FromMinutes(2),
                NameClaimType = "name",
                RoleClaimType = "role",
            };
            principal = handler.ValidateToken(idToken, parameters, out var validatedToken);
            if (validatedToken is JwtSecurityToken jwt && jwt.Audiences.Count() > 1 &&
                !string.Equals(principal.FindFirst("azp")?.Value, connection.ClientId, StringComparison.Ordinal))
            {
                return false;
            }
            var tokenNonce = principal.FindFirst("nonce")?.Value;
            return FixedTimeEquals(tokenNonce, nonce);
        }
        catch (Exception)
        {
            principal = null;
            return false;
        }
    }

    internal static ExternalIdentity? ReadExternalIdentity(
        ClaimsPrincipal principal,
        FederationConnection connection,
        string issuer)
    {
        var subject = principal.FindFirst("sub")?.Value;
        if (string.IsNullOrWhiteSpace(subject) || subject.Length > 512 || subject.Any(char.IsWhiteSpace))
        {
            return null;
        }

        if (connection.ProviderKind == FederationProviderKind.Entra && !ValidateEntraClaims(principal, issuer))
        {
            return null;
        }

        var email = ReadEmail(principal.FindFirst("email")?.Value);
        var emailVerified = string.Equals(principal.FindFirst("email_verified")?.Value, "true", StringComparison.OrdinalIgnoreCase);
        var displayName = ReadDisplayName(principal, subject);
        var tid = principal.FindFirst("tid")?.Value;
        var oid = principal.FindFirst("oid")?.Value;
        return new ExternalIdentity(issuer, subject, email, emailVerified, displayName,
            principal.FindFirst("hd")?.Value,
            connection.ProviderKind == FederationProviderKind.Entra ? tid : null,
            connection.ProviderKind == FederationProviderKind.Entra && Guid.TryParse(oid, out var objectId)
                ? objectId : null);
    }

    internal static bool ValidateEntraClaims(ClaimsPrincipal principal, string issuer)
    {
        var tid = principal.FindFirst("tid")?.Value;
        var oid = principal.FindFirst("oid")?.Value;
        if (string.IsNullOrWhiteSpace(tid) || string.IsNullOrWhiteSpace(oid) || !Guid.TryParse(tid, out _) || !Guid.TryParse(oid, out var objectId))
        {
            return false;
        }

        return TryGetEntraTenantFromIssuer(issuer, out var tenantId) &&
            string.Equals(tid, tenantId.ToString(), StringComparison.OrdinalIgnoreCase);
    }

    internal static bool TryGetEntraTenantFromIssuer(string? issuer, out Guid tenantId)
    {
        tenantId = Guid.Empty;
        if (string.IsNullOrWhiteSpace(issuer) || !Uri.TryCreate(issuer, UriKind.Absolute, out var uri))
            return false;

        var pathSegments = uri.AbsolutePath.Trim('/').Split('/', StringSplitOptions.RemoveEmptyEntries);
        var tenant = pathSegments.FirstOrDefault(segment =>
            !string.Equals(segment, "v2.0", StringComparison.OrdinalIgnoreCase));
        return tenant is not null && Guid.TryParse(tenant, out tenantId);
    }

    internal static bool AllowedDomain(ExternalIdentity identity, FederationConnection connection)
    {
        if (connection.ProviderKind == FederationProviderKind.Google)
        {
            // Google Workspace domain claims are a policy signal, never an
            // optional hint. Fail closed before the generic no-domain branch.
            if (connection.AllowedDomains.Length == 0 || !identity.EmailVerified ||
                identity.Email is null || string.IsNullOrWhiteSpace(identity.ProviderHd) ||
                !TryEmailDomain(identity.Email, out var googleEmailDomain) ||
                !string.Equals(identity.ProviderHd.TrimEnd('.'), googleEmailDomain,
                    StringComparison.OrdinalIgnoreCase))
            {
                return false;
            }

            var googleAllowed = connection.AllowedDomains
                .Select(domain => domain.Trim().TrimEnd('.').ToLowerInvariant())
                .ToHashSet(StringComparer.Ordinal);
            return googleAllowed.Contains(googleEmailDomain) &&
                googleAllowed.Contains(identity.ProviderHd.TrimEnd('.').ToLowerInvariant());
        }

        if (connection.AllowedDomains.Length == 0)
        {
            return true;
        }

        if (!identity.EmailVerified || identity.Email is null ||
            !TryEmailDomain(identity.Email, out var emailDomain))
        {
            return false;
        }

        var allowed = connection.AllowedDomains
            .Select(domain => domain.Trim().TrimEnd('.').ToLowerInvariant())
            .ToHashSet(StringComparer.Ordinal);
        return allowed.Contains(emailDomain);
    }

    private static string? ReadEmail(string? value) =>
        !string.IsNullOrWhiteSpace(value) && value.Length <= 256 && !value.Any(char.IsWhiteSpace) &&
        value.Count(character => character == '@') == 1 && TryEmailDomain(value, out _) ? value : null;

    private static bool TryEmailDomain(string email, out string domain)
    {
        var at = email.LastIndexOf('@');
        domain = at > 0 && at < email.Length - 1 ? email[(at + 1)..].TrimEnd('.').ToLowerInvariant() : string.Empty;
        return domain.Length > 0 && domain.Contains('.') && !domain.Any(char.IsControl);
    }

    private static string ReadDisplayName(ClaimsPrincipal principal, string subject) =>
        Clean(principal.FindFirst("name")?.Value) ??
        Clean(string.Join(' ', new[] { principal.FindFirst("given_name")?.Value, principal.FindFirst("family_name")?.Value }
            .Where(value => !string.IsNullOrWhiteSpace(value)))) ??
        Clean(principal.FindFirst("preferred_username")?.Value) ??
        (subject.Length > 200 ? subject[..200] : subject);

    private static string? Clean(string? value)
    {
        if (string.IsNullOrWhiteSpace(value) || value.Any(char.IsControl)) return null;
        var normalized = string.Join(' ', value.Split((char[]?)null, StringSplitOptions.RemoveEmptyEntries));
        return normalized.Length is 0 or > 200 ? null : normalized;
    }

    private string? GetCallbackUrl(HttpContext context)
    {
        var configured = publicUrls.PublicUrl(DynamicFederationAuthentication.CallbackPath);
        if (configured is not null) return configured;
        if (!environment.IsDevelopment()) return null;
        return $"{context.Request.Scheme}://{context.Request.Host}{context.Request.PathBase}{DynamicFederationAuthentication.CallbackPath}";
    }

    private string ProtectState(FederationOidcState state) =>
        dataProtectionProvider.CreateProtector(StatePurpose).Protect(JsonSerializer.Serialize(state));

    private FederationOidcState? UnprotectState(string state)
    {
        var json = dataProtectionProvider.CreateProtector(StatePurpose).Unprotect(state);
        return JsonSerializer.Deserialize<FederationOidcState>(json);
    }

    private static bool TryGetHttpsEndpoint(string? value, out string? endpoint) =>
        FederationEndpointPolicy.TryValidateHttpsEndpoint(value, out endpoint);

    private static bool TryValidateReturnPath(string? value, out string? path)
    {
        path = null;
        if (string.IsNullOrWhiteSpace(value)) value = "/";
        if (!value.StartsWith("/", StringComparison.Ordinal) || value.StartsWith("//", StringComparison.Ordinal) ||
            value.Contains('\\') || value.Contains('?') || value.Contains('#')) return false;
        if (value is not "/" and not "/sign-in") return false;
        var segments = value.Split('/');
        if (segments.Any(segment => segment is "." or "..")) return false;
        path = value;
        return value.Length <= 512;
    }

    private static string StableUserName(Guid connectionId, string issuer, string subject) =>
        $"federation-{Convert.ToHexString(SHA256.HashData(Encoding.UTF8.GetBytes($"{connectionId:N}\0{issuer}\0{subject}"))).ToLowerInvariant()}";

    private static string CreateOpaqueEmail(Guid connectionId, string issuer, string subject) =>
        $"federation-{Convert.ToHexString(SHA256.HashData(Encoding.UTF8.GetBytes($"{connectionId:N}\0{issuer}\0{subject}"))).ToLowerInvariant()}@sso.invalid";

    internal static string FederationLoginProvider(Guid connectionId, string issuer) =>
        $"{DynamicFederationAuthentication.Scheme}:{connectionId:N}:{Convert.ToHexString(SHA256.HashData(Encoding.UTF8.GetBytes(issuer))).ToLowerInvariant()}";

    private static string RandomToken(int bytes) => Base64Url(RandomNumberGenerator.GetBytes(bytes));

    private static string Base64Url(byte[] bytes) => Convert.ToBase64String(bytes).TrimEnd('=').Replace('+', '-').Replace('/', '_');

    private static bool FixedTimeEquals(string? left, string? right)
    {
        if (left is null || right is null) return false;
        var leftBytes = Encoding.UTF8.GetBytes(left);
        var rightBytes = Encoding.UTF8.GetBytes(right);
        return CryptographicOperations.FixedTimeEquals(leftBytes, rightBytes);
    }

    private static IResult Failure() => TypedResults.Redirect("/sign-in?error=federation_sign_in_failed");

    private async Task<bool> ConsumeStateAsync(Guid stateId, CancellationToken cancellationToken)
    {
        var now = DateTimeOffset.UtcNow;
        var stateHash = HashOpaque(stateId.ToString("N"));
        var affected = await dbContext.FederationOidcStates
            .Where(state => state.StateId == stateId && state.StateHash == stateHash &&
                state.ConsumedAt == null && state.ExpiresAt > now)
            .ExecuteUpdateAsync(setters => setters.SetProperty(state => state.ConsumedAt, now), cancellationToken);
        return affected == 1;
    }

    private async Task CleanupExpiredStatesAsync(CancellationToken cancellationToken)
    {
        await dbContext.FederationOidcStates
            .Where(state => state.ExpiresAt < DateTimeOffset.UtcNow.AddMinutes(-15))
            .OrderBy(state => state.ExpiresAt)
            .Take(100)
            .ExecuteDeleteAsync(cancellationToken);
    }

    private static string HashOpaque(string value) =>
        Convert.ToHexString(SHA256.HashData(Encoding.UTF8.GetBytes(value))).ToLowerInvariant();

    internal static bool IsExpectedJitConflict(Exception exception)
    {
        for (var current = exception; current is not null; current = current.InnerException)
        {
            if (current is Npgsql.PostgresException postgres &&
                postgres.SqlState is Npgsql.PostgresErrorCodes.SerializationFailure or
                    Npgsql.PostgresErrorCodes.DeadlockDetected or Npgsql.PostgresErrorCodes.UniqueViolation)
                return true;

            if (current is DbUpdateConcurrencyException || current is TimeoutException ||
                current is System.Transactions.TransactionAbortedException)
                return true;
        }

        return false;
    }

    private async Task<IResult> SignInLinkedIdentityAsync(
        HttpContext httpContext,
        FederationConnection connection,
        ExternalIdentity identity,
        FederatedIdentity linked,
        string returnPath,
        CancellationToken cancellationToken)
    {
        var existing = await userManager.FindByIdAsync(linked.UserId.ToString());
        if (existing is null || existing.IsDisabled ||
            (existing.LockoutEnd.HasValue && existing.LockoutEnd.Value > DateTimeOffset.UtcNow))
        {
            return Failure();
        }

        if (existing.TwoFactorEnabled)
        {
            await signInManager.SignOutAsync();
            await httpContext.SignInAsync(
                IdentityConstants.TwoFactorUserIdScheme,
                new ClaimsPrincipal(new ClaimsIdentity(
                    [new Claim(ClaimTypes.Name, existing.Id.ToString())],
                    IdentityConstants.TwoFactorUserIdScheme)));
            return TypedResults.Redirect("/sign-in?mfa=federation");
        }

        var result = await signInManager.ExternalLoginSignInAsync(
            FederationLoginProvider(connection.Id, identity.Issuer), identity.Subject,
            isPersistent: false, bypassTwoFactor: false);
        if (result.Succeeded)
        {
            return TypedResults.Redirect(returnPath);
        }

        if (result.RequiresTwoFactor)
        {
            return TypedResults.Redirect("/sign-in?mfa=federation");
        }

        // A winner may have been committed by a separate transaction while
        // this request's Identity manager still has a stale login lookup.
        // The active, already-correlated local account is safe to continue
        // through the ordinary local cookie path once MFA was ruled out.
        if (!existing.TwoFactorEnabled)
        {
            await signInManager.SignInAsync(existing, isPersistent: false);
            return TypedResults.Redirect(returnPath);
        }

        return Failure();
    }

    private async Task<IResult> RecoverWinningIdentityAsync(
        HttpContext httpContext,
        FederationConnection connection,
        ExternalIdentity identity,
        string returnPath,
        CancellationToken cancellationToken)
    {
        dbContext.ChangeTracker.Clear();
        await using var recoveryScope = scopeFactory.CreateAsyncScope();
        JitRecoveryObserver?.Invoke("scope");
        var recoveryDb = recoveryScope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        FederatedIdentity? raced = null;
        for (var attempt = 0; attempt < 20 && raced is null; attempt++)
        {
            raced = await recoveryDb.FederatedIdentities.AsNoTracking().SingleOrDefaultAsync(item =>
                item.ConnectionId == connection.Id && item.Issuer == identity.Issuer && item.Subject == identity.Subject,
                cancellationToken);
            if (raced is null) await Task.Delay(TimeSpan.FromMilliseconds(25), cancellationToken);
        }
        if (raced is null) return Failure();
        JitRecoveryObserver?.Invoke("winner-row");
        var recoveryUsers = recoveryScope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
        var recoverySignIn = recoveryScope.ServiceProvider.GetRequiredService<SignInManager<ApplicationUser>>();
        var winner = await recoveryUsers.FindByIdAsync(raced.UserId.ToString());
        if (winner is null || winner.IsDisabled ||
            (winner.LockoutEnd.HasValue && winner.LockoutEnd.Value > DateTimeOffset.UtcNow)) return Failure();
        JitRecoveryObserver?.Invoke("winner-user");
        if (winner.TwoFactorEnabled)
        {
            await recoverySignIn.SignOutAsync();
            await httpContext.SignInAsync(IdentityConstants.TwoFactorUserIdScheme,
                new ClaimsPrincipal(new ClaimsIdentity(
                    [new Claim(ClaimTypes.Name, winner.Id.ToString())], IdentityConstants.TwoFactorUserIdScheme)));
            return TypedResults.Redirect("/sign-in?mfa=federation");
        }
        await recoverySignIn.SignInAsync(winner, isPersistent: false);
        JitRecoveryObserver?.Invoke("signed-in");
        return TypedResults.Redirect(returnPath);
    }

    internal sealed record ExternalIdentity(string Issuer, string Subject, string? Email, bool EmailVerified, string DisplayName, string? ProviderHd,
        string? DirectoryTenantId, Guid? DirectoryObjectId);
    private sealed record FederationOidcState(Guid ConnectionId, int ConfigurationVersion, string ReturnPath, string Nonce, string CodeVerifier,
        DateTimeOffset IssuedAt, string CorrelationCookieName, string StateId);
}

public sealed record DynamicFederationChallenge(string AuthorizationUrl, string CorrelationValue, string CorrelationCookieName, CookieOptions CookieOptions);