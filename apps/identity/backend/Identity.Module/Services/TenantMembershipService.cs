using System.Security.Claims;

using Microsoft.AspNetCore.Authentication;
using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Options;

using Vantigo.Configuration;
using Vantigo.Identity.Database.Accounts;
using Vantigo.Tenancy.Abstractions;

namespace Vantigo.Identity.Services;

/// <summary>Creates memberships and resolves the active tenant for identity sessions.</summary>
public sealed class TenantMembershipService(
    AccountsDbContext dbContext,
    ITenantDirectory tenantDirectory,
    IOptions<TenancyOptions> tenancyOptions)
{
    public const string ActiveTenantClaim = "vantigo:active_tenant";

    /// <summary>Adds the user to the default tenant when single-tenant mode is active.</summary>
    public async Task EnsureDefaultMembershipAsync(Guid userId, CancellationToken cancellationToken = default)
    {
        if (tenancyOptions.Value.IsMultiTenant) return;
        await EnsureMembershipAsync(userId, await tenantDirectory.GetDefaultTenantAsync(cancellationToken), cancellationToken);
    }

    /// <summary>Returns the bootstrap tenant id used for legacy invitations.</summary>
    public Task<TenantId> GetDefaultTenantIdAsync(CancellationToken cancellationToken = default) =>
        tenantDirectory.GetDefaultTenantAsync(cancellationToken);

    /// <summary>Adds a membership if it does not already exist.</summary>
    public async Task EnsureMembershipAsync(Guid userId, TenantId tenantId, CancellationToken cancellationToken = default)
    {
        if (!await dbContext.Tenants.AsNoTracking().AnyAsync(tenant =>
                tenant.Id == tenantId.Value && tenant.Status == TenantStatus.Active, cancellationToken))
            throw new InvalidOperationException("The tenant is not active.");

        if (!await dbContext.TenantMemberships.AnyAsync(membership =>
                membership.UserId == userId && membership.TenantId == tenantId.Value, cancellationToken))
        {
            dbContext.TenantMemberships.Add(new TenantMembership
            {
                UserId = userId,
                TenantId = tenantId.Value,
                CreatedAtUtc = DateTimeOffset.UtcNow,
            });
            await dbContext.SaveChangesAsync(cancellationToken);
        }
    }

    /// <summary>Returns the user's active memberships in stable display order.</summary>
    public async Task<IReadOnlyList<TenantSummary>> GetTenantsAsync(Guid userId, CancellationToken cancellationToken = default)
    {
        var tenants = await dbContext.TenantMemberships.AsNoTracking()
            .Where(membership => membership.UserId == userId &&
                dbContext.Tenants.Any(tenant => tenant.Id == membership.TenantId && tenant.Status == TenantStatus.Active))
            .Join(dbContext.Tenants.AsNoTracking(), membership => membership.TenantId, tenant => tenant.Id,
                (_, tenant) => new { tenant.Id, tenant.Name, tenant.Slug })
            .OrderBy(tenant => tenant.Name).ThenBy(tenant => tenant.Id)
            .ToListAsync(cancellationToken);
        return tenants.Select(tenant => new TenantSummary(tenant.Id, tenant.Name, tenant.Slug)).ToArray();
    }

    /// <summary>Chooses a valid persisted or previously claimed tenant, failing closed otherwise.</summary>
    public async Task<Guid?> ResolveActiveTenantIdAsync(ApplicationUser user, ClaimsPrincipal? principal = null,
        CancellationToken cancellationToken = default)
    {
        var memberships = await GetTenantsAsync(user.Id, cancellationToken);
        var claimed = principal?.FindFirstValue(ActiveTenantClaim);
        if (Guid.TryParse(claimed, out var claimedId) && memberships.Any(tenant => tenant.Id == claimedId)) return claimedId;
        if (user.ActiveTenantId is Guid persistedId && memberships.Any(tenant => tenant.Id == persistedId)) return persistedId;
        return memberships.FirstOrDefault()?.Id;
    }

    /// <summary>Returns the claim used to bind authorization to the active tenant.</summary>
    public static Claim ActiveTenantClaimFor(Guid tenantId) => new(ActiveTenantClaim, tenantId.ToString("D"));

    /// <summary>Reissues an application cookie with the active tenant claim.</summary>
    public async Task SignInWithActiveTenantAsync(
        SignInManager<ApplicationUser> signInManager,
        ApplicationUser user,
        ClaimsPrincipal? priorPrincipal = null,
        bool isPersistent = false,
        CancellationToken cancellationToken = default)
    {
        var tenantId = await ResolveActiveTenantIdAsync(user, priorPrincipal, cancellationToken);
        user.ActiveTenantId = tenantId;
        var claims = (priorPrincipal?.Claims ?? []).Where(claim =>
                claim.Type is "amr" or ClaimTypes.AuthenticationMethod)
            .ToList();
        if (tenantId is Guid value) claims.Add(ActiveTenantClaimFor(value));
        user.ActiveTenantId = tenantId;
        await dbContext.SaveChangesAsync(cancellationToken);
        await signInManager.SignInWithClaimsAsync(user,
            new AuthenticationProperties { IsPersistent = isPersistent }, claims);
    }
}

/// <summary>Public tenant summary returned by identity session APIs.</summary>
public sealed record TenantSummary(Guid Id, string Name, string Slug);