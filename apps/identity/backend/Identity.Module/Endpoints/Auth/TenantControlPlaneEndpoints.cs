using System.Security.Claims;
using System.Text.Json.Serialization;

using Microsoft.AspNetCore.Identity;
using Microsoft.AspNetCore.Mvc;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Options;

using Npgsql;

using Vantigo.Configuration;
using Vantigo.Contracts.Identity;
using Vantigo.Identity.Authorization;
using Vantigo.Identity.Database.Accounts;
using Vantigo.Identity.Services;

namespace Vantigo.Identity.Endpoints.Auth;

/// <summary>System-admin-only tenant control-plane API.</summary>
internal static class TenantControlPlaneEndpoints
{
    internal static void Map(IEndpointRouteBuilder app)
    {
        var tenants = app.MapGroup("/api/v1/identity/admin/tenants")
            .WithTags("Identity tenant control plane")
            .RequireAuthorization(AuthPolicies.SystemAdmin);
        tenants.AddEndpointFilter(MultiTenantOnly);

        tenants.MapGet("", List);
        tenants.MapGet("/{id:guid}", Get);
        tenants.MapPost("", Create);
        tenants.MapPut("/{id:guid}", Update);
        tenants.MapPost("/{id:guid}/suspend", Suspend);
        tenants.MapPost("/{id:guid}/reactivate", Reactivate);
    }

    private static ValueTask<object?> MultiTenantOnly(
        EndpointFilterInvocationContext context,
        EndpointFilterDelegate next)
    {
        var options = context.HttpContext.RequestServices.GetRequiredService<IOptions<TenancyOptions>>();
        return options.Value.IsMultiTenant
            ? next(context)
            : new ValueTask<object?>(Error(StatusCodes.Status409Conflict, "multi_tenant_required",
                "Tenant control-plane endpoints are available only in multi-tenant mode."));
    }

    private static async Task<IResult> List(
        AccountsDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var tenants = await dbContext.Tenants.AsNoTracking()
            .OrderBy(tenant => tenant.Name).ThenBy(tenant => tenant.Id)
            .Select(tenant => new TenantControlPlaneResponse(
                tenant.Id,
                tenant.Name,
                tenant.Slug,
                tenant.Status.ToString(),
                tenant.EnabledModules,
                dbContext.TenantMemberships.Count(membership => membership.TenantId == tenant.Id)))
            .ToListAsync(cancellationToken);
        return TypedResults.Ok(tenants);
    }

    private static async Task<IResult> Get(
        Guid id,
        AccountsDbContext dbContext,
        CancellationToken cancellationToken)
    {
        var tenant = await FindTenant(dbContext, id, cancellationToken);
        return tenant is null ? TypedResults.NotFound() : TypedResults.Ok(tenant);
    }

