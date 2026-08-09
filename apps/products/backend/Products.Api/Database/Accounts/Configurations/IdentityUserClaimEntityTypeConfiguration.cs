using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

using Vantigo.Products.Api.Database.Accounts;

namespace Vantigo.Products.Api.Database.Accounts.Configurations;

internal sealed class IdentityUserClaimEntityTypeConfiguration : IEntityTypeConfiguration<IdentityUserClaim<Guid>>
{
    public void Configure(EntityTypeBuilder<IdentityUserClaim<Guid>> builder)
    {
        builder.ToTable("user_claims", "accounts");
        builder.HasKey(claim => claim.Id).HasName("pk_user_claims");
        builder.Property(claim => claim.Id).HasColumnName("id");
        builder.Property(claim => claim.UserId).HasColumnName("user_id");
        builder.Property(claim => claim.ClaimType).HasColumnName("claim_type");
        builder.Property(claim => claim.ClaimValue).HasColumnName("claim_value");
        builder.HasOne<ApplicationUser>()
            .WithMany()
            .HasForeignKey(claim => claim.UserId)
            .HasConstraintName("fk_user_claims_users_user_id")
            .OnDelete(DeleteBehavior.Cascade);
        builder.HasIndex(claim => claim.UserId).HasDatabaseName("ix_user_claims_user_id");
    }
}