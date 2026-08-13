using System.Globalization;
using System.Net.Mail;
using System.Security.Claims;
using System.Text.Json;
using System.Text.RegularExpressions;

using Microsoft.AspNetCore.Http;
using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;

using Vantigo.Identity.Authorization;
using Vantigo.Identity.Database.Accounts;
using Vantigo.Identity.Endpoints.Auth;

namespace Vantigo.Identity.Services;

/// <summary>
/// The deliberately small, deployment-bound SCIM protocol implementation.  It
/// does not use ASP.NET Identity authentication: every request is authenticated
/// with a bearer token and is then scoped to the one connection represented by
/// that token.
/// </summary>
public sealed class ScimProtocolService(
    AccountsDbContext dbContext,
    ScimTokenService tokenService,
    ScimLifecycleService lifecycleService,
    UserManager<ApplicationUser> userManager,
    AuthorizationAuditWriter auditWriter,
    IHttpContextAccessor httpContextAccessor,
    ScimIngressRateLimiter ingressRateLimiter,
    OperationalEventService operationalEventService)
{
    public static Func<Exception?>? AuditFailureInjector { get; set; }
    public const string BasePath = "/api/v1/identity/scim/v2";
    public const string ScimMediaType = "application/scim+json";
    public const string UserSchema = "urn:ietf:params:scim:schemas:core:2.0:User";
    public const string GroupSchema = "urn:ietf:params:scim:schemas:core:2.0:Group";
    public const string ListResponseSchema = "urn:ietf:params:scim:api:messages:2.0:ListResponse";
    public const string PatchSchema = "urn:ietf:params:scim:api:messages:2.0:PatchOp";
    private const int MaximumPageSize = 100;
    private const int MaximumGroupMembersPerMutation = 100;
    public const int MaximumRequestBodyBytes = 256 * 1024;
    private const int MaximumFilterLength = 512;

    private static readonly JsonSerializerOptions JsonOptions = new(JsonSerializerDefaults.Web)
    {
        PropertyNameCaseInsensitive = true,
    };

    public async Task<IResult> ServiceProviderConfigAsync(CancellationToken cancellationToken)
    {
        var authentication = await AuthenticateAsync(cancellationToken);
        if (authentication.Error is not null) return authentication.Error;

        return Ok(new
        {
            schemas = new[] { "urn:ietf:params:scim:schemas:core:2.0:ServiceProviderConfig" },
            documentationUri = "https://www.rfc-editor.org/rfc/rfc7644",
            patch = new { supported = true },
            bulk = new { supported = false, maxOperations = 0, maxPayloadSize = 0 },
            filter = new { supported = true, maxResults = MaximumPageSize },
            changePassword = new { supported = false },
            sort = new { supported = false },
            etag = new { supported = true },
            authenticationSchemes = new[]
            {
                new
                {
                    type = "oauthbearertoken",
                    name = "SCIM bearer token",
                    description = "Deployment-bound bearer token issued for one SCIM connection.",
                    specUri = "https://www.rfc-editor.org/rfc/rfc6750",
                    documentationUri = "https://www.rfc-editor.org/rfc/rfc7644",
                    primary = true,
                },
            },
        });
    }

    public async Task<IResult> SchemasAsync(CancellationToken cancellationToken)
    {
        var authentication = await AuthenticateAsync(cancellationToken);
        if (authentication.Error is not null) return authentication.Error;

        return Ok(new
        {
            schemas = new[] { "urn:ietf:params:scim:api:messages:2.0:ListResponse" },
            totalResults = 2,
            Resources = new[] { UserSchemaDocument(), GroupSchemaDocument() },
        });
    }

    public async Task<IResult> ResourceTypesAsync(CancellationToken cancellationToken)
    {
        var authentication = await AuthenticateAsync(cancellationToken);
        if (authentication.Error is not null) return authentication.Error;

        return Ok(new
        {
            schemas = new[] { "urn:ietf:params:scim:schemas:core:2.0:ResourceType" },
            totalResults = 2,
            Resources = new[]
            {
                new { id = "User", name = "User", endpoint = BasePath + "/Users", description = "SCIM User", schema = UserSchema },
                new { id = "Group", name = "Group", endpoint = BasePath + "/Groups", description = "SCIM Group", schema = GroupSchema },
            },
        });
    }

    public async Task<IResult> CreateUserAsync(JsonElement body, CancellationToken cancellationToken)
    {
        var authentication = await AuthenticateAsync(cancellationToken);
        if (authentication.Error is not null) return authentication.Error;
        if (!TryReadUser(body, requireUserName: true, requireExternalId: false, out var input, out var error))
            return error!;

        var connectionId = authentication.ConnectionId!.Value;
        input!.ExternalId ??= Guid.NewGuid().ToString("D");
        var validation = await ValidateUserInputAsync(connectionId, input!, null, cancellationToken);
        if (validation is not null) return validation;

        await using var transaction = await dbContext.Database.BeginTransactionAsync(
            System.Data.IsolationLevel.Serializable, cancellationToken);
        try
        {
            var mapping = await dbContext.ScimUserMappings.SingleOrDefaultAsync(item =>
                item.ScimConnectionId == connectionId && item.ExternalId == input!.ExternalId, cancellationToken);

            // OIDC-first correlation deliberately joins this transaction. The
            // mapping audit, SCIM resource mutation, and protocol audit must
            // commit or roll back as one unit.
            ApplicationUser user;
            var isNewUser = mapping is null;
            if (mapping is null)
            {
                user = new ApplicationUser
                {
                    UserName = input!.UserName,
                    Email = input.Email,
                    EmailConfirmed = true,
                    DisplayName = DisplayNameFor(input),
                    IsDisabled = false,
                };
                var create = await userManager.CreateAsync(user);
                if (!create.Succeeded)
                    return UserManagerFailure(create);

                // No role, group, Owner, or other authorization assignment is
                // made here.  SCIM-created users are intentionally unprivileged.
                mapping = NewMapping(connectionId, user.Id, input!, resourceId: Guid.NewGuid().ToString("D"));
                dbContext.ScimUserMappings.Add(mapping);
            }
            else
            {
                user = await dbContext.Users.SingleOrDefaultAsync(item => item.Id == mapping.UserId, cancellationToken)
                    ?? throw new InvalidOperationException("The SCIM mapping points to a missing user.");
                if (!await lifecycleService.IsScimControllableUserAsync(user.Id, cancellationToken))
                    return Error(StatusCodes.Status400BadRequest, "mutability", "The selected user is protected.");
            }

            ApplyUserInput(user, mapping, input!, replace: isNewUser ? false : true);
            await dbContext.SaveChangesAsync(cancellationToken);
            await WriteAuditAsync("scim.user.created", connectionId, mapping, null,
                await CaptureUserFactsAsync(mapping, user, connectionId, cancellationToken), cancellationToken);
            await transaction.CommitAsync(cancellationToken);

            var resource = await ReadUserResourceAsync(connectionId, mapping.ResourceId, includeMeta: true, cancellationToken);
            SetResponseEtag(mapping.ETag);
            return Created(UserLocation(mapping.ResourceId), resource!);
        }
        catch (DbUpdateConcurrencyException)
        {
            return Error(StatusCodes.Status412PreconditionFailed, "invalidVers", "The SCIM resource version is stale.");
        }
        catch (DbUpdateException exception) when (IsUniqueViolation(exception))
        {
            return Error(StatusCodes.Status409Conflict, "uniqueness", "The SCIM resource conflicts with an existing resource.");
        }
    }

    public async Task<IResult> GetUserAsync(string resourceId, CancellationToken cancellationToken)
    {
        var authentication = await AuthenticateAsync(cancellationToken);
        if (authentication.Error is not null) return authentication.Error;
        var resource = await ReadUserResourceAsync(authentication.ConnectionId!.Value, resourceId, true, cancellationToken);
        if (resource is not null)
            SetResponseEtag(await CurrentUserEtagAsync(authentication.ConnectionId.Value, resourceId, cancellationToken));
        return resource is null
            ? Error(StatusCodes.Status404NotFound, null, "The requested resource was not found.")
            : Ok(resource);
    }

    public async Task<IResult> ListUsersAsync(HttpRequest request, CancellationToken cancellationToken)
    {
        var authentication = await AuthenticateAsync(cancellationToken);
        if (authentication.Error is not null) return authentication.Error;
        if (!TryReadPaging(request, out var startIndex, out var count, out var pagingError)) return pagingError!;
        if (!TryReadFilter(request.Query["filter"], new[] { "userName", "externalId" }, out var filters, out var filterError))
            return filterError!;

        var mappingsQuery = dbContext.ScimUserMappings.AsNoTracking()
            .Where(item => item.ScimConnectionId == authentication.ConnectionId!.Value)
            .AsQueryable();
        if (filters is not null)
            foreach (var filter in filters)
                mappingsQuery = filter.Field == "userName"
                    ? mappingsQuery.Where(item => item.UserName == filter.Value)
                    : mappingsQuery.Where(item => item.ExternalId == filter.Value);

        var total = await mappingsQuery.CountAsync(cancellationToken);
        var mappings = await mappingsQuery.OrderBy(item => item.ResourceId)
            .Skip(startIndex - 1).Take(count).ToListAsync(cancellationToken);
        var resources = new List<object>();
        foreach (var mapping in mappings)
        {
            var resource = await ReadUserResourceAsync(authentication.ConnectionId.Value, mapping.ResourceId, true, cancellationToken);
            if (resource is not null) resources.Add(resource);
        }

        return Ok(new
        {
            schemas = new[] { ListResponseSchema },
            totalResults = total,
            startIndex,
            itemsPerPage = resources.Count,
            Resources = resources,
        });
    }

    public async Task<IResult> PutUserAsync(string resourceId, JsonElement body, HttpRequest request, CancellationToken cancellationToken)
    {
        var authentication = await AuthenticateAsync(cancellationToken);
        if (authentication.Error is not null) return authentication.Error;
        if (!TryReadUser(body, requireUserName: true, requireExternalId: false, out var input, out var error)) return error!;

        var connectionId = authentication.ConnectionId!.Value;
        var mapping = await dbContext.ScimUserMappings.SingleOrDefaultAsync(item =>
            item.ScimConnectionId == connectionId && item.ResourceId == resourceId, cancellationToken);
        if (mapping is null) return Error(StatusCodes.Status404NotFound, null, "The requested resource was not found.");
        var precondition = CheckPrecondition(request, mapping.ETag);
        if (precondition is not null) return precondition;
        if (!await lifecycleService.IsScimControllableUserAsync(mapping.UserId, cancellationToken))
            return Error(StatusCodes.Status400BadRequest, "mutability", "The selected user is protected.");

        input!.ExternalId ??= mapping.ExternalId;
        var validation = await ValidateUserInputAsync(connectionId, input, mapping.Id, cancellationToken);
        if (validation is not null) return validation;

        await using var transaction = await dbContext.Database.BeginTransactionAsync(cancellationToken);
        try
        {
            var user = await dbContext.Users.SingleOrDefaultAsync(item => item.Id == mapping.UserId, cancellationToken);
            if (user is null) return Error(StatusCodes.Status404NotFound, null, "The requested resource was not found.");
            var beforeFacts = await CaptureUserFactsAsync(mapping, user, connectionId, cancellationToken);
            ApplyUserInput(user, mapping, input, replace: true);
            Touch(mapping);
            await dbContext.SaveChangesAsync(cancellationToken);
            await WriteAuditAsync("scim.user.replaced", connectionId, mapping, beforeFacts,
                await CaptureUserFactsAsync(mapping, user, connectionId, cancellationToken), cancellationToken);
            await transaction.CommitAsync(cancellationToken);
            var resource = await ReadUserResourceAsync(connectionId, resourceId, true, cancellationToken);
            SetResponseEtag(mapping.ETag);
            return Ok(resource!);
        }
        catch (DbUpdateConcurrencyException)
        {
            return Error(StatusCodes.Status412PreconditionFailed, "invalidVers", "The SCIM resource version is stale.");
        }
        catch (DbUpdateException exception) when (IsUniqueViolation(exception))
        {
            return Error(StatusCodes.Status409Conflict, "uniqueness", "The SCIM resource conflicts with an existing resource.");
        }
    }

    public async Task<IResult> PatchUserAsync(string resourceId, JsonElement body, HttpRequest request, CancellationToken cancellationToken)
    {
        var authentication = await AuthenticateAsync(cancellationToken);
        if (authentication.Error is not null) return authentication.Error;
        if (!TryReadPatch(body, out var operations, out var patchError)) return patchError!;

        var connectionId = authentication.ConnectionId!.Value;
        var mapping = await dbContext.ScimUserMappings.SingleOrDefaultAsync(item =>
            item.ScimConnectionId == connectionId && item.ResourceId == resourceId, cancellationToken);
        if (mapping is null) return Error(StatusCodes.Status404NotFound, null, "The requested resource was not found.");
        var precondition = CheckPrecondition(request, mapping.ETag);
        if (precondition is not null) return precondition;
        if (!await lifecycleService.IsScimControllableUserAsync(mapping.UserId, cancellationToken))
            return Error(StatusCodes.Status400BadRequest, "mutability", "The selected user is protected.");

        var user = await dbContext.Users.SingleOrDefaultAsync(item => item.Id == mapping.UserId, cancellationToken);
        if (user is null) return Error(StatusCodes.Status404NotFound, null, "The requested resource was not found.");
        if (!TryValidateUserOperations(operations!, mapping, user, out var actions, out var operationError))
            return operationError!;
        var externalAction = actions!.FirstOrDefault(item => item.Field == "externalId");
        if (externalAction is not null && !string.Equals(NormalizeExternalId(externalAction.Value), mapping.ExternalId, StringComparison.Ordinal))
            return Error(StatusCodes.Status409Conflict, "mutability", "externalId is immutable once mapped.");
        var beforeFacts = await CaptureUserFactsAsync(mapping, user, connectionId, cancellationToken);

        await using var transaction = await dbContext.Database.BeginTransactionAsync(cancellationToken);
        try
        {
            foreach (var action in actions!) ApplyUserAction(user, mapping, action);
            Touch(mapping);
            await dbContext.SaveChangesAsync(cancellationToken);
            await WriteAuditAsync("scim.user.patched", connectionId, mapping,
                beforeFacts,
                await CaptureUserFactsAsync(mapping, user, connectionId, cancellationToken), cancellationToken);
            await transaction.CommitAsync(cancellationToken);
            var resource = await ReadUserResourceAsync(connectionId, resourceId, true, cancellationToken);
            SetResponseEtag(mapping.ETag);
            return Ok(resource!);
        }
        catch (DbUpdateConcurrencyException)
        {
            return Error(StatusCodes.Status412PreconditionFailed, "invalidVers", "The SCIM resource version is stale.");
        }
        catch (DbUpdateException exception) when (IsUniqueViolation(exception))
        {
            return Error(StatusCodes.Status409Conflict, "uniqueness", "The SCIM resource conflicts with an existing resource.");
        }
    }

    public async Task<IResult> DeleteUserAsync(string resourceId, HttpRequest request, CancellationToken cancellationToken)
    {
        var authentication = await AuthenticateAsync(cancellationToken);
        if (authentication.Error is not null) return authentication.Error;
        var connectionId = authentication.ConnectionId!.Value;
        var mapping = await dbContext.ScimUserMappings.SingleOrDefaultAsync(item =>
            item.ScimConnectionId == connectionId && item.ResourceId == resourceId, cancellationToken);
        if (mapping is null) return Error(StatusCodes.Status404NotFound, null, "The requested resource was not found.");
        var precondition = CheckPrecondition(request, mapping.ETag);
        if (precondition is not null) return precondition;
        if (!await lifecycleService.IsScimControllableUserAsync(mapping.UserId, cancellationToken))
            return Error(StatusCodes.Status400BadRequest, "mutability", "The selected user is protected.");

        var user = await dbContext.Users.SingleOrDefaultAsync(item => item.Id == mapping.UserId, cancellationToken);
        if (user is null) return Error(StatusCodes.Status404NotFound, null, "The requested resource was not found.");
        var beforeFacts = await CaptureUserFactsAsync(mapping, user, connectionId, cancellationToken);
        await using var transaction = await dbContext.Database.BeginTransactionAsync(cancellationToken);
        mapping.UpstreamActive = false;
        Touch(mapping);
        var memberships = await dbContext.AccessGroupMemberships
            .Join(dbContext.AccessGroups, membership => membership.GroupId, group => group.Id,
                (membership, group) => new { membership, group })
            .Where(item => item.group.ScimConnectionId == connectionId && item.membership.UserId == mapping.UserId &&
                item.membership.Source == AccessGroupSource.Scim)
            .ToListAsync(cancellationToken);
        foreach (var item in memberships) item.membership.IsUpstreamPresent = false;
        await dbContext.SaveChangesAsync(cancellationToken);
        await WriteAuditAsync("scim.user.deleted", connectionId, mapping,
            beforeFacts,
            await CaptureUserFactsAsync(mapping, user, connectionId, cancellationToken), cancellationToken);
        await transaction.CommitAsync(cancellationToken);
        SetResponseEtag(mapping.ETag);
        return NoContent();
    }

    public async Task<IResult> CreateGroupAsync(JsonElement body, CancellationToken cancellationToken)
    {
        var authentication = await AuthenticateAsync(cancellationToken);
        if (authentication.Error is not null) return authentication.Error;
        if (!TryReadGroup(body, requireDisplayName: true, out var input, out var error)) return error!;
        var connectionId = authentication.ConnectionId!.Value;
        var duplicate = await dbContext.AccessGroups.AnyAsync(group =>
            group.DisplayName == input!.DisplayName ||
            group.ScimConnectionId == connectionId && group.ExternalId == input.ExternalId, cancellationToken);
        if (duplicate) return Error(StatusCodes.Status409Conflict, "uniqueness", "The SCIM group conflicts with an existing group.");

        await using var transaction = await dbContext.Database.BeginTransactionAsync(cancellationToken);
        try
        {
            var now = DateTimeOffset.UtcNow;
            var group = new AccessGroup
            {
                ScimConnectionId = connectionId,
                DisplayName = input!.DisplayName!,
                ExternalId = input.ExternalId ?? Guid.NewGuid().ToString("D"),
                Source = AccessGroupSource.Scim,
                IsActive = input.Active,
                CreatedAt = now,
                UpdatedAt = now,
                ConcurrencyStamp = Guid.NewGuid().ToString("N"),
            };
            dbContext.AccessGroups.Add(group);
            await dbContext.SaveChangesAsync(cancellationToken);
            if (input.MemberIds.Count > MaximumGroupMembersPerMutation)
                return Error(StatusCodes.Status400BadRequest, "tooMany", "A group membership mutation exceeds the safe member cap.");
            var memberError = await ApplyGroupMembersAsync(group, input.MemberIds, add: true, connectionId, cancellationToken);
            if (memberError is not null) return memberError;
            await dbContext.SaveChangesAsync(cancellationToken);
            await WriteGroupAuditAsync("scim.group.created", connectionId, group, null,
                await CaptureGroupFactsAsync(group, cancellationToken), cancellationToken);
            await transaction.CommitAsync(cancellationToken);
            SetResponseEtag(group.ConcurrencyStamp);
            return Created(GroupLocation(group.Id), await ReadGroupResourceAsync(connectionId, group.Id, false, cancellationToken));
        }
        catch (DbUpdateException exception) when (IsUniqueViolation(exception))
        {
            return Error(StatusCodes.Status409Conflict, "uniqueness", "The SCIM group conflicts with an existing group.");
        }
    }

    public async Task<IResult> GetGroupAsync(string resourceId, HttpRequest request, CancellationToken cancellationToken)
    {
        var authentication = await AuthenticateAsync(cancellationToken);
        if (authentication.Error is not null) return authentication.Error;
        if (!Guid.TryParse(resourceId, out var groupId)) return Error(StatusCodes.Status404NotFound, null, "The requested resource was not found.");
        var group = await FindGroupAsync(authentication.ConnectionId!.Value, groupId, cancellationToken);
        if (group is null) return Error(StatusCodes.Status404NotFound, null, "The requested resource was not found.");
        SetResponseEtag(group.ConcurrencyStamp);
        return Ok(await ReadGroupResourceAsync(authentication.ConnectionId.Value, group.Id, IsMembersExcluded(request), cancellationToken));
    }

    public async Task<IResult> ListGroupsAsync(HttpRequest request, CancellationToken cancellationToken)
    {
        var authentication = await AuthenticateAsync(cancellationToken);
        if (authentication.Error is not null) return authentication.Error;
        if (!TryReadPaging(request, out var startIndex, out var count, out var pagingError)) return pagingError!;
        if (!TryReadFilter(request.Query["filter"], new[] { "displayName", "externalId" }, out var filters, out var filterError))
            return filterError!;
        var groupsQuery = dbContext.AccessGroups.AsNoTracking()
            .Where(group => group.ScimConnectionId == authentication.ConnectionId!.Value && group.Source == AccessGroupSource.Scim)
            .AsQueryable();
        if (filters is not null)
            foreach (var filter in filters)
                groupsQuery = filter.Field == "displayName"
                    ? groupsQuery.Where(group => group.DisplayName == filter.Value)
                    : groupsQuery.Where(group => group.ExternalId == filter.Value);
        var total = await groupsQuery.CountAsync(cancellationToken);
        var groups = await groupsQuery.OrderBy(group => group.Id)
            .Skip(startIndex - 1).Take(count).ToListAsync(cancellationToken);
        var resources = new List<object>();
        foreach (var group in groups)
            resources.Add(await ReadGroupResourceAsync(authentication.ConnectionId.Value, group.Id, IsMembersExcluded(request), cancellationToken));
        return Ok(new { schemas = new[] { ListResponseSchema }, totalResults = total, startIndex, itemsPerPage = resources.Count, Resources = resources });
    }

    public async Task<IResult> PatchGroupAsync(string resourceId, JsonElement body, HttpRequest request, CancellationToken cancellationToken)
    {
        var authentication = await AuthenticateAsync(cancellationToken);
        if (authentication.Error is not null) return authentication.Error;
        if (!Guid.TryParse(resourceId, out var groupId)) return Error(StatusCodes.Status404NotFound, null, "The requested resource was not found.");
        if (!TryReadPatch(body, out var operations, out var patchError)) return patchError!;
        var group = await FindGroupAsync(authentication.ConnectionId!.Value, groupId, cancellationToken);
        if (group is null) return Error(StatusCodes.Status404NotFound, null, "The requested resource was not found.");
        var precondition = CheckPrecondition(request, group.ConcurrencyStamp);
        if (precondition is not null) return precondition;
        precondition = CheckMetaVersion(body, group.ConcurrencyStamp);
        if (precondition is not null) return precondition;
        if (!TryValidateGroupOperations(operations!, out var actions, out var operationError)) return operationError!;
        if (actions!.Sum(action => action.MemberIds.Count) > MaximumGroupMembersPerMutation)
            return Error(StatusCodes.Status400BadRequest, "tooMany", "A group membership mutation exceeds the safe member cap.");

        await using var transaction = await dbContext.Database.BeginTransactionAsync(cancellationToken);
        try
        {
            var beforeFacts = await CaptureGroupFactsAsync(group, cancellationToken);
            foreach (var action in actions)
            {
                if (action.Field == "displayName") group.DisplayName = action.StringValue!;
                else if (action.Field == "active") group.IsActive = action.AddMembers;
                else
                {
                    if (action.ReplaceMembers || action.RemoveAllMembers)
                    {
                        var existing = await dbContext.AccessGroupMemberships
                            .Where(item => item.GroupId == group.Id && item.Source == AccessGroupSource.Scim)
                            .ToListAsync(cancellationToken);
                        var requested = action.MemberIds.ToHashSet(StringComparer.Ordinal);
                        var currentMappings = await dbContext.ScimUserMappings
                            .Where(item => item.ScimConnectionId == authentication.ConnectionId.Value)
                            .ToListAsync(cancellationToken);
                        foreach (var membership in existing)
                        {
                            var memberResourceId = currentMappings.FirstOrDefault(item => item.UserId == membership.UserId)?.ResourceId;
                            if (action.RemoveAllMembers || memberResourceId is null || !requested.Contains(memberResourceId))
                                membership.IsUpstreamPresent = false;
                        }
                    }
                    var memberError = action.RemoveAllMembers
                        ? null
                        : await ApplyGroupMembersAsync(group, action.MemberIds, action.AddMembers, authentication.ConnectionId.Value, cancellationToken);
                    if (memberError is not null) return memberError;
                }
            }
            group.UpdatedAt = DateTimeOffset.UtcNow;
            group.ConcurrencyStamp = Guid.NewGuid().ToString("N");
            await dbContext.SaveChangesAsync(cancellationToken);
            await WriteGroupAuditAsync("scim.group.patched", authentication.ConnectionId.Value, group,
                beforeFacts, await CaptureGroupFactsAsync(group, cancellationToken), cancellationToken);
            await transaction.CommitAsync(cancellationToken);
            SetResponseEtag(group.ConcurrencyStamp);
            return Ok(await ReadGroupResourceAsync(authentication.ConnectionId.Value, group.Id, false, cancellationToken));
        }
        catch (DbUpdateConcurrencyException)
        {
            return Error(StatusCodes.Status412PreconditionFailed, "invalidVers", "The SCIM resource version is stale.");
        }
        catch (DbUpdateException exception) when (IsUniqueViolation(exception))
        {
            return Error(StatusCodes.Status409Conflict, "uniqueness", "The SCIM group conflicts with an existing group.");
        }
    }

    public async Task<IResult> DeleteGroupAsync(string resourceId, HttpRequest request, CancellationToken cancellationToken)
    {
        var authentication = await AuthenticateAsync(cancellationToken);
        if (authentication.Error is not null) return authentication.Error;
        if (!Guid.TryParse(resourceId, out var groupId)) return Error(StatusCodes.Status404NotFound, null, "The requested resource was not found.");
        var group = await FindGroupAsync(authentication.ConnectionId!.Value, groupId, cancellationToken);
        if (group is null) return Error(StatusCodes.Status404NotFound, null, "The requested resource was not found.");
        var precondition = CheckPrecondition(request, group.ConcurrencyStamp);
        if (precondition is not null) return precondition;
        precondition = CheckMetaVersion(request, group.ConcurrencyStamp);
        if (precondition is not null) return precondition;
        await using var transaction = await dbContext.Database.BeginTransactionAsync(cancellationToken);
        var beforeFacts = await CaptureGroupFactsAsync(group, cancellationToken);
        group.IsActive = false;
        group.UpdatedAt = DateTimeOffset.UtcNow;
        group.ConcurrencyStamp = Guid.NewGuid().ToString("N");
        var memberships = await dbContext.AccessGroupMemberships.Where(item => item.GroupId == group.Id).ToListAsync(cancellationToken);
        foreach (var membership in memberships)
            if (membership.Source == AccessGroupSource.Scim) membership.IsUpstreamPresent = false;
        await dbContext.SaveChangesAsync(cancellationToken);
        await WriteGroupAuditAsync("scim.group.deleted", authentication.ConnectionId.Value, group,
            beforeFacts, await CaptureGroupFactsAsync(group, cancellationToken), cancellationToken);
        await transaction.CommitAsync(cancellationToken);
        SetResponseEtag(group.ConcurrencyStamp);
        return NoContent();
    }

    private async Task<(Guid? ConnectionId, IResult? Error)> AuthenticateAsync(CancellationToken cancellationToken)
    {
        var header = httpContextAccessor.HttpContext?.Request.Headers.Authorization.ToString();
        if (string.IsNullOrWhiteSpace(header) || !header.StartsWith("Bearer ", StringComparison.OrdinalIgnoreCase))
            return (null, Unauthorized());
        var token = header[7..].Trim();
        if (token.Length == 0 || token.Any(char.IsWhiteSpace)) return (null, Unauthorized());
        var verification = await tokenService.VerifyAsync(token, cancellationToken);
        if (!verification.Succeeded || verification.ScimConnectionId is null) return (null, Unauthorized());
        var connection = await dbContext.ScimConnections.AsNoTracking().SingleOrDefaultAsync(
            item => item.Id == ScimConnection.StaticId && item.Id == verification.ScimConnectionId.Value, cancellationToken);
        return connection is null ? (null, Unauthorized()) : (connection.Id, null);
    }

    /// <summary>
    /// Called by the endpoint filter before any request body is read. Invalid
    /// credentials are partitioned by remote address; valid credentials are
    /// partitioned only by the persisted SCIM connection id.
    /// </summary>
    public async Task<IResult?> AuthenticateAndRateLimitIngressAsync(CancellationToken cancellationToken)
    {
        var context = httpContextAccessor.HttpContext!;
        var header = context.Request.Headers.Authorization.ToString();
        var verification = !string.IsNullOrWhiteSpace(header) && header.StartsWith("Bearer ", StringComparison.OrdinalIgnoreCase)
            ? await tokenService.VerifyAsync(header[7..].Trim(), cancellationToken)
            : new ScimTokenVerificationResult(false, null);
        var connectionId = verification.Succeeded && verification.ScimConnectionId.HasValue
            ? verification.ScimConnectionId.Value.ToString("D")
            : "ip:" + (context.Connection.RemoteIpAddress?.ToString() ?? "unknown");
        if (!await ingressRateLimiter.AllowAsync(connectionId, cancellationToken))
            return Error(StatusCodes.Status429TooManyRequests, "tooMany", "SCIM request rate limit exceeded.");
        if (!verification.Succeeded || !verification.ScimConnectionId.HasValue ||
            !await dbContext.ScimConnections.AsNoTracking().AnyAsync(item =>
                item.Id == ScimConnection.StaticId && item.Id == verification.ScimConnectionId.Value, cancellationToken))
            return Unauthorized();
        try
        {
            await operationalEventService.RecordAsync(OperationalEventKinds.AuthenticatedScimRequest,
                DateTimeOffset.UtcNow, CancellationToken.None);
        }
        catch
        {
            // Operational status is best effort and must not reject valid SCIM traffic.
        }
        return null;
    }

    private async Task<object?> ReadUserResourceAsync(Guid connectionId, string resourceId, bool includeMeta, CancellationToken cancellationToken)
    {
        var row = await dbContext.ScimUserMappings.AsNoTracking()
            .Join(dbContext.Users.AsNoTracking(), mapping => mapping.UserId, user => user.Id,
                (mapping, user) => new { mapping, user })
            .SingleOrDefaultAsync(item => item.mapping.ScimConnectionId == connectionId && item.mapping.ResourceId == resourceId, cancellationToken);
        if (row is null) return null;
        var profile = ReadProfile(row.mapping.SourceProfileJson);
        var payload = new Dictionary<string, object?>(StringComparer.Ordinal)
        {
            ["schemas"] = new[] { UserSchema },
            ["id"] = row.mapping.ResourceId,
            ["externalId"] = row.mapping.ExternalId,
            ["userName"] = row.mapping.UserName,
            ["active"] = row.mapping.UpstreamActive,
            ["displayName"] = row.user.DisplayName,
        };
        if (profile.GivenName is not null || profile.FamilyName is not null)
            payload["name"] = new { givenName = profile.GivenName, familyName = profile.FamilyName };
        if (!string.IsNullOrWhiteSpace(row.user.Email))
            payload["emails"] = new[] { new { value = row.user.Email, type = "work", primary = true } };
        if (includeMeta)
            payload["meta"] = Meta("User", row.mapping.ResourceId, row.mapping.ETag, row.mapping.CreatedAt, row.mapping.UpdatedAt);
        return payload;
    }

    private async Task<object> ReadGroupResourceAsync(Guid connectionId, Guid groupId, bool excludeMembers, CancellationToken cancellationToken)
    {
        var group = await dbContext.AccessGroups.AsNoTracking().SingleAsync(item =>
            item.Id == groupId && item.ScimConnectionId == connectionId && item.Source == AccessGroupSource.Scim, cancellationToken);
        var payload = new Dictionary<string, object?>(StringComparer.Ordinal)
        {
            ["schemas"] = new[] { GroupSchema },
            ["id"] = group.Id.ToString("D"),
            ["externalId"] = group.ExternalId,
            ["displayName"] = group.DisplayName,
            ["active"] = group.IsActive,
            ["meta"] = Meta("Group", group.Id.ToString("D"), group.ConcurrencyStamp, group.CreatedAt, group.UpdatedAt),
        };
        if (!excludeMembers)
        {
            var memberIds = await dbContext.AccessGroupMemberships.AsNoTracking()
                .Where(item => item.GroupId == group.Id && item.Source == AccessGroupSource.Scim &&
                    (item.Override == AccessGroupMembershipOverride.ForceMember ||
                     item.Override == null && item.IsUpstreamPresent))
                .Join(dbContext.ScimUserMappings.AsNoTracking().Where(item => item.ScimConnectionId == connectionId),
                    membership => membership.UserId, mapping => mapping.UserId,
                    (_, mapping) => mapping.ResourceId)
                .ToArrayAsync(cancellationToken);
            payload["members"] = memberIds.Select(id => new { value = id, type = "User", @ref = UserLocation(id) }).ToArray();
        }
        return payload;
    }

    private async Task<AccessGroup?> FindGroupAsync(Guid connectionId, Guid groupId, CancellationToken cancellationToken) =>
        await dbContext.AccessGroups.SingleOrDefaultAsync(item => item.Id == groupId &&
            item.ScimConnectionId == connectionId && item.Source == AccessGroupSource.Scim, cancellationToken);

    private async Task<IResult?> ApplyGroupMembersAsync(AccessGroup group, IReadOnlyCollection<string> resourceIds,
        bool add, Guid connectionId, CancellationToken cancellationToken)
    {
        if (resourceIds.Count > MaximumGroupMembersPerMutation)
            return Error(StatusCodes.Status400BadRequest, "tooMany", "A group membership mutation exceeds the safe member cap.");
        var ids = resourceIds.Distinct(StringComparer.Ordinal).ToArray();
        var mappings = await dbContext.ScimUserMappings.Where(item => item.ScimConnectionId == connectionId && ids.Contains(item.ResourceId)).ToListAsync(cancellationToken);
        if (mappings.Count != ids.Length)
            return Error(StatusCodes.Status400BadRequest, "invalidValue", "Every group member must be a known SCIM User from this connection.");
        foreach (var mapping in mappings)
        {
            var membership = await dbContext.AccessGroupMemberships.SingleOrDefaultAsync(item =>
                item.GroupId == group.Id && item.UserId == mapping.UserId, cancellationToken);
            if (membership is not null && membership.Source == AccessGroupSource.Local && membership.Override.HasValue)
            {
                // SCIM observes only its own upstream record. Never turn a
                // durable local include/exclude override into a SCIM row.
                continue;
            }
            if (membership is null)
            {
                membership = new AccessGroupMembership { GroupId = group.Id, UserId = mapping.UserId };
                dbContext.AccessGroupMemberships.Add(membership);
            }
            membership.Source = AccessGroupSource.Scim;
            membership.IsUpstreamPresent = add;
            membership.UpdatedAt = DateTimeOffset.UtcNow;
            // A local ForceMember/ForceNonMember override is deliberately not
            // erased by an upstream observation.
        }
        return null;
    }

    private async Task<IResult?> ValidateUserInputAsync(Guid connectionId, ScimUserInput input, Guid? currentMappingId, CancellationToken cancellationToken)
    {
        if (!IsSafeValue(input.UserName, 512) || !IsSafeValue(input.ExternalId, 512))
            return Error(StatusCodes.Status400BadRequest, "invalidValue", "userName and externalId must be non-empty safe values.");
        if (input.DisplayName is not null && !IsSafeValue(input.DisplayName, 200))
            return Error(StatusCodes.Status400BadRequest, "invalidValue", "displayName is invalid.");
        if (input.GivenName is not null && !IsSafeValue(input.GivenName, 200) || input.FamilyName is not null && !IsSafeValue(input.FamilyName, 200))
            return Error(StatusCodes.Status400BadRequest, "invalidValue", "name values are invalid.");
        if (input.Email is not null)
        {
            try { _ = new MailAddress(input.Email); }
            catch { return Error(StatusCodes.Status400BadRequest, "invalidValue", "The work email is invalid."); }
        }
        var duplicateName = await dbContext.ScimUserMappings.AnyAsync(item => item.ScimConnectionId == connectionId &&
            item.UserName == input.UserName && item.Id != currentMappingId, cancellationToken);
        if (duplicateName) return Error(StatusCodes.Status409Conflict, "uniqueness", "userName is already used by this connection.");
        var duplicateExternal = await dbContext.ScimUserMappings.AnyAsync(item => item.ScimConnectionId == connectionId &&
            item.ExternalId == input.ExternalId && item.Id != currentMappingId, cancellationToken);
        return duplicateExternal ? Error(StatusCodes.Status409Conflict, "uniqueness", "externalId is already used by this connection.") : null;
    }

    private static bool TryReadUser(JsonElement root, bool requireUserName, bool requireExternalId, out ScimUserInput? input, out IResult? error)
    {
        input = null;
        error = null;
        if (root.ValueKind != JsonValueKind.Object) { error = Error(StatusCodes.Status400BadRequest, "invalidSyntax", "A SCIM resource must be a JSON object."); return false; }
        if (!TryReadSchemas(root, UserSchema, out error)) return false;
        var username = ReadString(root, "userName");
        var externalId = ReadString(root, "externalId");
        if (requireUserName && string.IsNullOrWhiteSpace(username)) { error = Error(StatusCodes.Status400BadRequest, "invalidValue", "userName is required."); return false; }
        if (requireExternalId && string.IsNullOrWhiteSpace(externalId)) { error = Error(StatusCodes.Status400BadRequest, "invalidValue", "externalId is required."); return false; }
        if (TryGet(root, "active", out var activeElement) && activeElement.ValueKind != JsonValueKind.True && activeElement.ValueKind != JsonValueKind.False)
        { error = Error(StatusCodes.Status400BadRequest, "invalidValue", "active must be boolean."); return false; }
        var profile = ReadProfileFromJson(root, out error);
        if (error is not null) return false;
        input = new ScimUserInput
        {
            UserName = username?.Trim(),
            ExternalId = NormalizeExternalId(externalId),
            Active = TryGet(root, "active", out activeElement) ? activeElement.GetBoolean() : null,
            DisplayName = ReadString(root, "displayName")?.Trim(),
            GivenName = profile.GivenName,
            FamilyName = profile.FamilyName,
            Email = profile.Email,
        };
        if (input.UserName is null && !requireUserName) input.UserName = string.Empty;
        return true;
    }

    private static bool TryReadGroup(JsonElement root, bool requireDisplayName, out ScimGroupInput? input, out IResult? error)
    {
        input = null; error = null;
        if (root.ValueKind != JsonValueKind.Object) { error = Error(StatusCodes.Status400BadRequest, "invalidSyntax", "A SCIM resource must be a JSON object."); return false; }
        if (!TryReadSchemas(root, GroupSchema, out error)) return false;
        var displayName = ReadString(root, "displayName")?.Trim();
        if (requireDisplayName && string.IsNullOrWhiteSpace(displayName)) { error = Error(StatusCodes.Status400BadRequest, "invalidValue", "displayName is required."); return false; }
        bool? active = null;
        if (TryGet(root, "active", out var activeElement))
        {
            if (activeElement.ValueKind is not (JsonValueKind.True or JsonValueKind.False)) { error = Error(StatusCodes.Status400BadRequest, "invalidValue", "active must be boolean."); return false; }
            active = activeElement.GetBoolean();
        }
        var memberIds = new List<string>();
        if (TryGet(root, "members", out var members))
        {
            if (members.ValueKind != JsonValueKind.Array) { error = Error(StatusCodes.Status400BadRequest, "invalidValue", "members must be an array."); return false; }
            foreach (var member in members.EnumerateArray())
            {
                var id = ReadString(member, "value");
                if (string.IsNullOrWhiteSpace(id) || !string.IsNullOrWhiteSpace(ReadString(member, "type")) && !string.Equals(ReadString(member, "type"), "User", StringComparison.OrdinalIgnoreCase))
                { error = Error(StatusCodes.Status400BadRequest, "invalidValue", "Only User members are supported."); return false; }
                memberIds.Add(id!);
            }
        }
        var externalId = ReadString(root, "externalId");
        if (externalId is not null && !IsSafeValue(externalId, 512)) { error = Error(StatusCodes.Status400BadRequest, "invalidValue", "externalId is invalid."); return false; }
        input = new ScimGroupInput(displayName, externalId, active ?? true, memberIds);
        return true;
    }

    private static bool TryReadPatch(JsonElement root, out List<ScimPatchOperation>? operations, out IResult? error)
    {
        operations = null; error = null;
        if (root.ValueKind != JsonValueKind.Object || !TryReadSchemas(root, PatchSchema, out error)) return false;
        if (!TryGet(root, "Operations", out var values) || values.ValueKind != JsonValueKind.Array || values.GetArrayLength() == 0)
        { error = Error(StatusCodes.Status400BadRequest, "invalidSyntax", "PatchOp Operations must be a non-empty array."); return false; }
        operations = new List<ScimPatchOperation>();
        foreach (var value in values.EnumerateArray())
        {
            var op = ReadString(value, "op");
            if (op is null || value.ValueKind != JsonValueKind.Object || (!TryGet(value, "value", out var operationValue) && !TryGet(value, "path", out _)))
            { error = Error(StatusCodes.Status400BadRequest, "invalidSyntax", "Each PatchOp operation must contain an op and a value or path."); return false; }
            operations.Add(new ScimPatchOperation(op, ReadString(value, "path"), TryGet(value, "value", out operationValue) ? operationValue : default));
        }
        return true;
    }

    private static bool TryValidateUserOperations(IReadOnlyCollection<ScimPatchOperation> operations, ScimUserMapping mapping, ApplicationUser user,
        out List<ScimUserAction>? actions, out IResult? error)
    {
        actions = []; error = null;
        foreach (var operation in operations)
        {
            var op = operation.Op.Trim().ToLowerInvariant();
            if (op is not ("add" or "replace" or "remove")) { error = Error(StatusCodes.Status400BadRequest, "invalidSyntax", "Only Add, Replace, and Remove are supported."); return false; }
            if (string.IsNullOrWhiteSpace(operation.Path) && operation.Value.ValueKind == JsonValueKind.Object)
            {
                // Entra commonly sends Replace with a pathless object. Expand
                // it into the same validated scalar operations used by the
                // pathful form so the whole request remains atomic.
                foreach (var property in operation.Value.EnumerateObject())
                {
                    if (property.Name.Equals("name", StringComparison.OrdinalIgnoreCase) && property.Value.ValueKind == JsonValueKind.Object)
                    {
                        foreach (var name in property.Value.EnumerateObject())
                        {
                            if (name.Name is not ("givenName" or "familyName"))
                            { error = Error(StatusCodes.Status400BadRequest, "invalidPath", "Only name.givenName and name.familyName are supported."); return false; }
                            if (!TryValidateUserOperations([new ScimPatchOperation(operation.Op, "name." + name.Name, name.Value)], mapping, user, out var nameActions, out error)) return false;
                            actions.AddRange(nameActions!);
                        }
                    }
                    else if (property.Name.Equals("emails", StringComparison.OrdinalIgnoreCase))
                    {
                        var profileRoot = new Dictionary<string, JsonElement> { ["emails"] = property.Value };
                        using var emailDocument = JsonDocument.Parse(JsonSerializer.Serialize(profileRoot, JsonOptions));
                        var profile = ReadProfileFromJson(emailDocument.RootElement, out error);
                        if (error is not null) return false;
                        if (!TryValidateUserOperations([new ScimPatchOperation(operation.Op, "emails.value", JsonSerializer.SerializeToElement(profile.Email, JsonOptions))], mapping, user, out var emailActions, out error)) return false;
                        actions.AddRange(emailActions!);
                    }
                    else
                    {
                        if (!TryValidateUserOperations([new ScimPatchOperation(operation.Op, property.Name, property.Value)], mapping, user, out var propertyActions, out error)) return false;
                        actions.AddRange(propertyActions!);
                    }
                }
                continue;
            }
            if (!TryNormalizeUserPath(operation.Path, operation.Value, op, out var field, out var value, out var remove, out error)) return false;
            if (field is "userName" or "externalId" && remove) { error = Error(StatusCodes.Status400BadRequest, "mutability", $"{field} cannot be removed."); return false; }
            if (field == "userName" && !IsSafeValue(value, 512)) { error = Error(StatusCodes.Status400BadRequest, "invalidValue", "userName is invalid."); return false; }
            if (field == "externalId" && !IsSafeValue(NormalizeExternalId(value), 512)) { error = Error(StatusCodes.Status400BadRequest, "invalidValue", "externalId is invalid."); return false; }
            if (field is "displayName" or "givenName" or "familyName" && value is not null && !IsSafeValue(value, 200)) { error = Error(StatusCodes.Status400BadRequest, "invalidValue", "A name value is invalid."); return false; }
            if (field == "email" && value is not null)
            {
                try { _ = new MailAddress(value); } catch { error = Error(StatusCodes.Status400BadRequest, "invalidValue", "The work email is invalid."); return false; }
            }
            actions.Add(new ScimUserAction(field, value, field == "active" ? ParseBoolean(operation.Value, out var boolean) ? boolean : null : null, remove));
        }
        return true;
    }

    private static bool TryValidateGroupOperations(IReadOnlyCollection<ScimPatchOperation> operations, out List<ScimGroupAction>? actions, out IResult? error)
    {
        actions = []; error = null;
        foreach (var operation in operations)
        {
            var op = operation.Op.Trim().ToLowerInvariant();
            if (op is not ("add" or "replace" or "remove")) { error = Error(StatusCodes.Status400BadRequest, "invalidSyntax", "Only Add, Replace, and Remove are supported."); return false; }
            var path = operation.Path?.Trim();
            if (path is null && operation.Value.ValueKind == JsonValueKind.Object)
            {
                foreach (var property in operation.Value.EnumerateObject())
                {
                    if (property.Name.Equals("members", StringComparison.OrdinalIgnoreCase))
                    {
                        if (!TryReadMemberValues(property.Value, out var ids, out error)) return false;
                        actions.Add(new ScimGroupAction("members", null, ids!, op != "remove", op == "replace", op == "remove"));
                    }
                    else if (property.Name.Equals("displayName", StringComparison.OrdinalIgnoreCase) && TryGetStringValue(property.Value, out var name)) actions.Add(new("displayName", name, [], false, false, false));
                    else if (property.Name.Equals("active", StringComparison.OrdinalIgnoreCase) && ParseBoolean(property.Value, out var active)) actions.Add(new("active", null, [], active, false, false));
                    else { error = Error(StatusCodes.Status400BadRequest, "invalidPath", "The PatchOp path is not supported."); return false; }
                }
                continue;
            }
            if (path is not null && Regex.IsMatch(path, "^members(?:\\[value\\s+eq\\s+\"[^\"]+\"\\])?$", RegexOptions.IgnoreCase | RegexOptions.CultureInvariant))
            {
                if (op == "remove" && path.Equals("members", StringComparison.OrdinalIgnoreCase))
                {
                    actions.Add(new("members", null, [], false, false, true));
                    continue;
                }
                if (op == "remove" && Regex.Match(path, "value\\s+eq\\s+\"([^\"]+)\"", RegexOptions.IgnoreCase).Groups[1].Success)
                    actions.Add(new("members", null, [Regex.Match(path, "value\\s+eq\\s+\"([^\"]+)\"", RegexOptions.IgnoreCase).Groups[1].Value], false, false, false));
                else if (!TryReadMemberValues(operation.Value, out var ids, out error)) return false;
                else actions.Add(new("members", null, ids!, op != "remove", op == "replace", op == "remove"));
                continue;
            }
            if (path is not null && path.Equals("displayName", StringComparison.OrdinalIgnoreCase) && op != "remove" && TryGetStringValue(operation.Value, out var displayName) && IsSafeValue(displayName, 200))
            { actions.Add(new("displayName", displayName, [], false, false, false)); continue; }
            if (path is not null && path.Equals("active", StringComparison.OrdinalIgnoreCase) && op != "remove" && ParseBoolean(operation.Value, out var isActive))
            { actions.Add(new("active", null, [], isActive, false, false)); continue; }
            error = Error(StatusCodes.Status400BadRequest, "invalidPath", "The PatchOp path is not supported."); return false;
        }
        return true;
    }

    private static bool TryNormalizeUserPath(string? rawPath, JsonElement rawValue, string op, out string field, out string? value, out bool remove, out IResult? error)
    {
        field = string.Empty; value = null; remove = op == "remove"; error = null;
        if (string.IsNullOrWhiteSpace(rawPath))
        {
            if (rawValue.ValueKind != JsonValueKind.Object) { error = Error(StatusCodes.Status400BadRequest, "invalidPath", "A path is required for this PatchOp value."); return false; }
            // A pathless object is expanded by the caller's single operation.
            error = Error(StatusCodes.Status400BadRequest, "invalidPath", "Pathless object PatchOp values are not supported."); return false;
        }
        var path = rawPath.Trim();
        var match = Regex.Match(path, "^emails(?:\\[.*?\\])?\\.value$", RegexOptions.IgnoreCase | RegexOptions.CultureInvariant);
        if (match.Success) field = "email";
        else if (path.Equals("userName", StringComparison.OrdinalIgnoreCase)) field = "userName";
        else if (path.Equals("externalId", StringComparison.OrdinalIgnoreCase)) field = "externalId";
        else if (path.Equals("active", StringComparison.OrdinalIgnoreCase)) field = "active";
        else if (path.Equals("displayName", StringComparison.OrdinalIgnoreCase)) field = "displayName";
        else if (path.Equals("name.givenName", StringComparison.OrdinalIgnoreCase)) field = "givenName";
        else if (path.Equals("name.familyName", StringComparison.OrdinalIgnoreCase)) field = "familyName";
        else if (path.Equals("name", StringComparison.OrdinalIgnoreCase))
        {
            if (rawValue.ValueKind != JsonValueKind.Object) { error = Error(StatusCodes.Status400BadRequest, "invalidValue", "name must be an object."); return false; }
            // The compact protocol supports one name component per operation.
            var properties = rawValue.EnumerateObject().ToArray();
            if (properties.Length != 1 || properties[0].Name is not ("givenName" or "familyName")) { error = Error(StatusCodes.Status400BadRequest, "invalidPath", "Only name.givenName and name.familyName are supported."); return false; }
            field = properties[0].Name;
            rawValue = properties[0].Value;
        }
        else { error = Error(StatusCodes.Status400BadRequest, "invalidPath", "The PatchOp path is not supported."); return false; }

        if (remove) return true;
        if (field == "active")
        {
            if (!ParseBoolean(rawValue, out _)) { error = Error(StatusCodes.Status400BadRequest, "invalidValue", "active must be boolean."); return false; }
            return true;
        }
        if (!TryGetStringValue(rawValue, out value)) { error = Error(StatusCodes.Status400BadRequest, "invalidValue", "The PatchOp value must be a string."); return false; }
        return true;
    }

    private static void ApplyUserAction(ApplicationUser user, ScimUserMapping mapping, ScimUserAction action)
    {
        switch (action.Field)
        {
            case "userName": user.UserName = action.Value; user.NormalizedUserName = action.Value?.ToUpperInvariant(); mapping.UserName = action.Value!; break;
            case "externalId": mapping.ExternalId = NormalizeExternalId(action.Value)!; break;
            case "active": mapping.UpstreamActive = action.Remove ? mapping.UpstreamActive : action.BoolValue!.Value; break;
            case "displayName": if (!action.Remove) user.DisplayName = action.Value!; break;
            case "email": user.Email = action.Remove ? null : action.Value; user.NormalizedEmail = action.Remove ? null : action.Value?.ToUpperInvariant(); break;
            case "givenName": UpdateProfile(mapping, givenName: action.Remove ? null : action.Value, setGivenName: true); break;
            case "familyName": UpdateProfile(mapping, familyName: action.Remove ? null : action.Value, setFamilyName: true); break;
        }
    }

    private static void ApplyUserInput(ApplicationUser user, ScimUserMapping mapping, ScimUserInput input, bool replace)
    {
        user.UserName = input.UserName!;
        user.NormalizedUserName = input.UserName!.ToUpperInvariant();
        if (input.Email is not null || replace) { user.Email = input.Email; user.NormalizedEmail = input.Email?.ToUpperInvariant(); }
        if (input.DisplayName is not null) user.DisplayName = input.DisplayName;
        else if (replace && !string.IsNullOrWhiteSpace(input.GivenName ?? input.FamilyName)) user.DisplayName = DisplayNameFor(input);
        mapping.UserName = input.UserName!;
        mapping.ExternalId = NormalizeExternalId(input.ExternalId)!;
        if (input.Active.HasValue) mapping.UpstreamActive = input.Active.Value;
        if (input.GivenName is not null || input.FamilyName is not null)
            UpdateProfile(mapping, input.GivenName, input.FamilyName, input.GivenName is not null, input.FamilyName is not null);
    }

    private static void UpdateProfile(ScimUserMapping mapping, string? givenName = null, string? familyName = null,
        bool setGivenName = false, bool setFamilyName = false)
    {
        var current = ReadProfile(mapping.SourceProfileJson);
        var next = new ScimProfile(setGivenName ? givenName : current.GivenName, setFamilyName ? familyName : current.FamilyName);
        mapping.SourceProfileJson = JsonSerializer.Serialize(next, JsonOptions);
    }

    private static ScimUserMapping NewMapping(Guid connectionId, Guid userId, ScimUserInput input, string resourceId) => new()
    {
        ScimConnectionId = connectionId,
        UserId = userId,
        ResourceId = resourceId,
        ExternalId = NormalizeExternalId(input.ExternalId)!,
        UserName = input.UserName!,
        UpstreamActive = input.Active ?? true,
        SourceProfileJson = JsonSerializer.Serialize(new ScimProfile(input.GivenName, input.FamilyName), JsonOptions),
        LastSynchronizedAt = DateTimeOffset.UtcNow,
        ETag = Guid.NewGuid().ToString("N"),
        CreatedAt = DateTimeOffset.UtcNow,
        UpdatedAt = DateTimeOffset.UtcNow,
    };

    private static void Touch(ScimUserMapping mapping)
    {
        mapping.Version++;
        mapping.ETag = Guid.NewGuid().ToString("N");
        mapping.UpdatedAt = DateTimeOffset.UtcNow;
        mapping.LastSynchronizedAt = mapping.UpdatedAt;
    }

    private async Task WriteAuditAsync(string action, Guid connectionId, ScimUserMapping mapping, object? before, object after, CancellationToken cancellationToken)
    {
        if (AuditFailureInjector?.Invoke() is { } failure) throw failure;
        await auditWriter.WriteAsync(dbContext, httpContextAccessor.HttpContext!, null, mapping.UserId, null, action,
            before ?? new { ScimConnectionId = connectionId, ResourceId = mapping.ResourceId }, after, cancellationToken);
    }

    private async Task WriteGroupAuditAsync(string action, Guid connectionId, AccessGroup group, object? before, object after, CancellationToken cancellationToken)
    {
        if (AuditFailureInjector?.Invoke() is { } failure) throw failure;
        await auditWriter.WriteAsync(dbContext, httpContextAccessor.HttpContext!, null, null, null, action,
            before ?? new { ScimConnectionId = connectionId, ResourceId = group.Id }, after, cancellationToken);
    }

    private async Task<ScimUserAuditFacts> CaptureUserFactsAsync(ScimUserMapping mapping, ApplicationUser user,
        Guid connectionId, CancellationToken cancellationToken)
    {
        var groupResourceIds = await dbContext.AccessGroupMemberships.AsNoTracking()
            .Where(item => item.UserId == user.Id && item.Source == AccessGroupSource.Scim && item.IsUpstreamPresent)
            .Join(dbContext.AccessGroups.AsNoTracking().Where(item => item.ScimConnectionId == connectionId && item.Source == AccessGroupSource.Scim),
                membership => membership.GroupId, group => group.Id, (_, group) => group.Id)
            .OrderBy(id => id)
            .ToArrayAsync(cancellationToken);
        return new(connectionId, mapping.ResourceId, mapping.UpstreamActive, groupResourceIds);
    }

    private async Task<ScimGroupAuditFacts> CaptureGroupFactsAsync(AccessGroup group, CancellationToken cancellationToken)
    {
        var memberResourceIds = await dbContext.AccessGroupMemberships.AsNoTracking()
            .Where(item => item.GroupId == group.Id && item.Source == AccessGroupSource.Scim && item.IsUpstreamPresent)
            .Join(dbContext.ScimUserMappings.AsNoTracking().Where(item => item.ScimConnectionId == group.ScimConnectionId),
                membership => membership.UserId, mapping => mapping.UserId, (_, mapping) => mapping.ResourceId)
            .OrderBy(id => id)
            .ToArrayAsync(cancellationToken);
        return new(group.ScimConnectionId, group.Id, group.IsActive, memberResourceIds);
    }

    private static object UserSchemaDocument() => new
    {
        id = UserSchema,
        name = "User",
        description = "SCIM User",
        attributes = new object[]
        {
            new { name = "userName", type = "string", multiValued = false, required = true, caseExact = true, mutability = "readWrite", returned = "default", uniqueness = "server" },
            new { name = "externalId", type = "string", multiValued = false, required = true, caseExact = true, mutability = "readWrite", returned = "default", uniqueness = "server" },
            new { name = "active", type = "boolean", multiValued = false, required = false, mutability = "readWrite", returned = "default" },
            new { name = "displayName", type = "string", multiValued = false, required = false, caseExact = true, mutability = "readWrite", returned = "default" },
            new { name = "name", type = "complex", multiValued = false, required = false, mutability = "readWrite", returned = "default", subAttributes = new object[] { new { name = "givenName", type = "string", multiValued = false, required = false, mutability = "readWrite", returned = "default" }, new { name = "familyName", type = "string", multiValued = false, required = false, mutability = "readWrite", returned = "default" } } },
            new { name = "emails", type = "complex", multiValued = true, required = false, mutability = "readWrite", returned = "default", subAttributes = new object[] { new { name = "value", type = "string", multiValued = false, required = false, mutability = "readWrite", returned = "default" }, new { name = "type", type = "string", multiValued = false, required = false, mutability = "readWrite", returned = "default" }, new { name = "primary", type = "boolean", multiValued = false, required = false, mutability = "readWrite", returned = "default" } } },
        },
    };

    private static object GroupSchemaDocument() => new
    {
        id = GroupSchema,
        name = "Group",
        description = "SCIM Group",
        attributes = new object[]
        {
            new { name = "externalId", type = "string", multiValued = false, required = false, caseExact = true, mutability = "readWrite", returned = "default", uniqueness = "server" },
            new { name = "displayName", type = "string", multiValued = false, required = true, caseExact = true, mutability = "readWrite", returned = "default", uniqueness = "server" },
            new { name = "active", type = "boolean", multiValued = false, required = false, mutability = "readWrite", returned = "default" },
            new { name = "members", type = "complex", multiValued = true, required = false, mutability = "readWrite", returned = "default", subAttributes = new object[] { new { name = "value", type = "string", multiValued = false, required = true, mutability = "readWrite", returned = "default" }, new { name = "$ref", type = "reference", multiValued = false, required = false, mutability = "readOnly", returned = "default" }, new { name = "type", type = "string", multiValued = false, required = false, mutability = "readWrite", returned = "default" } } },
        },
    };

    private static object Meta(string resourceType, string id, string version, DateTimeOffset created, DateTimeOffset modified) => new
    {
        resourceType,
        created = created.ToUniversalTime().ToString("O", CultureInfo.InvariantCulture),
        lastModified = modified.ToUniversalTime().ToString("O", CultureInfo.InvariantCulture),
        location = resourceType == "User" ? UserLocation(id) : GroupLocation(Guid.Parse(id)),
        version,
    };

    private static bool TryReadPaging(HttpRequest request, out int startIndex, out int count, out IResult? error)
    {
        startIndex = ParseQueryInt(request.Query["startIndex"], 1);
        count = ParseQueryInt(request.Query["count"], MaximumPageSize);
        error = null;
        if (startIndex < 1 || count < 0 || count > MaximumPageSize || !int.TryParse(request.Query["startIndex"], out _) && request.Query.ContainsKey("startIndex") || !int.TryParse(request.Query["count"], out _) && request.Query.ContainsKey("count"))
        { error = Error(StatusCodes.Status400BadRequest, "invalidValue", "startIndex and count are invalid."); return false; }
        return true;
    }

    private static bool TryReadFilter(string? raw, IReadOnlyCollection<string> allowed, out List<ScimFilter>? filters, out IResult? error)
    {
        filters = null; error = null;
        if (string.IsNullOrWhiteSpace(raw)) return true;
        if (raw.Length > MaximumFilterLength) { error = Error(StatusCodes.Status400BadRequest, "invalidFilter", "The filter is too long."); return false; }
        var matches = Regex.Matches(raw, "^\\s*([A-Za-z][A-Za-z0-9]*)\\s+eq\\s+(\"(?:\\\\.|[^\"\\\\])*\")(?:\\s+and\\s+([A-Za-z][A-Za-z0-9]*)\\s+eq\\s+(\"(?:\\\\.|[^\"\\\\])*\"))*\\s*$", RegexOptions.IgnoreCase | RegexOptions.CultureInvariant);
        if (matches.Count != 1) { error = Error(StatusCodes.Status400BadRequest, "invalidFilter", "Only field eq \"value\" filters joined by and are supported."); return false; }
        var match = matches[0];
        filters = [];
        var field = match.Groups[1].Value;
        var first = allowed.FirstOrDefault(item => item.Equals(field, StringComparison.OrdinalIgnoreCase));
        if (first is null || !TryDecodeQuoted(match.Groups[2].Value, out var firstValue)) { error = Error(StatusCodes.Status400BadRequest, "invalidFilter", "The filter field or value is invalid."); return false; }
        filters.Add(new(first, firstValue!));
        if (match.Groups[3].Success)
        {
            var second = allowed.FirstOrDefault(item => item.Equals(match.Groups[3].Value, StringComparison.OrdinalIgnoreCase));
            if (second is null || !TryDecodeQuoted(match.Groups[4].Value, out var secondValue)) { error = Error(StatusCodes.Status400BadRequest, "invalidFilter", "The filter field or value is invalid."); return false; }
            filters.Add(new(second, secondValue!));
        }
        return true;
    }

    private static bool TryReadSchemas(JsonElement root, string expected, out IResult? error)
    {
        error = null;
        if (!TryGet(root, "schemas", out var schemas)) return true; // tolerant of older Entra payloads
        if (schemas.ValueKind != JsonValueKind.Array || !schemas.EnumerateArray().All(item => item.ValueKind == JsonValueKind.String))
        { error = Error(StatusCodes.Status400BadRequest, "invalidValue", "schemas must be an array of strings."); return false; }
        if (!schemas.EnumerateArray().Any(item => string.Equals(item.GetString(), expected, StringComparison.Ordinal)))
        { error = Error(StatusCodes.Status400BadRequest, "invalidValue", "The resource schema is not supported."); return false; }
        return true;
    }

    private static ScimProfile ReadProfileFromJson(JsonElement root, out IResult? error)
    {
        error = null;
        var profile = new ScimProfile(null, null);
        if (TryGet(root, "name", out var name))
        {
            if (name.ValueKind != JsonValueKind.Object) { error = Error(StatusCodes.Status400BadRequest, "invalidValue", "name must be an object."); return profile; }
            profile = new(ReadString(name, "givenName")?.Trim(), ReadString(name, "familyName")?.Trim());
        }
        if (TryGet(root, "emails", out var emails))
        {
            if (emails.ValueKind != JsonValueKind.Array) { error = Error(StatusCodes.Status400BadRequest, "invalidValue", "emails must be an array."); return profile; }
            var candidates = emails.EnumerateArray().Where(item => item.ValueKind == JsonValueKind.Object &&
                string.Equals(ReadString(item, "type"), "work", StringComparison.OrdinalIgnoreCase) &&
                (!TryGet(item, "primary", out var primary) || primary.ValueKind == JsonValueKind.True)).ToArray();
            if (candidates.Length > 1) { error = Error(StatusCodes.Status400BadRequest, "invalidValue", "Only one primary work email is supported."); return profile; }
            if (candidates.Length == 1) profile.Email = ReadString(candidates[0], "value")?.Trim();
        }
        return profile;
    }

    private static ScimProfile ReadProfile(string? json)
    {
        if (string.IsNullOrWhiteSpace(json)) return new(null, null);
        try { return JsonSerializer.Deserialize<ScimProfile>(json, JsonOptions) ?? new(null, null); }
        catch { return new(null, null); }
    }

    private static bool TryReadMemberValues(JsonElement value, out List<string>? ids, out IResult? error)
    {
        ids = []; error = null;
        if (value.ValueKind != JsonValueKind.Array) { error = Error(StatusCodes.Status400BadRequest, "invalidValue", "members PatchOp values must be an array."); return false; }
        foreach (var item in value.EnumerateArray())
        {
            var id = item.ValueKind == JsonValueKind.String ? item.GetString() : ReadString(item, "value");
            if (string.IsNullOrWhiteSpace(id)) { error = Error(StatusCodes.Status400BadRequest, "invalidValue", "A group member id is required."); return false; }
            ids.Add(id!);
        }
        return true;
    }

    private static bool TryGetStringValue(JsonElement value, out string? result)
    {
        result = value.ValueKind == JsonValueKind.String ? value.GetString() : null;
        return result is not null;
    }

    private static bool ParseBoolean(JsonElement value, out bool result)
    {
        result = false;
        if (value.ValueKind is not (JsonValueKind.True or JsonValueKind.False)) return false;
        result = value.GetBoolean(); return true;
    }

    private static bool TryGet(JsonElement element, string name, out JsonElement value)
    {
        if (element.ValueKind == JsonValueKind.Object)
            foreach (var property in element.EnumerateObject()) if (property.Name.Equals(name, StringComparison.OrdinalIgnoreCase)) { value = property.Value; return true; }
        value = default; return false;
    }

    private static string? ReadString(JsonElement element, string name) => TryGet(element, name, out var value) && value.ValueKind == JsonValueKind.String ? value.GetString() : null;
    private static bool IsSafeValue(string? value, int max) => !string.IsNullOrWhiteSpace(value) && value!.Length <= max && !value.Any(char.IsControl);
    private static string? NormalizeExternalId(string? value) => Guid.TryParse(value, out var id) ? id.ToString("D") : value?.Trim();
    private static string DisplayNameFor(ScimUserInput input)
    {
        var value = input.DisplayName;
        if (string.IsNullOrWhiteSpace(value))
            value = string.Join(" ", new[] { input.GivenName, input.FamilyName }.Where(item => !string.IsNullOrWhiteSpace(item)));
        if (string.IsNullOrWhiteSpace(value)) value = input.UserName;
        value = value!.Trim();
        return value[..Math.Min(200, value.Length)];
    }
    private static int ParseQueryInt(string? value, int fallback) => int.TryParse(value, NumberStyles.None, CultureInfo.InvariantCulture, out var result) ? result : fallback;
    private static bool IsMembersExcluded(HttpRequest request) => request.Query["excludedAttributes"].ToString().Split(',', StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries).Any(item => item.Equals("members", StringComparison.OrdinalIgnoreCase));
    private static string UserLocation(string id) => BasePath + "/Users/" + Uri.EscapeDataString(id);
    private static string GroupLocation(Guid id) => BasePath + "/Groups/" + id.ToString("D");
    private static bool TryDecodeQuoted(string value, out string? decoded) { try { decoded = JsonSerializer.Deserialize<string>(value); return decoded is not null; } catch { decoded = null; return false; } }
    private static bool IsUniqueViolation(Exception exception) => exception.ToString().Contains("23505", StringComparison.Ordinal);
    private static IResult? CheckPrecondition(HttpRequest request, string actual)
    {
        var value = request.Headers.IfMatch.ToString();
        if (!string.IsNullOrWhiteSpace(value) && value != "*" && !value.Split(',').Select(item => item.Trim().Trim('"')).Contains(actual, StringComparer.Ordinal))
            return Error(StatusCodes.Status412PreconditionFailed, "invalidVers", "The resource version is stale.");
        return null;
    }
    private static IResult? CheckMetaVersion(JsonElement body, string actual)
    {
        if (body.ValueKind == JsonValueKind.Object && TryGet(body, "meta", out var meta) &&
            TryGet(meta, "version", out var version) && version.ValueKind == JsonValueKind.String &&
            !string.Equals(version.GetString(), actual, StringComparison.Ordinal))
            return Error(StatusCodes.Status412PreconditionFailed, "invalidVers", "The SCIM resource version is stale.");
        return null;
    }
    private static IResult? CheckMetaVersion(HttpRequest request, string actual) =>
        request.Headers.TryGetValue("X-SCIM-Meta-Version", out var value) && !string.Equals(value, actual, StringComparison.Ordinal)
            ? Error(StatusCodes.Status412PreconditionFailed, "invalidVers", "The SCIM resource version is stale.") : null;
    private async Task<string> CurrentUserEtagAsync(Guid connectionId, string resourceId, CancellationToken cancellationToken) =>
        await dbContext.ScimUserMappings.Where(item => item.ScimConnectionId == connectionId && item.ResourceId == resourceId)
            .Select(item => item.ETag).SingleAsync(cancellationToken);
    private void SetResponseEtag(string value) => httpContextAccessor.HttpContext!.Response.Headers.ETag = '"' + value + '"';
    public static IResult ConcurrencyError() => Error(StatusCodes.Status412PreconditionFailed,
        "invalidVers", "The SCIM resource version is stale.");
    private static IResult Ok(object value) => TypedResults.Json(value, contentType: ScimMediaType, statusCode: StatusCodes.Status200OK);
    private IResult Created(string location, object value)
    {
        httpContextAccessor.HttpContext!.Response.Headers.Location = location;
        return TypedResults.Json(value, contentType: ScimMediaType, statusCode: StatusCodes.Status201Created);
    }
    private static IResult NoContent() => TypedResults.StatusCode(StatusCodes.Status204NoContent);
    private static IResult Unauthorized() => TypedResults.Json(new { schemas = new[] { "urn:ietf:params:scim:api:messages:2.0:Error" }, status = "401", scimType = "invalidValue", detail = "A valid SCIM bearer token is required." }, contentType: ScimMediaType, statusCode: StatusCodes.Status401Unauthorized);
    private static IResult Error(int status, string? scimType, string detail) => TypedResults.Json(new { schemas = new[] { "urn:ietf:params:scim:api:messages:2.0:Error" }, status = status.ToString(CultureInfo.InvariantCulture), scimType, detail }, contentType: ScimMediaType, statusCode: status);
    private static IResult UserManagerFailure(IdentityResult result) => Error(StatusCodes.Status400BadRequest, "invalidValue", string.Join("; ", result.Errors.Select(item => item.Code)));

    private sealed record ScimUserAuditFacts(Guid ConnectionId, string ResourceId, bool Active,
        IReadOnlyCollection<Guid> UpstreamGroupResourceIds);
    private sealed record ScimGroupAuditFacts(Guid? ConnectionId, Guid ResourceId, bool Active,
        IReadOnlyCollection<string> UpstreamMemberResourceIds);

    private sealed record ScimProfile(string? GivenName, string? FamilyName) { public string? Email { get; set; } }
    private sealed class ScimUserInput { public string? UserName { get; set; } public string? ExternalId { get; set; } public bool? Active { get; set; } public string? DisplayName { get; set; } public string? GivenName { get; set; } public string? FamilyName { get; set; } public string? Email { get; set; } }
    private sealed record ScimGroupInput(string? DisplayName, string? ExternalId, bool Active, List<string> MemberIds);
    private sealed record ScimPatchOperation(string Op, string? Path, JsonElement Value);
    private sealed record ScimUserAction(string Field, string? Value, bool? BoolValue, bool Remove);
    private sealed record ScimGroupAction(string Field, string? StringValue, List<string> MemberIds, bool AddMembers, bool ReplaceMembers, bool RemoveAllMembers);
    private sealed record ScimFilter(string Field, string Value);
}