    private static async Task<IResult> Create(
        [FromBody] TenantCreateRequest? request,
        ClaimsPrincipal principal,
        HttpContext httpContext,
        AccountsDbContext dbContext,
        UserManager<ApplicationUser> userManager,
        AuthorizationAuditWriter auditWriter,
        IOptions<VantigoAuthenticationOptions> authenticationOptions,
        CancellationToken cancellationToken)
    {
        var validation = ValidateCreate(request);
        if (validation is not null) return validation;

        var actor = await userManager.GetUserAsync(principal);
        if (actor is null) return Error(StatusCodes.Status401Unauthorized, "unauthenticated", "Authentication is required.");

        var slug = TenantSlug.Normalize(request!.Slug);
        var name = request.Name!.Trim();
        var modules = NormalizeModules(request.EnabledModules!, out var moduleError);
        if (modules is null) return Error(400, "invalid_modules", moduleError!);
        var adminUserId = request.AdminUserId.GetValueOrDefault();
        if (adminUserId == Guid.Empty) adminUserId = actor.Id;
        var adminEmail = string.IsNullOrWhiteSpace(request.AdminEmail) ? null : request.AdminEmail.Trim();
        var normalizedAdminEmail = adminEmail is null ? null : NormalizeEmail(adminEmail);

        Tenant? tenant;
        Invitation? seededInvitation = null;
        string? seededInvitationToken = null;
        var created = false;
        var changed = false;

        try
        {
            await using var transaction = await dbContext.Database.BeginTransactionAsync(
                System.Data.IsolationLevel.Serializable, cancellationToken);

            tenant = await dbContext.Tenants.SingleOrDefaultAsync(item => item.Slug == slug, cancellationToken);
            if (tenant is null)
            {
                tenant = new Tenant
                {
                    Name = name,
                    Slug = slug!,
                    Status = TenantStatus.Active,
                    EnabledModules = modules,
                    CreatedAtUtc = DateTimeOffset.UtcNow,
                };
                dbContext.Tenants.Add(tenant);
                created = true;
                changed = true;
            }
            else if (!string.Equals(tenant.Name, name, StringComparison.Ordinal) ||
                     !tenant.EnabledModules.SequenceEqual(modules, StringComparer.Ordinal))
            {
                return Conflict("tenant_exists", "A tenant with this slug already exists with different provisioning data.");
            }

            if (adminEmail is null)
            {
                if (!await dbContext.Users.AnyAsync(user => user.Id == adminUserId, cancellationToken))
                    return Error(400, "invalid_admin", "The designated admin account does not exist.");

                if (!await dbContext.UserRoles
                        .Join(dbContext.Roles, assignment => assignment.RoleId, role => role.Id,
                            (_, role) => role)
                        .AnyAsync(role => role.Name == AuthRoles.Owner &&
                            dbContext.UserRoles.Any(assignment => assignment.UserId == adminUserId && assignment.RoleId == role.Id),
                            cancellationToken))
                    return Error(400, "invalid_admin", "The designated account must have the global Owner role.");

                if (!await dbContext.TenantMemberships.AnyAsync(membership =>
                        membership.UserId == adminUserId && membership.TenantId == tenant.Id, cancellationToken))
                {
                    dbContext.TenantMemberships.Add(new TenantMembership
                    {
                        UserId = adminUserId,
                        TenantId = tenant.Id,
                        CreatedAtUtc = DateTimeOffset.UtcNow,
                    });
                    changed = true;
                }
            }
            else
            {
                if (await dbContext.Users.AnyAsync(user => user.NormalizedEmail == NormalizeEmail(adminEmail), cancellationToken))
                    return Error(409, "account_exists", "An account already exists for the designated admin email; use adminUserId instead.");

                seededInvitation = await dbContext.Invitations.SingleOrDefaultAsync(invitation =>
                    invitation.TenantId == tenant.Id && invitation.NormalizedEmail == normalizedAdminEmail &&
                    invitation.AcceptedAt == null && invitation.RevokedAt == null && invitation.ExpiresAt > DateTimeOffset.UtcNow,
                    cancellationToken);
                if (seededInvitation is null)
                {
                    if (await dbContext.Invitations.AnyAsync(invitation =>
                            invitation.NormalizedEmail == normalizedAdminEmail && invitation.AcceptedAt == null && invitation.RevokedAt == null,
                            cancellationToken))
                        return Conflict("invitation_exists", "An active invitation already exists for this email address.");

                    var token = InvitationTokenService.Create();
                    seededInvitationToken = token.RawToken;
                    seededInvitation = new Invitation
                    {
                        TenantId = tenant.Id,
                        Email = adminEmail,
                        NormalizedEmail = normalizedAdminEmail!,
                        Role = AuthRoles.Owner,
                        DisplayName = string.IsNullOrWhiteSpace(request.AdminDisplayName) ? null : request.AdminDisplayName.Trim(),
                        TokenHash = token.Hash,
                        CreatedAt = DateTimeOffset.UtcNow,
                        ExpiresAt = DateTimeOffset.UtcNow.Add(InvitationTokenService.GetLifetime(authenticationOptions.Value.Invitations)),
                        InvitedByUserId = actor.Id,
                    };
                    dbContext.Invitations.Add(seededInvitation);
                    changed = true;
                }
            }

            if (changed)
            {
                await auditWriter.WriteAsync(dbContext, httpContext, actor.Id, adminEmail is null ? adminUserId : null, null,
                    created ? "tenant.created" : "tenant.provisioning-reconciled",
                    new { Tenant = (object?)null }, new
                    {
                        tenant.Id,
                        tenant.Name,
                        tenant.Slug,
                        Status = tenant.Status.ToString(),
                        tenant.EnabledModules,
                        AdminUserId = adminEmail is null ? adminUserId : (Guid?)null,
                        AdminInvitationId = seededInvitation?.Id,
                    }, cancellationToken);
            }

            await transaction.CommitAsync(cancellationToken);
        }
        catch (DbUpdateException exception) when (IsTenantConflict(exception))
        {
            return Conflict("tenant_conflict", "The tenant conflicts with another control-plane change.");
        }

        var response = await FindTenant(dbContext, tenant!.Id, cancellationToken);
        if (response is null) return Error(500, "tenant_provisioning_failed", "The tenant could not be loaded after provisioning.");
        var withInvitation = response with
        {
            SeededAdminInvitationId = seededInvitation?.Id,
            SeededAdminInvitationToken = seededInvitationToken,
        };
        return created
            ? TypedResults.Created($"/api/v1/identity/admin/tenants/{tenant.Id}", withInvitation)
            : TypedResults.Ok(withInvitation);
    }

