using System.Globalization;
using System.Net;
using System.Text.RegularExpressions;

using Microsoft.AspNetCore.Mvc;
using Microsoft.EntityFrameworkCore;

using Vantigo.Identity.Database.Accounts;
using Vantigo.Identity.Services;

namespace Vantigo.Identity.Endpoints.Auth;

internal static partial class IdentityControlPlaneEndpoints
{
    internal static void MapIdentityControlPlaneEndpoints(IEndpointRouteBuilder app)
    {
        var access = app.MapGroup("/api/v1/identity/access")
            .WithTags("Identity control plane")
            .RequireAuthorization(Vantigo.Contracts.Identity.AuthPolicies.OwnerManagement);
        access.AddEndpointFilter(async (context, next) =>
        {
            try { return await next(context); }
            catch (Exception exception) when (exception is DbUpdateConcurrencyException ||
                Vantigo.Identity.Authorization.AuthorizationConflict.IsExpected(exception))
            { return Error(StatusCodes.Status409Conflict, "concurrency_conflict", "The resource changed concurrently."); }
            catch (FederationConnectionDeletionConflictException exception)
            { return Error(StatusCodes.Status409Conflict, "provenance_conflict", exception.Message); }
            catch (FederationConnectionValidationException exception)
            { return Error(StatusCodes.Status400BadRequest, exception.Code, exception.Message); }
        });

        var federation = access.MapGroup("/federation-connections");
        federation.MapGet("", ListFederationConnections);
        federation.MapGet("/{id:guid}", GetFederationConnection);
        federation.MapPost("", CreateFederationConnection);
        federation.MapPut("/{id:guid}", UpdateFederationConnection);
        federation.MapPost("/{id:guid}/validate", ValidateFederationConnection);
        federation.MapPost("/{id:guid}/enable", EnableFederationConnection);
        federation.MapPost("/{id:guid}/disable", DisableFederationConnection);
        federation.MapDelete("/{id:guid}", DeleteFederationConnection);

        var groups = access.MapGroup("/groups");
        groups.MapGet("", ListGroups);
        groups.MapGet("/{id:guid}", GetGroup);
        groups.MapPost("", CreateGroup);
        groups.MapPut("/{id:guid}", UpdateGroup);
        groups.MapDelete("/{id:guid}", DeleteGroup);
        groups.MapPost("/{groupId:guid}/members/{userId:guid}", AddMember);
        groups.MapPut("/{groupId:guid}/members/{userId:guid}", AddMember);
        groups.MapDelete("/{groupId:guid}/members/{userId:guid}", RemoveMember);
        groups.MapPost("/{groupId:guid}/role-mappings/{roleId:guid}", AddRoleMapping);
        groups.MapPut("/{groupId:guid}/role-mappings/{roleId:guid}", AddRoleMapping);
        groups.MapDelete("/{groupId:guid}/role-mappings/{roleId:guid}", RemoveRoleMapping);
    }

    private static async Task<IResult> ListFederationConnections(
        FederationConnectionManagementService service,
        CancellationToken cancellationToken) =>
        TypedResults.Ok((await service.ListAsync(cancellationToken)).Select(ToResponse).ToArray());

    private static async Task<IResult> GetFederationConnection(
        Guid id,
        FederationConnectionManagementService service,
        CancellationToken cancellationToken)
    {
        var connection = await service.GetAsync(id, cancellationToken);
        return connection is null ? TypedResults.NotFound() : TypedResults.Ok(ToResponse(connection));
    }

    private static async Task<IResult> CreateFederationConnection(
        [FromBody] FederationConnectionRequest? request,
        FederationConnectionManagementService service,
        AccountsDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var validation = ValidateFederationRequest(request, isUpdate: false);
        if (validation is not null) return validation;
        var values = ReadFederationRequest(request!);
        if (await dbContext.FederationConnections.AnyAsync(
                item => item.DisplayName == values.DisplayName, cancellationToken))
            return Error(StatusCodes.Status409Conflict, "connection_exists", "A federation connection with this display name already exists.");

        try
        {
            var connection = await service.CreateAsync(
                values.ProviderKind, values.DisplayName, values.Authority, values.ClientId,
                values.ClientSecretReference, values.AllowedDomains, values.IsDefault, values.JitCreationMode,
                cancellationToken);
            return TypedResults.Created($"/api/v1/identity/access/federation-connections/{connection.Id}", ToResponse(connection));
        }
        catch (DbUpdateException)
        {
            return Error(StatusCodes.Status409Conflict, "connection_conflict", "The federation connection conflicts with another change.");
        }
    }

