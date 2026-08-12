using Microsoft.AspNetCore.Identity;
using Microsoft.AspNetCore.Identity.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore;

using Vantigo.Identity.Database.Accounts.Configurations;

namespace Vantigo.Identity.Database.Accounts;

public sealed class AccountsDbContext(DbContextOptions<AccountsDbContext> options)
    : IdentityDbContext<ApplicationUser, IdentityRole<Guid>, Guid>(options)
{
    public DbSet<BootstrapState> BootstrapStates => Set<BootstrapState>();

    public DbSet<Invitation> Invitations => Set<Invitation>();

    public DbSet<RoleMetadata> RoleMetadata => Set<RoleMetadata>();

    public DbSet<RolePermission> RolePermissions => Set<RolePermission>();

    public DbSet<AuthorizationDelegation> AuthorizationDelegations => Set<AuthorizationDelegation>();

    public DbSet<AuthorizationDelegationPermission> AuthorizationDelegationPermissions => Set<AuthorizationDelegationPermission>();

    public DbSet<AuthorizationDelegationRole> AuthorizationDelegationRoles => Set<AuthorizationDelegationRole>();

    public DbSet<AuthorizationAuditEvent> AuthorizationAuditEvents => Set<AuthorizationAuditEvent>();

    protected override void OnModelCreating(ModelBuilder modelBuilder)
    {
        base.OnModelCreating(modelBuilder);

        modelBuilder.HasDefaultSchema("identity");
        modelBuilder.ApplyConfiguration(new ApplicationUserEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new IdentityRoleEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new IdentityUserClaimEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new IdentityUserRoleEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new IdentityUserLoginEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new IdentityUserTokenEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new IdentityRoleClaimEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new BootstrapStateEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new InvitationEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new RoleMetadataEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new RolePermissionEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new AuthorizationDelegationEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new AuthorizationDelegationPermissionEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new AuthorizationDelegationRoleEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new AuthorizationAuditEventEntityTypeConfiguration());
    }
}