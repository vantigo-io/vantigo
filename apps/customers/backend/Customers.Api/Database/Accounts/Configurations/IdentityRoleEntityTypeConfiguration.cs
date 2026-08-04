using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

namespace Vantigo.Customers.Api.Database.Accounts.Configurations;

internal sealed class IdentityRoleEntityTypeConfiguration : IEntityTypeConfiguration<IdentityRole<Guid>>
{
    public void Configure(EntityTypeBuilder<IdentityRole<Guid>> builder)
    {
        builder.ToTable("roles", "accounts");
        builder.HasKey(role => role.Id).HasName("pk_roles");
        builder.Property(role => role.Id).HasColumnName("id");
        builder.Property(role => role.Name).HasColumnName("name");
        builder.Property(role => role.NormalizedName).HasColumnName("normalized_name");
        builder.Property(role => role.ConcurrencyStamp).HasColumnName("concurrency_stamp");
        builder.HasIndex(role => role.NormalizedName)
            .HasDatabaseName("ux_roles_normalized_name")
            .IsUnique();
    }
}
