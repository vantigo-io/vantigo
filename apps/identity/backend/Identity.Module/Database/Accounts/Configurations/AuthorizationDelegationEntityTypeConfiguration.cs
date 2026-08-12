using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

namespace Vantigo.Identity.Database.Accounts.Configurations;

internal sealed class AuthorizationDelegationEntityTypeConfiguration : IEntityTypeConfiguration<AuthorizationDelegation>
{
    public void Configure(EntityTypeBuilder<AuthorizationDelegation> builder)
    {
        builder.ToTable("authorization_delegations", "identity");
        builder.HasKey(item => item.Id).HasName("pk_authorization_delegations");
        builder.Property(item => item.Id).HasColumnName("id");
        builder.Property(item => item.GranteeUserId).HasColumnName("grantee_user_id");
        builder.Property(item => item.CreatedByUserId).HasColumnName("created_by_user_id");
        builder.Property(item => item.ExpiresAt).HasColumnName("expires_at");
        builder.Property(item => item.RevokedAt).HasColumnName("revoked_at");
        builder.Property(item => item.ConcurrencyStamp).HasColumnName("concurrency_stamp").IsConcurrencyToken().IsRequired();
        builder.Property(item => item.CanCreateRoles).HasColumnName("can_create_roles").IsRequired();
        builder.HasOne<ApplicationUser>().WithMany().HasForeignKey(item => item.GranteeUserId)
            .HasConstraintName("fk_authorization_delegations_grantee_user_id").OnDelete(DeleteBehavior.Cascade);
        builder.HasOne<ApplicationUser>().WithMany().HasForeignKey(item => item.CreatedByUserId)
            .HasConstraintName("fk_authorization_delegations_created_by_user_id").OnDelete(DeleteBehavior.Restrict);
        builder.HasIndex(item => item.GranteeUserId).HasDatabaseName("ix_authorization_delegations_grantee_user_id");
    }
}

internal sealed class AuthorizationDelegationPermissionEntityTypeConfiguration : IEntityTypeConfiguration<AuthorizationDelegationPermission>
{
    public void Configure(EntityTypeBuilder<AuthorizationDelegationPermission> builder)
    {
        builder.ToTable("authorization_delegation_permissions", "identity");
        builder.HasKey(item => new { item.DelegationId, item.PermissionKey }).HasName("pk_authorization_delegation_permissions");
        builder.Property(item => item.DelegationId).HasColumnName("delegation_id");
        builder.Property(item => item.PermissionKey).HasColumnName("permission_key").HasMaxLength(200).IsRequired();
        builder.HasOne<AuthorizationDelegation>().WithMany().HasForeignKey(item => item.DelegationId)
            .HasConstraintName("fk_authorization_delegation_permissions_delegation_id").OnDelete(DeleteBehavior.Cascade);
    }
}

internal sealed class AuthorizationDelegationRoleEntityTypeConfiguration : IEntityTypeConfiguration<AuthorizationDelegationRole>
{
    public void Configure(EntityTypeBuilder<AuthorizationDelegationRole> builder)
    {
        builder.ToTable("authorization_delegation_roles", "identity");
        builder.HasKey(item => new { item.DelegationId, item.RoleId }).HasName("pk_authorization_delegation_roles");
        builder.Property(item => item.DelegationId).HasColumnName("delegation_id");
        builder.Property(item => item.RoleId).HasColumnName("role_id");
        builder.HasOne<AuthorizationDelegation>().WithMany().HasForeignKey(item => item.DelegationId)
            .HasConstraintName("fk_authorization_delegation_roles_delegation_id").OnDelete(DeleteBehavior.Cascade);
        builder.HasOne<Microsoft.AspNetCore.Identity.IdentityRole<Guid>>().WithMany().HasForeignKey(item => item.RoleId)
            .HasConstraintName("fk_authorization_delegation_roles_role_id").OnDelete(DeleteBehavior.Cascade);
    }
}