    private static async Task<IResult> UpdateFederationConnection(
        Guid id,
        [FromBody] FederationConnectionRequest? request,
        FederationConnectionManagementService service,
        AccountsDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var validation = ValidateFederationRequest(request, isUpdate: true);
        if (validation is not null) return validation;
        var current = await service.GetAsync(id, cancellationToken);
        if (current is null) return TypedResults.NotFound();
        if (string.IsNullOrWhiteSpace(request!.ConcurrencyStamp))
            return Error(StatusCodes.Status400BadRequest, "concurrency_required", "A concurrency stamp is required.");
        if (!string.Equals(request.ConcurrencyStamp, current.ConcurrencyStamp, StringComparison.Ordinal))
            return Error(StatusCodes.Status409Conflict, "connection_conflict", "The federation connection changed concurrently.");

        var values = ReadFederationRequest(request);
        if (await dbContext.FederationConnections.AnyAsync(
                item => item.Id != id && item.DisplayName == values.DisplayName, cancellationToken))
            return Error(StatusCodes.Status409Conflict, "connection_exists", "A federation connection with this display name already exists.");
        try
        {
            var connection = await service.UpdateAsync(
                id, values.ProviderKind, values.DisplayName, values.Authority, values.ClientId,
                values.ClientSecretReference, values.ClientSecretReference is not null, values.ClearClientSecretReference,
                values.AllowedDomains, values.IsDefault, values.JitCreationMode,
                request.ConcurrencyStamp, cancellationToken);
            return connection is null ? TypedResults.NotFound() : TypedResults.Ok(ToResponse(connection));
        }
        catch (DbUpdateException)
        {
            return Error(StatusCodes.Status409Conflict, "connection_conflict", "The federation connection conflicts with another change.");
        }
    }

    private static Task<IResult> EnableFederationConnection(
        Guid id,
        [FromBody] MutationRequest? request,
        FederationConnectionManagementService service,
        CancellationToken cancellationToken) => SetFederationConnectionEnabled(id, true, request?.ConcurrencyStamp, request?.IsDefault ?? false, service, cancellationToken);

    private static Task<IResult> DisableFederationConnection(
        Guid id,
        [FromBody] MutationRequest? request,
        FederationConnectionManagementService service,
        CancellationToken cancellationToken) => SetFederationConnectionEnabled(id, false, request?.ConcurrencyStamp, false, service, cancellationToken);

    private static async Task<IResult> ValidateFederationConnection(
        Guid id,
        [FromBody] MutationRequest? request,
        FederationConnectionManagementService service,
        CancellationToken cancellationToken)
    {
        if (string.IsNullOrWhiteSpace(request?.ConcurrencyStamp))
            return Error(StatusCodes.Status400BadRequest, "concurrency_required", "A concurrency stamp is required.");
        var outcome = await service.ValidateAsync(id, request.ConcurrencyStamp, cancellationToken);
        if (outcome is null) return TypedResults.NotFound();
        return TypedResults.Ok(new
        {
            connection = ToResponse(outcome.Connection),
            succeeded = outcome.Result.Succeeded,
            code = outcome.Result.Code,
            message = outcome.Result.Message,
        });
    }

    private static async Task<IResult> SetFederationConnectionEnabled(
        Guid id,
        bool enabled,
        string? concurrencyStamp,
        bool makeDefault,
        FederationConnectionManagementService service,
        CancellationToken cancellationToken)
    {
        if (string.IsNullOrWhiteSpace(concurrencyStamp))
            return Error(StatusCodes.Status400BadRequest, "concurrency_required", "A concurrency stamp is required.");
        var connection = await service.SetEnabledAsync(id, enabled, makeDefault, concurrencyStamp, cancellationToken);
        return connection is null ? TypedResults.NotFound() : TypedResults.Ok(ToResponse(connection));
    }

    private static async Task<IResult> DeleteFederationConnection(
        Guid id,
        [FromBody] MutationRequest? request,
        FederationConnectionManagementService service,
        CancellationToken cancellationToken)
    {
        if (string.IsNullOrWhiteSpace(request?.ConcurrencyStamp))
            return Error(StatusCodes.Status400BadRequest, "concurrency_required", "A concurrency stamp is required.");
        return await service.DeleteAsync(id, request.ConcurrencyStamp, cancellationToken)
            ? TypedResults.NoContent() : TypedResults.NotFound();
    }

