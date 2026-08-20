using Microsoft.AspNetCore.Identity;
using Microsoft.AspNetCore.Identity.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore;

using Vantigo.Identity.Database.Accounts.Configurations;

namespace Vantigo.Identity.Database.Accounts;

public sealed class AccountsDbContext(DbContextOptions<AccountsDbContext> options)
    : IdentityDbContext<ApplicationUser, IdentityRole<Guid>, Guid,
        IdentityUserClaim<Guid>, ApplicationUserRole, IdentityUserLogin<Guid>,
        IdentityRoleClaim<Guid>, IdentityUserToken<Guid>, IdentityUserPasskey<Guid>>(options)
{
    public DbSet<BootstrapState> BootstrapStates => Set<BootstrapState>();

    public DbSet<Tenant> Tenants => Set<Tenant>();

    public DbSet<TenantMembership> TenantMemberships => Set<TenantMembership>();

    public DbSet<Invitation> Invitations => Set<Invitation>();

    public DbSet<RoleMetadata> RoleMetadata => Set<RoleMetadata>();

    public DbSet<RolePermission> RolePermissions => Set<RolePermission>();

    public DbSet<AuthorizationDelegation> AuthorizationDelegations => Set<AuthorizationDelegation>();

    public DbSet<AuthorizationDelegationPermission> AuthorizationDelegationPermissions => Set<AuthorizationDelegationPermission>();

    public DbSet<AuthorizationDelegationRole> AuthorizationDelegationRoles => Set<AuthorizationDelegationRole>();

    public DbSet<AuthorizationAuditEvent> AuthorizationAuditEvents => Set<AuthorizationAuditEvent>();

    public DbSet<PasskeyCeremony> PasskeyCeremonies => Set<PasskeyCeremony>();

    public DbSet<ProfileAvatar> ProfileAvatars => Set<ProfileAvatar>();

    public DbSet<ScimConnection> ScimConnections => Set<ScimConnection>();

    public DbSet<ScimUserMapping> ScimUserMappings => Set<ScimUserMapping>();

    public DbSet<AccessGroup> AccessGroups => Set<AccessGroup>();

    public DbSet<AccessGroupMembership> AccessGroupMemberships => Set<AccessGroupMembership>();

    public DbSet<AccessGroupRoleMapping> AccessGroupRoleMappings => Set<AccessGroupRoleMapping>();

    public DbSet<OperationalEvent> OperationalEvents => Set<OperationalEvent>();

    public DbSet<SystemSetting> SystemSettings => Set<SystemSetting>();

    protected override void OnModelCreating(ModelBuilder modelBuilder)
    {
        base.OnModelCreating(modelBuilder);

        modelBuilder.HasDefaultSchema("identity");
        modelBuilder.ApplyConfiguration(new ApplicationUserEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new TenantEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new TenantMembershipEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new IdentityRoleEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new IdentityUserClaimEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new ApplicationUserRoleEntityTypeConfiguration());
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
        modelBuilder.ApplyConfiguration(new IdentityUserPasskeyEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new PasskeyCeremonyEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new ProfileAvatarEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new OperationalEventEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new ScimConnectionEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new ScimUserMappingEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new AccessGroupEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new AccessGroupMembershipEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new AccessGroupRoleMappingEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new SystemSettingEntityTypeConfiguration());
    }
}