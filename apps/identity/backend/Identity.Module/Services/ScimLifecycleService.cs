using Microsoft.AspNetCore.Http;
using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;

using Vantigo.Identity.Authorization;
using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Identity.Services;

public sealed class ScimLifecycleService(
    AccountsDbContext dbContext,
    UserManager<ApplicationUser> userManager,
    AuthorizationAuditWriter auditWriter,
    IHttpContextAccessor httpContextAccessor)
{
    internal static Func<Exception?>? MutationFailureInjector { get; set; }

    public async Task<bool> IsEffectivelyDisabledAsync(Guid userId, CancellationToken cancellationToken)
    {
        var user = await dbContext.Users.AsNoTracking().SingleOrDefaultAsync(item => item.Id == userId, cancellationToken);
        if (user is null || user.IsDisabled) return true;
        var ownerRoleId = await dbContext.Roles.AsNoTracking()
            .Where(role => role.Name == AuthRoles.Owner)
            .Select(role => (Guid?)role.Id)
            .SingleOrDefaultAsync(cancellationToken);
        if (ownerRoleId.HasValue && await dbContext.UserRoles.AsNoTracking()
                .AnyAsync(assignment => assignment.UserId == userId && assignment.RoleId == ownerRoleId.Value, cancellationToken))
        {
            return false;
        }
        var mappings = await dbContext.ScimUserMappings.AsNoTracking()
            .Where(item => item.UserId == userId)
            .Join(dbContext.ScimConnections.AsNoTracking(), mapping => mapping.ScimConnectionId, connection => connection.Id,
                (mapping, connection) => new { mapping, connection })
            .ToListAsync(cancellationToken);
        return mappings.Any(item => item.mapping.LifecycleOverride == ScimLifecycleOverride.ForceDisable ||
            item.mapping.LifecycleOverride != ScimLifecycleOverride.ForceEnable && item.connection.IsEnabled &&
            item.connection.Mode == ScimProvisioningMode.Authoritative && !item.mapping.UpstreamActive);
    }

    public async Task<bool> IsScimControllableUserAsync(Guid userId, CancellationToken cancellationToken)
    {
        var ownerRoleId = await dbContext.Roles.AsNoTracking()
            .Where(role => role.Name == AuthRoles.Owner)
            .Select(role => (Guid?)role.Id)
            .SingleOrDefaultAsync(cancellationToken);
        return !ownerRoleId.HasValue || !await dbContext.UserRoles.AsNoTracking()
            .AnyAsync(assignment => assignment.UserId == userId && assignment.RoleId == ownerRoleId.Value, cancellationToken);
    }

    public async Task<ScimEntraMappingResolution> ResolveEntraMappingForFederationAsync(
        Guid federationConnectionId,
        string issuer,
        Guid tenantId,
        Guid objectId,
        CancellationToken cancellationToken)
    {
        var federation = await dbContext.FederationConnections.AsNoTracking()
            .SingleOrDefaultAsync(item => item.Id == federationConnectionId &&
                item.ProviderKind == FederationProviderKind.Entra &&
                item.ValidatedIssuer == issuer, cancellationToken);
        if (federation is null ||
            !DynamicFederationOidcService.TryGetEntraTenantFromIssuer(federation.ValidatedIssuer, out var issuerTenant) ||
            issuerTenant != tenantId)
        {
            return ScimEntraMappingResolution.Missing;
        }

        var candidates = await dbContext.ScimUserMappings
            .AsNoTracking()
            .Where(mapping => mapping.ExternalId == objectId.ToString("D") &&
                dbContext.ScimConnections.Any(connection =>
                    connection.Id == mapping.ScimConnectionId &&
                    connection.FederationConnectionId == federationConnectionId))
            .ToListAsync(cancellationToken);
        if (candidates.Count == 0) return ScimEntraMappingResolution.Missing;
        if (candidates.Count != 1) return ScimEntraMappingResolution.Ambiguous;

        var mapping = candidates[0];
        if (!await IsScimControllableUserAsync(mapping.UserId, cancellationToken))
            return ScimEntraMappingResolution.OwnerRejected;

        var scimConnection = await dbContext.ScimConnections.AsNoTracking()
            .SingleOrDefaultAsync(item => item.Id == mapping.ScimConnectionId &&
                item.FederationConnectionId == federationConnectionId, cancellationToken);
        if (scimConnection is null || !scimConnection.IsEnabled ||
            await IsEffectivelyDisabledAsync(mapping.UserId, cancellationToken))
        {
            return ScimEntraMappingResolution.Disabled;
        }

        return new(ScimEntraMappingResolutionKind.Active, mapping);
    }

    public async Task<ScimUserMapping?> FindEntraMappingAsync(
        Guid scimConnectionId, Guid tenantId, Guid objectId, CancellationToken cancellationToken)
    {
        var binding = await dbContext.ScimConnections.AsNoTracking()
            .Join(dbContext.FederationConnections.AsNoTracking(),
                scim => scim.FederationConnectionId,
                federation => federation.Id,
                (scim, federation) => new { scim, federation })
            .SingleOrDefaultAsync(item => item.scim.Id == scimConnectionId, cancellationToken);
        if (binding is null || binding.federation.ValidatedIssuer is null) return null;

        var result = await ResolveEntraMappingForFederationAsync(
            binding.federation.Id,
            binding.federation.ValidatedIssuer,
            tenantId,
            objectId,
            cancellationToken);
        return result.IsActive ? result.Mapping : null;
    }

    /// <summary>
    /// Correlates an already validated Entra OIDC identity to SCIM without
    /// using email. The federation binding, tenant, object id, and local user
    /// are all part of the same serializable transaction.
    /// </summary>
    public async Task<ScimUserMapping?> EnsureMappingForOidcFederatedIdentityAsync(
        Guid scimConnectionId,
        string externalId,
        CancellationToken cancellationToken)
    {
        if (!Guid.TryParse(externalId, out var objectId)) return null;
        externalId = objectId.ToString("D");

        var ownsTransaction = dbContext.Database.CurrentTransaction is null;
        await using var transaction = ownsTransaction
            ? await dbContext.Database.BeginTransactionAsync(System.Data.IsolationLevel.Serializable, cancellationToken)
            : null;
        try
        {
            var binding = await dbContext.ScimConnections
                .Join(dbContext.FederationConnections,
                    scim => scim.FederationConnectionId,
                    federation => federation.Id,
                    (scim, federation) => new { scim, federation })
                .SingleOrDefaultAsync(item => item.scim.Id == scimConnectionId, cancellationToken);
            if (binding is null || !binding.scim.IsEnabled ||
                binding.federation.ProviderKind != FederationProviderKind.Entra ||
                !binding.federation.IsEnabled || binding.federation.ValidatedIssuer is null ||
                !DynamicFederationOidcService.TryGetEntraTenantFromIssuer(binding.federation.ValidatedIssuer, out var tenantId))
            {
                return null;
            }

            var identities = await dbContext.FederatedIdentities
                .Where(identity => identity.ConnectionId == binding.federation.Id &&
                    identity.Issuer == binding.federation.ValidatedIssuer &&
                    identity.DirectoryObjectId == objectId)
                .ToListAsync(cancellationToken);
            identities = identities.Where(identity => IsTenant(identity.DirectoryTenantId, tenantId)).ToList();
            if (identities.Count != 1) return null;

            var identity = identities[0];
            if (!await IsScimControllableUserAsync(identity.UserId, cancellationToken) ||
                await IsEffectivelyDisabledAsync(identity.UserId, cancellationToken))
            {
                return null;
            }

            var externalMatches = await dbContext.ScimUserMappings
                .Where(mapping => mapping.ScimConnectionId == scimConnectionId &&
                    mapping.ExternalId == externalId)
                .ToListAsync(cancellationToken);
            if (externalMatches.Count > 1) return null;
            if (externalMatches.Count == 1)
            {
                return externalMatches[0].UserId == identity.UserId ? externalMatches[0] : null;
            }

            // A SCIM connection can have at most one mapping for a local user.
            // A different external id is therefore a real correlation conflict,
            // not an invitation to overwrite the existing provenance.
            if (await dbContext.ScimUserMappings.AnyAsync(mapping =>
                    mapping.ScimConnectionId == scimConnectionId &&
                    mapping.UserId == identity.UserId, cancellationToken))
            {
                return null;
            }

            var user = await dbContext.Users.AsNoTracking()
                .SingleOrDefaultAsync(item => item.Id == identity.UserId, cancellationToken);
            if (user is null) return null;

            var now = DateTimeOffset.UtcNow;
            var mapping = new ScimUserMapping
            {
                ScimConnectionId = scimConnectionId,
                UserId = identity.UserId,
                ResourceId = externalId,
                ExternalId = externalId,
                UserName = user.UserName ?? user.Email ?? identity.UserId.ToString("D"),
                UpstreamActive = true,
                ETag = Guid.NewGuid().ToString("N"),
                CreatedAt = now,
                UpdatedAt = now,
                LastSynchronizedAt = now,
            };
            dbContext.ScimUserMappings.Add(mapping);
            await dbContext.SaveChangesAsync(cancellationToken);
            await auditWriter.WriteAsync(
                dbContext,
                httpContextAccessor.HttpContext ?? new DefaultHttpContext(),
                null,
                mapping.UserId,
                null,
                "scim.mapping.oidc-created",
                new
                {
                    ConnectionId = mapping.ScimConnectionId,
                    UserId = mapping.UserId,
                    TenantId = tenantId,
                    ObjectId = objectId,
                    Source = "oidc-first",
                    MappingId = (Guid?)null,
                },
                new
                {
                    ConnectionId = mapping.ScimConnectionId,
                    UserId = mapping.UserId,
                    TenantId = tenantId,
                    ObjectId = objectId,
                    Source = "oidc-first",
                    MappingId = mapping.Id,
                },
                cancellationToken);
            if (MutationFailureInjector?.Invoke() is { } mappingFailure)
                throw mappingFailure;
            if (ownsTransaction) await transaction!.CommitAsync(cancellationToken);
            return mapping;
        }
        catch (Exception exception) when (ownsTransaction && exception is (DbUpdateConcurrencyException or DbUpdateException))
        {
            return null;
        }
    }

    public async Task<FederatedIdentity?> AttachEntraIdentityToMappingAsync(
        ScimUserMapping expectedMapping,
        Guid federationConnectionId,
        string issuer,
        string subject,
        Guid tenantId,
        Guid objectId,
        CancellationToken cancellationToken)
    {
        await using var transaction = await dbContext.Database.BeginTransactionAsync(
            System.Data.IsolationLevel.Serializable, cancellationToken);
        try
        {
            var binding = await dbContext.ScimConnections
                .Join(dbContext.FederationConnections,
                    scim => scim.FederationConnectionId,
                    federation => federation.Id,
                    (scim, federation) => new { scim, federation })
                .SingleOrDefaultAsync(item => item.scim.FederationConnectionId == federationConnectionId &&
                    item.federation.Id == federationConnectionId &&
                    item.federation.ValidatedIssuer == issuer &&
                    item.federation.ProviderKind == FederationProviderKind.Entra,
                    cancellationToken);
            if (binding is null || !binding.scim.IsEnabled || !binding.federation.IsEnabled ||
                !DynamicFederationOidcService.TryGetEntraTenantFromIssuer(binding.federation.ValidatedIssuer, out var issuerTenant) ||
                issuerTenant != tenantId)
            {
                return null;
            }

            var mapping = await dbContext.ScimUserMappings.SingleOrDefaultAsync(item =>
                item.Id == expectedMapping.Id && item.ScimConnectionId == binding.scim.Id &&
                item.ExternalId == objectId.ToString("D"), cancellationToken);
            if (mapping is null || !await IsScimControllableUserAsync(mapping.UserId, cancellationToken) ||
                await IsEffectivelyDisabledAsync(mapping.UserId, cancellationToken))
            {
                return null;
            }

            var existing = await dbContext.FederatedIdentities.SingleOrDefaultAsync(item =>
                item.ConnectionId == federationConnectionId && item.Issuer == issuer && item.Subject == subject,
                cancellationToken);
            if (existing is not null)
            {
                return existing.UserId == mapping.UserId ? existing : null;
            }

            var directoryMatches = await dbContext.FederatedIdentities
                .Where(item => item.ConnectionId == federationConnectionId &&
                    item.Issuer == issuer && item.DirectoryObjectId == objectId)
                .ToListAsync(cancellationToken);
            directoryMatches = directoryMatches.Where(item => IsTenant(item.DirectoryTenantId, tenantId)).ToList();
            if (directoryMatches.Count != 0)
            {
                return directoryMatches.Count == 1 && directoryMatches[0].UserId == mapping.UserId
                    ? directoryMatches[0]
                    : null;
            }

            var user = await userManager.FindByIdAsync(mapping.UserId.ToString());
            if (user is null) return null;

            var provider = DynamicFederationOidcService.FederationLoginProvider(federationConnectionId, issuer);
            var login = await userManager.AddLoginAsync(user, new UserLoginInfo(provider, subject, binding.federation.DisplayName));
            if (!login.Succeeded) return null;

            var attached = new FederatedIdentity
            {
                ConnectionId = federationConnectionId,
                Issuer = issuer,
                Subject = subject,
                DirectoryTenantId = tenantId.ToString("D"),
                DirectoryObjectId = objectId,
                UserId = mapping.UserId,
                CreatedAt = DateTimeOffset.UtcNow,
                UpdatedAt = DateTimeOffset.UtcNow,
            };
            dbContext.FederatedIdentities.Add(attached);
            await dbContext.SaveChangesAsync(cancellationToken);
            await auditWriter.WriteAsync(
                dbContext,
                httpContextAccessor.HttpContext ?? new DefaultHttpContext(),
                null,
                mapping.UserId,
                null,
                "scim.mapping.oidc-attached",
                new
                {
                    ConnectionId = federationConnectionId,
                    UserId = mapping.UserId,
                    TenantId = tenantId,
                    ObjectId = objectId,
                    Source = "oidc-callback",
                    FederatedIdentityId = (Guid?)null,
                },
                new
                {
                    ConnectionId = federationConnectionId,
                    UserId = mapping.UserId,
                    TenantId = tenantId,
                    ObjectId = objectId,
                    Source = "oidc-callback",
                    FederatedIdentityId = attached.Id,
                },
                cancellationToken);
            if (MutationFailureInjector?.Invoke() is { } attachmentFailure)
                throw attachmentFailure;
            await transaction.CommitAsync(cancellationToken);
            return attached;
        }
        catch (Exception exception) when (exception is DbUpdateConcurrencyException or DbUpdateException)
        {
            return null;
        }
    }

    private static bool IsTenant(string? value, Guid expected) =>
        Guid.TryParse(value, out var actual) && actual == expected;
}

public enum ScimEntraMappingResolutionKind
{
    Missing,
    Active,
    Disabled,
    OwnerRejected,
    Ambiguous,
}

public sealed record ScimEntraMappingResolution(
    ScimEntraMappingResolutionKind Kind,
    ScimUserMapping? Mapping = null)
{
    public bool IsActive => Kind == ScimEntraMappingResolutionKind.Active && Mapping is not null;
    public static ScimEntraMappingResolution Missing { get; } = new(ScimEntraMappingResolutionKind.Missing);
    public static ScimEntraMappingResolution Disabled { get; } = new(ScimEntraMappingResolutionKind.Disabled);
    public static ScimEntraMappingResolution OwnerRejected { get; } = new(ScimEntraMappingResolutionKind.OwnerRejected);
    public static ScimEntraMappingResolution Ambiguous { get; } = new(ScimEntraMappingResolutionKind.Ambiguous);
}