    private static async Task<IResult> ListGroups(
        AccessGroupManagementService service,
        CancellationToken cancellationToken)
    {
        var groups = await service.ListAsync(cancellationToken);
        var details = new List<AccessGroupResponse>(groups.Count);
        foreach (var group in groups)
            details.Add(await ToResponse(service, group, cancellationToken));
        return TypedResults.Ok(details.ToArray());
    }

    private static async Task<IResult> GetGroup(
        Guid id,
        AccessGroupManagementService service,
        CancellationToken cancellationToken)
    {
        var group = await service.GetAsync(id, cancellationToken);
        if (group is null || group.Source != AccessGroupSource.Local) return TypedResults.NotFound();
        return TypedResults.Ok(await ToResponse(service, group, cancellationToken));
    }

    private static async Task<IResult> CreateGroup(
        [FromBody] AccessGroupRequest? request,
        AccessGroupManagementService service,
        AccountsDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var displayName = NormalizeDisplayName(request?.DisplayName, "displayName");
        if (displayName is null) return Error(StatusCodes.Status400BadRequest, "invalid_group", "A safe display name is required.");
        if (await dbContext.AccessGroups.AnyAsync(group => group.DisplayName == displayName, cancellationToken))
            return Error(StatusCodes.Status409Conflict, "group_exists", "An access group with this display name already exists.");
        try
        {
            var group = await service.CreateAsync(displayName, request!.IsActive, cancellationToken);
            return TypedResults.Created($"/api/v1/identity/access/groups/{group.Id}",
                await ToResponse(service, group, cancellationToken));
        }
        catch (DbUpdateException)
        {
            return Error(StatusCodes.Status409Conflict, "group_conflict", "The access group conflicts with another change.");
        }
    }

    private static async Task<IResult> UpdateGroup(
        Guid id,
        [FromBody] AccessGroupRequest? request,
        AccessGroupManagementService service,
        AccountsDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var displayName = NormalizeDisplayName(request?.DisplayName, "displayName");
        if (displayName is null) return Error(StatusCodes.Status400BadRequest, "invalid_group", "A safe display name is required.");
        var current = await service.GetAsync(id, cancellationToken);
        if (current is null) return TypedResults.NotFound();
        if (string.IsNullOrWhiteSpace(request!.ConcurrencyStamp))
            return Error(StatusCodes.Status400BadRequest, "concurrency_required", "A concurrency stamp is required.");
        if (!string.Equals(request.ConcurrencyStamp, current.ConcurrencyStamp, StringComparison.Ordinal))
            return Error(StatusCodes.Status409Conflict, "group_conflict", "The access group changed concurrently.");
        if (await dbContext.AccessGroups.AnyAsync(group => group.Id != id && group.DisplayName == displayName, cancellationToken))
            return Error(StatusCodes.Status409Conflict, "group_exists", "An access group with this display name already exists.");
        try
        {
            var group = await service.UpdateAsync(id, displayName, request.IsActive, request.ConcurrencyStamp, cancellationToken);
            return group is null ? TypedResults.NotFound() : TypedResults.Ok(await ToResponse(service, group, cancellationToken));
        }
        catch (AccessGroupValidationException exception)
        {
            return Error(exception.StatusCode, exception.Code, exception.Message);
        }
    }

    private static async Task<IResult> DeleteGroup(
        Guid id,
        [FromBody] MutationRequest? request,
        AccessGroupManagementService service,
        CancellationToken cancellationToken)
    {
        var group = await service.GetAsync(id, cancellationToken);
        if (group is null) return TypedResults.NotFound();
        if (string.IsNullOrWhiteSpace(request?.ConcurrencyStamp))
            return Error(StatusCodes.Status400BadRequest, "concurrency_required", "A concurrency stamp is required.");
        try
        {
            return await service.DeleteAsync(id, request.ConcurrencyStamp, cancellationToken) ? TypedResults.NoContent() : TypedResults.NotFound();
        }
        catch (AccessGroupValidationException exception)
        {
            return Error(exception.StatusCode, exception.Code, exception.Message);
        }
    }

