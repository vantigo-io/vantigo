using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Identity.Database.Accounts.Configurations;

internal sealed class ApplicationUserRoleEntityTypeConfiguration : IEntityTypeConfiguration<ApplicationUserRole>
{
    public void Configure(EntityTypeBuilder<ApplicationUserRole> builder)
    {
        builder.ToTable("user_roles", "identity");
        builder.HasKey(userRole => new { userRole.UserId, userRole.RoleId }).HasName("pk_user_roles");
        builder.Property(userRole => userRole.UserId).HasColumnName("user_id");
        builder.Property(userRole => userRole.RoleId).HasColumnName("role_id");
        builder.Property(userRole => userRole.TenantId).HasColumnName("tenant_id");
        builder.HasOne<ApplicationUser>()
            .WithMany()
            .HasForeignKey(userRole => userRole.UserId)
            .HasConstraintName("fk_user_roles_users_user_id")
            .OnDelete(DeleteBehavior.Cascade);
        builder.HasOne<IdentityRole<Guid>>()
            .WithMany()
            .HasForeignKey(userRole => userRole.RoleId)
            .HasConstraintName("fk_user_roles_roles_role_id")
            .OnDelete(DeleteBehavior.Cascade);
        builder.HasIndex(userRole => userRole.RoleId).HasDatabaseName("ix_user_roles_role_id");
        builder.HasIndex(userRole => new { userRole.UserId, userRole.RoleId, userRole.TenantId })
            .IsUnique().HasDatabaseName("ux_user_roles_user_role_tenant");
    }
}