    private static async Task<IResult> Update(
        Guid id,
        [FromBody] TenantUpdateRequest? request,
        ClaimsPrincipal principal,
        HttpContext httpContext,
        AccountsDbContext dbContext,
        UserManager<ApplicationUser> userManager,
        AuthorizationAuditWriter auditWriter,
        CancellationToken cancellationToken)
    {
        var validation = ValidateUpdate(request);
        if (validation is not null) return validation;
        var actor = await userManager.GetUserAsync(principal);
        if (actor is null) return Error(401, "unauthenticated", "Authentication is required.");

        string? moduleError = null;
        string[]? modules = null;
        if (request!.EnabledModules is not null)
            modules = NormalizeModules(request.EnabledModules, out moduleError);
        if (request.EnabledModules is not null && modules is null)
            return Error(400, "invalid_modules", moduleError!);
        var normalizedSlug = request.Slug is null ? null : TenantSlug.Normalize(request.Slug);
        if (request.Slug is not null && normalizedSlug is null)
            return Error(400, "invalid_slug", "Slug must be a lowercase URL-safe tenant slug.");
        var status = request.Status is null ? null : ParseStatus(request.Status);
        if (request.Status is not null && status is null)
            return Error(400, "invalid_status", "Status must be Active or Suspended.");

        await using var transaction = await dbContext.Database.BeginTransactionAsync(
            System.Data.IsolationLevel.Serializable, cancellationToken);
        var tenant = await dbContext.Tenants.SingleOrDefaultAsync(item => item.Id == id, cancellationToken);
        if (tenant is null) return TypedResults.NotFound();
        if (normalizedSlug is not null && await dbContext.Tenants.AnyAsync(item => item.Id != id && item.Slug == normalizedSlug, cancellationToken))
            return Conflict("slug_exists", "The tenant slug is already in use.");

        var before = new { tenant.Name, tenant.Slug, Status = tenant.Status.ToString(), tenant.EnabledModules };
        if (request.Name is not null) tenant.Name = request.Name.Trim();
        if (normalizedSlug is not null) tenant.Slug = normalizedSlug;
        if (status is TenantStatus requestedStatus) tenant.Status = requestedStatus;
        if (modules is not null) tenant.EnabledModules = modules;
        var after = new { tenant.Name, tenant.Slug, Status = tenant.Status.ToString(), tenant.EnabledModules };

        await auditWriter.WriteAsync(dbContext, httpContext, actor.Id, null, null, "tenant.updated", before, after, cancellationToken);
        await transaction.CommitAsync(cancellationToken);
        return TypedResults.Ok(await FindTenant(dbContext, id, cancellationToken));
    }

