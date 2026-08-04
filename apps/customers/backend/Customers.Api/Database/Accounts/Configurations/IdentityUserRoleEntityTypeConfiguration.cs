using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

using Vantigo.Customers.Api.Database.Accounts;

namespace Vantigo.Customers.Api.Database.Accounts.Configurations;

internal sealed class IdentityUserRoleEntityTypeConfiguration : IEntityTypeConfiguration<IdentityUserRole<Guid>>
{
    public void Configure(EntityTypeBuilder<IdentityUserRole<Guid>> builder)
    {
        builder.ToTable("user_roles", "accounts");
        builder.HasKey(userRole => new { userRole.UserId, userRole.RoleId }).HasName("pk_user_roles");
        builder.Property(userRole => userRole.UserId).HasColumnName("user_id");
        builder.Property(userRole => userRole.RoleId).HasColumnName("role_id");
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
    }
}
