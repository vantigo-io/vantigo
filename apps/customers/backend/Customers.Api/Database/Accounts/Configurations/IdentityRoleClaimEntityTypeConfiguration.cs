using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

namespace Vantigo.Customers.Api.Database.Accounts.Configurations;

internal sealed class IdentityRoleClaimEntityTypeConfiguration : IEntityTypeConfiguration<IdentityRoleClaim<Guid>>
{
    public void Configure(EntityTypeBuilder<IdentityRoleClaim<Guid>> builder)
    {
        builder.ToTable("role_claims", "accounts");
        builder.HasKey(claim => claim.Id).HasName("pk_role_claims");
        builder.Property(claim => claim.Id).HasColumnName("id");
        builder.Property(claim => claim.RoleId).HasColumnName("role_id");
        builder.Property(claim => claim.ClaimType).HasColumnName("claim_type");
        builder.Property(claim => claim.ClaimValue).HasColumnName("claim_value");
        builder.HasOne<IdentityRole<Guid>>()
            .WithMany()
            .HasForeignKey(claim => claim.RoleId)
            .HasConstraintName("fk_role_claims_roles_role_id")
            .OnDelete(DeleteBehavior.Cascade);
        builder.HasIndex(claim => claim.RoleId).HasDatabaseName("ix_role_claims_role_id");
    }
}