    private static Task<IResult> Suspend(
        Guid id,
        ClaimsPrincipal principal,
        HttpContext httpContext,
        AccountsDbContext dbContext,
        UserManager<ApplicationUser> userManager,
        AuthorizationAuditWriter auditWriter,
        CancellationToken cancellationToken) => SetStatus(id, TenantStatus.Suspended, principal, httpContext, dbContext, userManager, auditWriter, cancellationToken);

    private static Task<IResult> Reactivate(
        Guid id,
        ClaimsPrincipal principal,
        HttpContext httpContext,
        AccountsDbContext dbContext,
        UserManager<ApplicationUser> userManager,
        AuthorizationAuditWriter auditWriter,
        CancellationToken cancellationToken) => SetStatus(id, TenantStatus.Active, principal, httpContext, dbContext, userManager, auditWriter, cancellationToken);

    private static async Task<IResult> SetStatus(
        Guid id,
        TenantStatus status,
        ClaimsPrincipal principal,
        HttpContext httpContext,
        AccountsDbContext dbContext,
        UserManager<ApplicationUser> userManager,
        AuthorizationAuditWriter auditWriter,
        CancellationToken cancellationToken)
    {
        var actor = await userManager.GetUserAsync(principal);
        if (actor is null) return Error(401, "unauthenticated", "Authentication is required.");
        await using var transaction = await dbContext.Database.BeginTransactionAsync(cancellationToken);
        var tenant = await dbContext.Tenants.SingleOrDefaultAsync(item => item.Id == id, cancellationToken);
        if (tenant is null) return TypedResults.NotFound();
        var before = tenant.Status.ToString();
        tenant.Status = status;
        await auditWriter.WriteAsync(dbContext, httpContext, actor.Id, null, null, status == TenantStatus.Suspended ? "tenant.suspended" : "tenant.reactivated",
            new { Status = before }, new { Status = status.ToString(), tenant.Id }, cancellationToken);
        await transaction.CommitAsync(cancellationToken);
        return TypedResults.Ok(await FindTenant(dbContext, id, cancellationToken));
    }

    private static async Task<TenantControlPlaneResponse?> FindTenant(
        AccountsDbContext dbContext,
        Guid id,
        CancellationToken cancellationToken) =>
        await dbContext.Tenants.AsNoTracking()
            .Where(tenant => tenant.Id == id)
            .Select(tenant => new TenantControlPlaneResponse(
                tenant.Id,
                tenant.Name,
                tenant.Slug,
                tenant.Status.ToString(),
                tenant.EnabledModules,
                dbContext.TenantMemberships.Count(membership => membership.TenantId == tenant.Id)))
            .SingleOrDefaultAsync(cancellationToken);

    private static IResult? ValidateCreate(TenantCreateRequest? request)
    {
        if (request is null) return Error(400, "invalid_request", "A tenant name, slug, and module set are required.");
        if (string.IsNullOrWhiteSpace(request.Name) || request.Name.Trim().Length > 200 || request.Name.Any(char.IsControl))
            return Error(400, "invalid_name", "Tenant name is required and must be at most 200 characters.");
        if (TenantSlug.Normalize(request.Slug) is null)
            return Error(400, "invalid_slug", "Slug must be a lowercase URL-safe tenant slug.");
        if (request.EnabledModules is null)
            return Error(400, "invalid_modules", "Enabled module keys are required.");
        if (request.AdminUserId is Guid userId && userId == Guid.Empty)
            return Error(400, "invalid_admin", "The designated admin account id is invalid.");
        if (!string.IsNullOrWhiteSpace(request.AdminEmail) && request.AdminUserId.HasValue)
            return Error(400, "invalid_admin", "Specify either adminEmail or adminUserId, not both.");
        if (!string.IsNullOrWhiteSpace(request.AdminEmail) && !IsEmail(request.AdminEmail))
            return Error(400, "invalid_admin_email", "A valid designated admin email is required.");
        return null;
    }