    private static async Task<IResult> AddMember(
        Guid groupId,
        Guid userId,
        [FromBody] MutationRequest? request,
        AccessGroupManagementService service,
        CancellationToken cancellationToken) =>
        await MemberMutation(groupId, userId, request?.ConcurrencyStamp, add: true, service, cancellationToken);

    private static async Task<IResult> RemoveMember(
        Guid groupId,
        Guid userId,
        [FromBody] MutationRequest? request,
        AccessGroupManagementService service,
        CancellationToken cancellationToken) =>
        await MemberMutation(groupId, userId, request?.ConcurrencyStamp, add: false, service, cancellationToken);

    private static async Task<IResult> MemberMutation(
        Guid groupId,
        Guid userId,
        string? concurrencyStamp,
        bool add,
        AccessGroupManagementService service,
        CancellationToken cancellationToken)
    {
        if (string.IsNullOrWhiteSpace(concurrencyStamp)) return Error(StatusCodes.Status400BadRequest, "concurrency_required", "A concurrency stamp is required.");
        try
        {
            var group = add
                ? await service.AddMemberAsync(groupId, userId, concurrencyStamp, cancellationToken)
                : await service.RemoveMemberAsync(groupId, userId, concurrencyStamp, cancellationToken);
            if (group is null || group.Source != AccessGroupSource.Local) return TypedResults.NotFound();
            return TypedResults.Ok(await ToResponse(service, group, cancellationToken));
        }
        catch (AccessGroupValidationException exception)
        {
            return Error(exception.StatusCode, exception.Code, exception.Message);
        }
    }

    private static async Task<IResult> AddRoleMapping(
        Guid groupId,
        Guid roleId,
        [FromBody] MutationRequest? request,
        AccessGroupManagementService service,
        CancellationToken cancellationToken) =>
        await RoleMappingMutation(groupId, roleId, request?.ConcurrencyStamp, request?.ScimConnectionId, add: true, service, cancellationToken);

    private static async Task<IResult> RemoveRoleMapping(
        Guid groupId,
        Guid roleId,
        [FromBody] MutationRequest? request,
        AccessGroupManagementService service,
        CancellationToken cancellationToken) =>
        await RoleMappingMutation(groupId, roleId, request?.ConcurrencyStamp, request?.ScimConnectionId, add: false, service, cancellationToken);

    private static async Task<IResult> RoleMappingMutation(
        Guid groupId,
        Guid roleId,
        string? concurrencyStamp,
        Guid? scimConnectionId,
        bool add,
        AccessGroupManagementService service,
        CancellationToken cancellationToken)
    {
        if (string.IsNullOrWhiteSpace(concurrencyStamp)) return Error(StatusCodes.Status400BadRequest, "concurrency_required", "A concurrency stamp is required.");
        try
        {
            var group = add
                ? await service.AddRoleMappingAsync(groupId, roleId, concurrencyStamp, scimConnectionId, cancellationToken)
                : await service.RemoveRoleMappingAsync(groupId, roleId, concurrencyStamp, scimConnectionId, cancellationToken);
            if (group is null) return TypedResults.NotFound();
            return TypedResults.Ok(await ToResponse(service, group, cancellationToken));
        }
        catch (AccessGroupValidationException exception)
        {
            return Error(exception.StatusCode, exception.Code, exception.Message);
        }
    }

