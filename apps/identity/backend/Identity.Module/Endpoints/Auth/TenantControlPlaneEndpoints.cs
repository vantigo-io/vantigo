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
        tenants.MapGet("/{id:guid}/sso", GetSso);
        tenants.MapPut("/{id:guid}/sso", UpsertSso);
        tenants.MapGet("/{id:guid}/offboarding", OffboardingStatus);
        tenants.MapPost("/{id:guid}/offboarding/export", RequestExport);
        tenants.MapPost("/{id:guid}/offboarding/purge", RequestPurge);
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
                dbContext.TenantMemberships.Count(membership => membership.TenantId == tenant.Id),
                dbContext.TenantSsoConfigurations.Any(configuration => configuration.TenantId == tenant.Id)))
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
        var sso = request.Sso is null
            ? (Configuration: (TenantSsoConfiguration?)null, Error: (IResult?)null)
            : ValidateSso(request.Sso);
        if (sso.Error is not null) return sso.Error;

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

            if (sso.Configuration is not null)
            {
                var duplicateSso = await dbContext.TenantSsoConfigurations.AnyAsync(configuration =>
                    configuration.TenantId != tenant.Id && configuration.EntraTenantId == sso.Configuration.EntraTenantId,
                    cancellationToken);
                if (duplicateSso)
                    return Conflict("entra_tenant_exists", "The Entra tenant is already assigned to another tenant.");

                var currentSso = await dbContext.TenantSsoConfigurations
                    .SingleOrDefaultAsync(configuration => configuration.TenantId == tenant.Id, cancellationToken);
                if (currentSso is null)
                {
                    sso.Configuration.TenantId = tenant.Id;
                    dbContext.TenantSsoConfigurations.Add(sso.Configuration);
                    changed = true;
                }
                else if (!SameSso(currentSso, sso.Configuration))
                {
                    return Conflict("tenant_exists", "The tenant already has different SSO configuration.");
                }
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
                        SsoConfigured = sso.Configuration is not null,
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

    private static async Task<IResult> GetSso(
        Guid id,
        AccountsDbContext dbContext,
        CancellationToken cancellationToken)
    {
        if (!await dbContext.Tenants.AsNoTracking().AnyAsync(tenant => tenant.Id == id, cancellationToken))
            return TypedResults.NotFound();
        var configuration = await dbContext.TenantSsoConfigurations.AsNoTracking()
            .SingleOrDefaultAsync(item => item.TenantId == id, cancellationToken);
        return configuration is null
            ? TypedResults.NotFound()
            : TypedResults.Ok(ToSsoResponse(configuration));
    }

    private static async Task<IResult> UpsertSso(
        Guid id,
        [FromBody] TenantSsoRequest? request,
        ClaimsPrincipal principal,
        HttpContext httpContext,
        AccountsDbContext dbContext,
        UserManager<ApplicationUser> userManager,
        AuthorizationAuditWriter auditWriter,
        CancellationToken cancellationToken)
    {
        var validation = ValidateSso(request);
        if (validation.Error is not null) return validation.Error;
        var actor = await userManager.GetUserAsync(principal);
        if (actor is null) return Error(401, "unauthenticated", "Authentication is required.");

        await using var transaction = await dbContext.Database.BeginTransactionAsync(
            System.Data.IsolationLevel.Serializable, cancellationToken);
        var tenant = await dbContext.Tenants.SingleOrDefaultAsync(item => item.Id == id, cancellationToken);
        if (tenant is null) return TypedResults.NotFound();

        // The uniqueness check is global by design: one Entra organization maps
        // to one Vantigo tenant. Serializable transactions make the application
        // invariant race-safe even though Phase 1 did not add a database index.
        var configuration = await dbContext.TenantSsoConfigurations
            .SingleOrDefaultAsync(item => item.TenantId == id, cancellationToken);
        var requestedConfiguration = validation.Configuration!;
        if (await dbContext.TenantSsoConfigurations.AnyAsync(item =>
                item.TenantId != id && item.EntraTenantId == requestedConfiguration.EntraTenantId, cancellationToken))
            return Conflict("entra_tenant_exists", "The Entra tenant is already assigned to another tenant.");

        var before = configuration is null ? null : ToSsoResponse(configuration);
        if (configuration is null)
        {
            requestedConfiguration.TenantId = id;
            dbContext.TenantSsoConfigurations.Add(requestedConfiguration);
            configuration = requestedConfiguration;
        }
        else
        {
            configuration.EntraTenantId = requestedConfiguration.EntraTenantId;
            configuration.AllowedEmailDomain = requestedConfiguration.AllowedEmailDomain;
            configuration.JitProvisioningEnabled = requestedConfiguration.JitProvisioningEnabled;
        }

        await auditWriter.WriteAsync(dbContext, httpContext, actor.Id, null, null, "tenant.sso-upserted",
            new { TenantId = id, Sso = before }, new { TenantId = id, Sso = ToSsoResponse(configuration) }, cancellationToken);
        await transaction.CommitAsync(cancellationToken);
        return TypedResults.Ok(ToSsoResponse(configuration));
    }

    private static async Task<IResult> OffboardingStatus(
        Guid id,
        AccountsDbContext dbContext,
        TenantOffboardingService offboarding,
        CancellationToken cancellationToken)
    {
        if (!await dbContext.Tenants.AsNoTracking().AnyAsync(tenant => tenant.Id == id, cancellationToken))
            return TypedResults.NotFound();
        return TypedResults.Ok(ToOffboardingResponse(await offboarding.GetAsync(id, cancellationToken)));
    }

    private static async Task<IResult> RequestExport(
        Guid id,
        ClaimsPrincipal principal,
        HttpContext httpContext,
        AccountsDbContext dbContext,
        UserManager<ApplicationUser> userManager,
        AuthorizationAuditWriter auditWriter,
        TenantOffboardingService offboarding,
        CancellationToken cancellationToken)
    {
        var actor = await userManager.GetUserAsync(principal);
        if (actor is null) return Error(401, "unauthenticated", "Authentication is required.");
        var tenant = await dbContext.Tenants.AsNoTracking().SingleOrDefaultAsync(item => item.Id == id, cancellationToken);
        if (tenant is null) return TypedResults.NotFound();

        var requested = await offboarding.RequestExportAsync(id, cancellationToken);
        if (requested.Created)
        {
            try
            {
                await using var transaction = await dbContext.Database.BeginTransactionAsync(cancellationToken);
                await auditWriter.WriteAsync(dbContext, httpContext, actor.Id, null, null, "tenant.offboarding-export-requested",
                    new { TenantId = id }, new { TenantId = id, requested.State.ExportId, Status = "export_requested" }, cancellationToken);
                await transaction.CommitAsync(cancellationToken);
            }
            catch
            {
                await offboarding.RemoveAsync(id, requested.State.ExportId, cancellationToken);
                throw;
            }
        }

        return TypedResults.Accepted($"/api/v1/identity/admin/tenants/{id}/offboarding", new
        {
            tenantId = id,
            exportId = requested.State.ExportId,
            exportRequestId = requested.State.ExportId,
            status = "export_requested",
            purgeToken = requested.State.PurgeToken,
            purgeConfirmation = $"PURGE {tenant.Slug}",
            deferred = "Cross-module export and purge are deferred to system orchestration; no tenant data was deleted.",
        });
    }

    private static async Task<IResult> RequestPurge(
        Guid id,
        [FromBody] TenantPurgeRequest? request,
        ClaimsPrincipal principal,
        HttpContext httpContext,
        AccountsDbContext dbContext,
        UserManager<ApplicationUser> userManager,
        AuthorizationAuditWriter auditWriter,
        TenantOffboardingService offboarding,
        CancellationToken cancellationToken)
    {
        var actor = await userManager.GetUserAsync(principal);
        if (actor is null) return Error(401, "unauthenticated", "Authentication is required.");
        var tenant = await dbContext.Tenants.AsNoTracking().SingleOrDefaultAsync(item => item.Id == id, cancellationToken);
        if (tenant is null) return TypedResults.NotFound();
        if (!Guid.TryParse(request?.ExportRequestId, out var exportId) || string.IsNullOrWhiteSpace(request?.PurgeToken) ||
            !await offboarding.ValidatePurgeAsync(id, exportId, request.PurgeToken, cancellationToken) ||
            !string.Equals(request.Confirmation, $"PURGE {tenant.Slug}", StringComparison.Ordinal))
            return Error(400, "purge_confirmation_required", "A prior export request, its two-step token, and the exact purge confirmation are required.");

        await using var transaction = await dbContext.Database.BeginTransactionAsync(cancellationToken);
        await auditWriter.WriteAsync(dbContext, httpContext, actor.Id, null, null, "tenant.offboarding-purge-requested",
            new { TenantId = id, ExportId = exportId }, new
            {
                TenantId = id,
                ExportId = exportId,
                Status = "deferred_cross_module_orchestration",
            }, cancellationToken);
        await transaction.CommitAsync(cancellationToken);
        await offboarding.MarkPurgeRequestedAsync(id, cancellationToken);

        return TypedResults.Accepted($"/api/v1/identity/admin/tenants/{id}/offboarding", new
        {
            tenantId = id,
            exportId,
            status = "deferred_cross_module_orchestration",
            message = "Purge was acknowledged but not executed. Cross-module deletion is deferred to system orchestration.",
        });
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
                dbContext.TenantMemberships.Count(membership => membership.TenantId == tenant.Id),
                dbContext.TenantSsoConfigurations.Any(configuration => configuration.TenantId == tenant.Id)))
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

    private static (TenantSsoConfiguration? Configuration, IResult? Error) ValidateSso(TenantSsoRequest? request)
    {
        if (request is null || !Guid.TryParse(request.EntraTenantId, out var entraTenantId) || entraTenantId == Guid.Empty)
            return (null, Error(400, "invalid_sso", "A valid Entra tenant id is required."));
        var domain = NormalizeDomain(request.AllowedEmailDomain);
        if (request.AllowedEmailDomain is not null && domain is null)
            return (null, Error(400, "invalid_email_domain", "Allowed email domain must be a valid DNS domain."));
        return (new TenantSsoConfiguration
        {
            EntraTenantId = entraTenantId,
            AllowedEmailDomain = domain,
            JitProvisioningEnabled = request.JitProvisioningEnabled,
        }, null);
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

    private static bool SameSso(TenantSsoConfiguration left, TenantSsoConfiguration right) =>
        left.EntraTenantId == right.EntraTenantId &&
        string.Equals(left.AllowedEmailDomain, right.AllowedEmailDomain, StringComparison.Ordinal) &&
        left.JitProvisioningEnabled == right.JitProvisioningEnabled;

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

    private static TenantSsoResponse ToSsoResponse(TenantSsoConfiguration configuration) =>
        new(configuration.EntraTenantId, configuration.AllowedEmailDomain, configuration.JitProvisioningEnabled);

    private static object ToOffboardingResponse(OffboardingState? state) => state is null
        ? new { status = "not_requested", exportId = (Guid?)null, requestedAtUtc = (DateTimeOffset?)null, purgeRequestedAtUtc = (DateTimeOffset?)null }
        : new { status = state.PurgeRequestedAtUtc.HasValue ? "deferred_cross_module_orchestration" : "export_requested", exportId = (Guid?)state.ExportId, requestedAtUtc = (DateTimeOffset?)state.RequestedAtUtc, purgeRequestedAtUtc = state.PurgeRequestedAtUtc };

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
    string? AdminDisplayName = null,
    TenantSsoRequest? Sso = null);

internal sealed record TenantUpdateRequest(
    string? Name,
    string? Slug,
    string? Status,
    IReadOnlyCollection<string>? EnabledModules);

internal sealed record TenantSsoRequest(
    string? EntraTenantId,
    string? AllowedEmailDomain,
    bool JitProvisioningEnabled);

internal sealed record TenantPurgeRequest(
    string? ExportRequestId,
    string? PurgeToken,
    string? Confirmation);

internal sealed record TenantControlPlaneResponse(
    Guid Id,
    string Name,
    string Slug,
    string Status,
    IReadOnlyCollection<string> EnabledModules,
    int MembershipsCount,
    bool SsoConfigured,
    Guid? SeededAdminInvitationId = null,
    string? SeededAdminInvitationToken = null);

internal sealed record TenantSsoResponse(
    Guid EntraTenantId,
    string? AllowedEmailDomain,
    bool JitProvisioningEnabled);