    private static IResult? ValidateUpdate(TenantUpdateRequest? request)
    {
        if (request is null) return Error(400, "invalid_request", "A tenant update is required.");
        if (request.Name is not null && (string.IsNullOrWhiteSpace(request.Name) || request.Name.Trim().Length > 200 || request.Name.Any(char.IsControl)))
            return Error(400, "invalid_name", "Tenant name must be at most 200 characters.");
        return null;
    }

    private static string[]? NormalizeModules(IReadOnlyCollection<string> modules, out string? error)
    {
        if (TenantModuleCatalog.TryNormalize(modules, out var normalized, out var invalidKey))
        {
            error = null;
            return normalized;
        }

        error = $"Unknown tenant module key '{invalidKey}'. Allowed keys: {string.Join(", ", TenantModuleCatalog.KnownModuleKeys.OrderBy(key => key, StringComparer.Ordinal))}.";
        return null;
    }

    private static TenantStatus? ParseStatus(string status) =>
        Enum.TryParse<TenantStatus>(status.Trim(), true, out var value) ? value : null;

    private static string? NormalizeDomain(string? value)
    {
        if (string.IsNullOrWhiteSpace(value)) return null;
        var domain = value.Trim().ToLowerInvariant();
        if (domain.Length > 255 || domain.Contains('@') || domain.StartsWith('.') || domain.EndsWith('.') ||
            domain.Contains("..", StringComparison.Ordinal) ||
            domain.Split('.').Any(label => label.Length is 0 or > 63 || label[0] == '-' || label[^1] == '-' ||
                label.Any(character => !(char.IsAsciiLetterOrDigit(character) || character == '-'))))
            return null;
        return domain;
    }

    private static bool IsEmail(string value) =>
        value.Trim().Length <= 256 &&
        value.Count(character => character == '@') == 1 &&
        NormalizeDomain(value[(value.LastIndexOf('@') + 1)..]) is not null;

    private static string NormalizeEmail(string value) => value.Trim().ToUpperInvariant();

    private static bool IsTenantConflict(Exception exception)
    {
        for (var current = exception; current is not null; current = current.InnerException)
        {
            if (current is PostgresException postgres && postgres.SqlState is PostgresErrorCodes.UniqueViolation or PostgresErrorCodes.SerializationFailure or PostgresErrorCodes.DeadlockDetected)
                return true;
        }
        return false;
    }

    private static IResult Conflict(string code, string message) => Error(StatusCodes.Status409Conflict, code, message);

    private static IResult Error(int statusCode, string code, string message) =>
        TypedResults.Json(new { code, message }, statusCode: statusCode);
}

internal sealed record TenantCreateRequest(
    string? Name,
    string? Slug,
    IReadOnlyCollection<string>? EnabledModules,
    Guid? AdminUserId = null,
    string? AdminEmail = null,
    string? AdminDisplayName = null);

internal sealed record TenantUpdateRequest(
    string? Name,
    string? Slug,
    string? Status,
    IReadOnlyCollection<string>? EnabledModules);

internal sealed record TenantControlPlaneResponse(
    Guid Id,
    string Name,
    string Slug,
    string Status,
    IReadOnlyCollection<string> EnabledModules,
    int MembershipsCount,
    Guid? SeededAdminInvitationId = null,
    string? SeededAdminInvitationToken = null);