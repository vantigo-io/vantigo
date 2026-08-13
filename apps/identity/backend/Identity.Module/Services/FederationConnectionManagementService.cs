using System.Security.Claims;

using Microsoft.AspNetCore.Http;
using Microsoft.EntityFrameworkCore;

using Vantigo.Identity.Authorization;
using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Identity.Services;

public sealed class FederationConnectionManagementService(
    AccountsDbContext dbContext,
    IFederationClientSecretResolver secretResolver,
    IFederationDiscoveryValidator discoveryValidator,
    AuthorizationAuditWriter auditWriter,
    IHttpContextAccessor httpContextAccessor)
{
    public Task<List<FederationConnection>> ListAsync(CancellationToken cancellationToken) =>
        dbContext.FederationConnections.AsNoTracking()
            .OrderBy(connection => connection.DisplayName)
            .ThenBy(connection => connection.Id)
            .ToListAsync(cancellationToken);

    public Task<FederationConnection?> GetAsync(Guid id, CancellationToken cancellationToken) =>
        dbContext.FederationConnections.SingleOrDefaultAsync(connection => connection.Id == id, cancellationToken);

    public async Task<FederationConnection> CreateAsync(
        FederationProviderKind providerKind,
        string displayName,
        string authority,
        string clientId,
        string? clientSecretReference,
        IReadOnlyCollection<string> allowedDomains,
        bool isDefault,
        JitCreationMode jitCreationMode,
        CancellationToken cancellationToken)
    {
        ValidateProviderRules(providerKind, allowedDomains);
        if (isDefault)
            throw new FederationConnectionValidationException("invalid_default", "A default federation connection must pass validation and be enabled first.");

        await using var transaction = await dbContext.Database.BeginTransactionAsync(cancellationToken);
        var now = DateTimeOffset.UtcNow;
        var connection = new FederationConnection
        {
            ProviderKind = providerKind,
            DisplayName = displayName,
            Authority = authority,
            ClientId = clientId,
            ClientSecretReference = clientSecretReference,
            AllowedDomains = allowedDomains.ToArray(),
            IsEnabled = false,
            IsDefault = false,
            JitCreationMode = jitCreationMode,
            ConfigurationVersion = 1,
            ValidationState = FederationConnectionValidationState.Draft,
            ConcurrencyStamp = NewConcurrencyStamp(),
            CreatedAt = now,
            UpdatedAt = now,
        };

        dbContext.FederationConnections.Add(connection);
        await dbContext.SaveChangesAsync(cancellationToken);
        await AuditAsync("federation_connection.created", null, connection);
        await transaction.CommitAsync(cancellationToken);
        return connection;
    }

    public async Task<FederationConnection?> UpdateAsync(
        Guid id,
        FederationProviderKind providerKind,
        string displayName,
        string authority,
        string clientId,
        string? clientSecretReference,
        bool replaceClientSecretReference,
        bool clearClientSecretReference,
        IReadOnlyCollection<string> allowedDomains,
        bool isDefault,
        JitCreationMode jitCreationMode,
        string concurrencyStamp,
        CancellationToken cancellationToken)
    {
        ValidateProviderRules(providerKind, allowedDomains);
        if (isDefault)
            throw new FederationConnectionValidationException("invalid_default", "A default federation connection must pass validation and be enabled first.");

        await using var transaction = await dbContext.Database.BeginTransactionAsync(cancellationToken);
        var connection = await dbContext.FederationConnections.SingleOrDefaultAsync(
            item => item.Id == id, cancellationToken);
        if (connection is null) return null;
        if (!string.Equals(connection.ConcurrencyStamp, concurrencyStamp, StringComparison.Ordinal))
            throw new DbUpdateConcurrencyException();
        var before = Snapshot(connection);

        connection.ProviderKind = providerKind;
        connection.DisplayName = displayName;
        connection.Authority = authority;
        connection.ClientId = clientId;
        connection.AllowedDomains = allowedDomains.ToArray();
        connection.IsEnabled = false;
        connection.IsDefault = false;
        connection.JitCreationMode = jitCreationMode;
        if (clearClientSecretReference)
            connection.ClientSecretReference = null;
        else if (replaceClientSecretReference)
            connection.ClientSecretReference = clientSecretReference;

        connection.ConfigurationVersion++;
        InvalidateValidation(connection);
        connection.UpdatedAt = DateTimeOffset.UtcNow;
        connection.ConcurrencyStamp = NewConcurrencyStamp();
        await dbContext.SaveChangesAsync(cancellationToken);
        await AuditAsync("federation_connection.updated", before, connection);
        await transaction.CommitAsync(cancellationToken);
        return connection;
    }

    public async Task<FederationConnection?> SetEnabledAsync(
        Guid id,
        bool enabled,
        bool makeDefault,
        string concurrencyStamp,
        CancellationToken cancellationToken)
    {
        await using var transaction = await dbContext.Database.BeginTransactionAsync(cancellationToken);
        var connection = await dbContext.FederationConnections.SingleOrDefaultAsync(
            item => item.Id == id, cancellationToken);
        if (connection is null) return null;
        if (!string.Equals(connection.ConcurrencyStamp, concurrencyStamp, StringComparison.Ordinal))
            throw new DbUpdateConcurrencyException();
        var before = Snapshot(connection);

        if (enabled)
        {
            if (connection.ValidationState != FederationConnectionValidationState.Succeeded ||
                connection.ValidatedConfigurationVersion != connection.ConfigurationVersion)
                throw new FederationConnectionValidationException("validation_required", "The current federation configuration must pass validation before it can be enabled.");
            if (string.IsNullOrWhiteSpace(connection.ClientSecretReference))
                throw new FederationConnectionValidationException("client_secret_reference_missing", "A client secret reference is required before enabling the connection.");
            var resolution = secretResolver.Resolve(connection.ClientSecretReference);
            if (resolution.Status != FederationClientSecretResolutionStatus.Configured)
                throw new FederationConnectionValidationException("client_secret_reference_unconfigured", "The configured client secret reference is not available to the application.");

            if (makeDefault)
            {
                var existingDefaults = await dbContext.FederationConnections
                    .Where(item => item.Id != id && item.IsDefault)
                    .ToListAsync(cancellationToken);
                foreach (var existing in existingDefaults)
                {
                    var displaced = Snapshot(existing);
                    existing.IsDefault = false;
                    existing.UpdatedAt = DateTimeOffset.UtcNow;
                    existing.ConcurrencyStamp = NewConcurrencyStamp();
                    await AuditAsync("federation_connection.default_displaced", displaced, existing);
                }
            }

            connection.IsEnabled = true;
            connection.IsDefault = makeDefault;
        }
        else
        {
            connection.IsEnabled = false;
            connection.IsDefault = false;
        }

        connection.UpdatedAt = DateTimeOffset.UtcNow;
        connection.ConcurrencyStamp = NewConcurrencyStamp();
        await dbContext.SaveChangesAsync(cancellationToken);
        await AuditAsync(enabled ? "federation_connection.enabled" : "federation_connection.disabled", before, connection);
        await transaction.CommitAsync(cancellationToken);
        return connection;
    }

    public async Task<FederationConnectionValidationOutcome?> ValidateAsync(
        Guid id,
        string concurrencyStamp,
        CancellationToken cancellationToken)
    {
        var connection = await dbContext.FederationConnections.SingleOrDefaultAsync(
            item => item.Id == id, cancellationToken);
        if (connection is null) return null;
        if (!string.Equals(connection.ConcurrencyStamp, concurrencyStamp, StringComparison.Ordinal))
            throw new DbUpdateConcurrencyException();

        var result = await discoveryValidator.ValidateAsync(
            connection.ProviderKind, connection.Authority, cancellationToken);

        await using var transaction = await dbContext.Database.BeginTransactionAsync(cancellationToken);
        var now = DateTimeOffset.UtcNow;
        connection.IsEnabled = false;
        connection.IsDefault = false;
        connection.ValidationState = result.Succeeded
            ? FederationConnectionValidationState.Succeeded
            : FederationConnectionValidationState.Failed;
        connection.ValidationErrorCode = result.Succeeded ? null : result.Code;
        connection.ValidationCompletedAt = now;
        connection.ValidatedConfigurationVersion = connection.ConfigurationVersion;
        connection.ValidatedIssuer = result.Succeeded ? result.Issuer : null;
        connection.ValidatedDiscoveryEndpoint = result.Succeeded ? result.DiscoveryEndpoint : null;
        connection.ValidatedAuthorizationEndpoint = result.Succeeded ? result.AuthorizationEndpoint : null;
        connection.ValidatedTokenEndpoint = result.Succeeded ? result.TokenEndpoint : null;
        connection.ValidatedJwksUri = result.Succeeded ? result.JwksUri : null;
        connection.UpdatedAt = now;
        connection.ConcurrencyStamp = NewConcurrencyStamp();
        await dbContext.SaveChangesAsync(cancellationToken);
        await AuditAsync(result.Succeeded ? "federation_connection.validated" : "federation_connection.validation_failed", null, connection);
        await transaction.CommitAsync(cancellationToken);
        return new FederationConnectionValidationOutcome(connection, result);
    }

    public async Task<bool> DeleteAsync(Guid id, string concurrencyStamp, CancellationToken cancellationToken)
    {
        await using var transaction = await dbContext.Database.BeginTransactionAsync(cancellationToken);
        var connection = await dbContext.FederationConnections.SingleOrDefaultAsync(
            item => item.Id == id, cancellationToken);
        if (connection is null) return false;
        if (!string.Equals(connection.ConcurrencyStamp, concurrencyStamp, StringComparison.Ordinal))
            throw new DbUpdateConcurrencyException();
        var before = Snapshot(connection);

        // Federation and SCIM rows are provenance, not disposable children.
        // Refuse the destructive operation before EF can surface a foreign-key
        // violation as an opaque database 500.
        if (await dbContext.ScimConnections.AnyAsync(item => item.FederationConnectionId == id, cancellationToken) ||
            await dbContext.ScimUserMappings.AnyAsync(item =>
                dbContext.ScimConnections.Any(scim => scim.Id == item.ScimConnectionId &&
                    scim.FederationConnectionId == id), cancellationToken) ||
            await dbContext.ScimBearerTokens.AnyAsync(item =>
                dbContext.ScimConnections.Any(scim => scim.Id == item.ScimConnectionId &&
                    scim.FederationConnectionId == id), cancellationToken) ||
            await dbContext.AccessGroups.AnyAsync(item => item.ScimConnectionId.HasValue &&
                dbContext.ScimConnections.Any(scim => scim.Id == item.ScimConnectionId.Value &&
                    scim.FederationConnectionId == id), cancellationToken) ||
            await dbContext.FederatedIdentities.AnyAsync(item => item.ConnectionId == id, cancellationToken))
        {
            throw new FederationConnectionDeletionConflictException();
        }

        dbContext.FederationConnections.Remove(connection);
        await dbContext.SaveChangesAsync(cancellationToken);
        await AuditAsync("federation_connection.deleted", before, null);
        await transaction.CommitAsync(cancellationToken);
        return true;
    }

    private async Task AuditAsync(string action, object? before, FederationConnection? after)
    {
        var context = httpContextAccessor.HttpContext ?? throw new InvalidOperationException("An HTTP context is required for an identity audit event.");
        var actor = Guid.TryParse(context.User.FindFirstValue(ClaimTypes.NameIdentifier), out var actorId)
            ? actorId
            : (Guid?)null;
        await auditWriter.WriteAsync(dbContext, context, actor, null, null, action,
            before ?? new { }, after is null ? new { } : Snapshot(after));
    }

    private static object Snapshot(FederationConnection connection) => new
    {
        connection.Id,
        ProviderType = connection.ProviderKind.ToString(),
        connection.DisplayName,
        connection.Authority,
        connection.ClientId,
        connection.ClientSecretReference,
        connection.AllowedDomains,
        connection.IsEnabled,
        connection.IsDefault,
        JitCreationMode = connection.JitCreationMode.ToString(),
        connection.ConfigurationVersion,
        ValidationState = connection.ValidationState.ToString(),
        connection.ValidationErrorCode,
        connection.ValidationCompletedAt,
        connection.ValidatedConfigurationVersion,
        connection.ValidatedIssuer,
        connection.ValidatedDiscoveryEndpoint,
        connection.ValidatedAuthorizationEndpoint,
        connection.ValidatedTokenEndpoint,
        connection.ValidatedJwksUri,
        connection.ConcurrencyStamp,
    };

    private static void InvalidateValidation(FederationConnection connection)
    {
        connection.ValidationState = FederationConnectionValidationState.Draft;
        connection.ValidationErrorCode = null;
        connection.ValidationCompletedAt = null;
        connection.ValidatedConfigurationVersion = null;
        connection.ValidatedIssuer = null;
        connection.ValidatedDiscoveryEndpoint = null;
        connection.ValidatedAuthorizationEndpoint = null;
        connection.ValidatedTokenEndpoint = null;
        connection.ValidatedJwksUri = null;
    }

    private static string NewConcurrencyStamp() => Guid.NewGuid().ToString("N");

    private static void ValidateProviderRules(
        FederationProviderKind providerKind,
        IReadOnlyCollection<string> allowedDomains)
    {
        if (providerKind == FederationProviderKind.Google && allowedDomains.Count == 0)
            throw new FederationConnectionValidationException(
                "invalid_domain", "Google connections require at least one allowed domain.");
    }
}

public sealed class FederationConnectionValidationException(string code, string message) : Exception(message)
{
    public string Code { get; } = code;
}

public sealed class FederationConnectionDeletionConflictException() : Exception(
    "The federation connection has retained SCIM or federated identity provenance and cannot be deleted.");

public sealed record FederationConnectionValidationOutcome(
    FederationConnection Connection,
    FederationDiscoveryValidationResult Result);