    private static IResult? ValidateFederationRequest(FederationConnectionRequest? request, bool isUpdate)
    {
        if (request is null) return Error(StatusCodes.Status400BadRequest, "invalid_connection", "A federation connection request is required.");
        var provider = First(request.ProviderType, request.ProviderKind);
        if (!Enum.TryParse<FederationProviderKind>(provider, ignoreCase: true, out var providerKind) ||
            !Enum.IsDefined(providerKind))
            return Error(StatusCodes.Status400BadRequest, "invalid_provider", "Provider type must be Entra, Google, or Generic.");
        if (NormalizeDisplayName(request.DisplayName, "displayName") is null)
            return Error(StatusCodes.Status400BadRequest, "invalid_connection", "A safe display name is required.");
        var authority = First(request.Authority, request.Issuer);
        if (!TryNormalizeHttpsAuthority(authority, out _))
            return Error(StatusCodes.Status400BadRequest, "invalid_authority", "Authority/issuer must be an absolute HTTPS URL without credentials, query, or fragment.");
        if (string.IsNullOrWhiteSpace(request.ClientId) || request.ClientId.Trim().Length > 256 || request.ClientId.Any(char.IsControl))
            return Error(StatusCodes.Status400BadRequest, "invalid_client_id", "ClientId is required and must be safe.");
        if (!TryNormalizeDomains(request.AllowedDomains, out _, out var domainError))
            return Error(StatusCodes.Status400BadRequest, "invalid_domain", domainError!);
        if (providerKind == FederationProviderKind.Google && (request.AllowedDomains is null || request.AllowedDomains.Count == 0))
            return Error(StatusCodes.Status400BadRequest, "invalid_domain", "Google connections require at least one allowed domain.");
        if (!Enum.TryParse<JitCreationMode>(request.JitCreationMode, ignoreCase: true, out var jitMode) ||
            !Enum.IsDefined(jitMode))
            return Error(StatusCodes.Status400BadRequest, "invalid_jit_mode", "JIT creation mode must be Disabled or CreateUser.");
        if (request.IsDefault)
            return Error(StatusCodes.Status400BadRequest, "invalid_default", "A federation connection must be validated and enabled before becoming default.");
        if (!string.IsNullOrWhiteSpace(request.ClientSecretReference) &&
            !FederationConnectionRules.TryNormalizeClientSecretReference(request.ClientSecretReference, out _))
        {
            return Error(StatusCodes.Status400BadRequest, "invalid_client_secret_reference", "ClientSecretReference must be a safe configuration key or environment-variable name.");
        }
        if (isUpdate && request.ClearClientSecretReference && !string.IsNullOrWhiteSpace(request.ClientSecretReference))
            return Error(StatusCodes.Status400BadRequest, "invalid_client_secret_reference", "ClearClientSecretReference cannot be used with a reference value.");
        return null;
    }

    private static FederationRequestValues ReadFederationRequest(FederationConnectionRequest request)
    {
        Enum.TryParse(First(request.ProviderType, request.ProviderKind), true, out FederationProviderKind providerKind);
        Enum.TryParse(request.JitCreationMode, true, out JitCreationMode jitCreationMode);
        TryNormalizeHttpsAuthority(First(request.Authority, request.Issuer), out var authority);
        TryNormalizeDomains(request.AllowedDomains, out var domains, out _);
        var clientSecretReference = string.IsNullOrWhiteSpace(request.ClientSecretReference) ? null : request.ClientSecretReference.Trim();
        return new(
            providerKind,
            NormalizeDisplayName(request.DisplayName, "displayName")!,
            authority!,
            request.ClientId!.Trim(),
            clientSecretReference,
            domains!,
            request.IsDefault,
            jitCreationMode,
            request.ClearClientSecretReference);
    }

    private static FederationConnectionResponse ToResponse(FederationConnection connection) => new(
        connection.Id,
        connection.ProviderKind.ToString(),
        connection.DisplayName,
        connection.Authority,
        connection.ClientId,
        connection.AllowedDomains,
        connection.IsEnabled,
        connection.IsDefault,
        connection.JitCreationMode.ToString(),
        connection.ConfigurationVersion,
        connection.ConcurrencyStamp,
        connection.ClientSecretReference,
        connection.ValidationState.ToString(),
        connection.ValidationErrorCode,
        connection.ValidationCompletedAt,
        connection.ValidatedConfigurationVersion,
        connection.ValidatedIssuer,
        connection.ValidatedDiscoveryEndpoint,
        connection.ValidatedAuthorizationEndpoint,
        connection.ValidatedTokenEndpoint,
        connection.ValidatedJwksUri);

    private static async Task<AccessGroupResponse> ToResponse(
        AccessGroupManagementService service,
        AccessGroup group,
        CancellationToken cancellationToken)
    {
        var details = await service.DetailsAsync(group, cancellationToken);
        return new(
            group.Id,
            group.DisplayName,
            group.Source.ToString(),
            group.ScimConnectionId,
            group.IsActive,
            group.CreatedAt,
            group.UpdatedAt,
            group.ConcurrencyStamp,
            details.MemberUserIds,
            details.RoleIds);
    }

    private static string? NormalizeDisplayName(string? value, string _)
    {
        if (string.IsNullOrWhiteSpace(value)) return null;
        var normalized = value.Trim();
        return normalized.Length <= 200 && !normalized.Any(char.IsControl) ? normalized : null;
    }

    private static bool TryNormalizeHttpsAuthority(string? value, out string? normalized)
    {
        normalized = null;
        if (string.IsNullOrWhiteSpace(value) || !Uri.TryCreate(value.Trim(), UriKind.Absolute, out var uri) ||
            uri.Scheme != Uri.UriSchemeHttps || string.IsNullOrEmpty(uri.Host) ||
            !string.IsNullOrEmpty(uri.UserInfo) || !string.IsNullOrEmpty(uri.Query) ||
            !string.IsNullOrEmpty(uri.Fragment)) return false;
        var path = uri.AbsolutePath.TrimEnd('/');
        normalized = $"https://{uri.Host.ToLowerInvariant()}{(uri.IsDefaultPort ? "" : ":" + uri.Port.ToString(CultureInfo.InvariantCulture))}{path}";
        return normalized.Length <= 2048;
    }

    private static bool TryNormalizeDomains(
        IReadOnlyCollection<string>? values,
        out string[]? normalized,
        out string? error)
    {
        normalized = null;
        error = null;
        if (values is null) { error = "AllowedDomains is required."; return false; }
        var result = new HashSet<string>(StringComparer.Ordinal);
        foreach (var value in values)
        {
            var domain = value.Trim().TrimEnd('.').ToLowerInvariant();
            if (domain.Length is 0 or > 253 || domain.Contains('/') || domain.Contains('@') ||
                domain.Contains(':') || domain.Any(char.IsWhiteSpace) ||
                !DomainRegex().IsMatch(domain))
            {
                error = "Allowed domains must be bare DNS names (for example, example.com).";
                return false;
            }
            result.Add(domain);
        }
        normalized = result.Order(StringComparer.Ordinal).ToArray();
        return true;
    }

    [GeneratedRegex(@"^(?=.{1,253}$)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$", RegexOptions.CultureInvariant)]
    private static partial Regex DomainRegex();

    private static string? First(string? first, string? second) =>
        string.IsNullOrWhiteSpace(first) ? second : first;

    private static IResult Error(int statusCode, string code, string message) =>
        TypedResults.Json(new { code, message }, statusCode: statusCode);

    private sealed record FederationRequestValues(
        FederationProviderKind ProviderKind,
        string DisplayName,
        string Authority,
        string ClientId,
        string? ClientSecretReference,
        string[] AllowedDomains,
        bool IsDefault,
        JitCreationMode JitCreationMode,
        bool ClearClientSecretReference);
}

internal sealed record FederationConnectionRequest(
    string? ProviderType,
    string? DisplayName,
    string? Authority,
    string? ClientId,
    string? ClientSecretReference,
    IReadOnlyCollection<string>? AllowedDomains,
    bool IsEnabled,
    bool IsDefault,
    string? JitCreationMode,
    string? ConcurrencyStamp = null,
    bool ClearClientSecretReference = false,
    string? ProviderKind = null,
    string? Issuer = null);

internal sealed record FederationConnectionResponse(
    Guid Id,
    string ProviderType,
    string DisplayName,
    string Authority,
    string ClientId,
    IReadOnlyCollection<string> AllowedDomains,
    bool IsEnabled,
    bool IsDefault,
    string JitCreationMode,
    int ConfigurationVersion,
    string ConcurrencyStamp,
    string? ClientSecretReference,
    string ValidationState,
    string? ValidationErrorCode,
    DateTimeOffset? ValidationCompletedAt,
    int? ValidatedConfigurationVersion,
    string? ValidatedIssuer,
    string? ValidatedDiscoveryEndpoint,
    string? ValidatedAuthorizationEndpoint,
    string? ValidatedTokenEndpoint,
    string? ValidatedJwksUri);

internal sealed record AccessGroupRequest(
    string? DisplayName,
    bool IsActive,
    string? ConcurrencyStamp = null);

internal sealed record MutationRequest(string? ConcurrencyStamp, bool IsDefault = false, Guid? ScimConnectionId = null);

internal sealed record AccessGroupResponse(
    Guid Id,
    string DisplayName,
    string Source,
    Guid? ScimConnectionId,
    bool IsActive,
    DateTimeOffset CreatedAt,
    DateTimeOffset UpdatedAt,
    string ConcurrencyStamp,
    IReadOnlyCollection<Guid> MemberUserIds,
    IReadOnlyCollection<Guid> RoleIds);