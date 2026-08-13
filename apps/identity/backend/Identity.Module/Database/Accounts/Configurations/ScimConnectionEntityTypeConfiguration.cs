using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

namespace Vantigo.Identity.Database.Accounts.Configurations;

internal sealed class ScimConnectionEntityTypeConfiguration : IEntityTypeConfiguration<ScimConnection>
{
    public void Configure(EntityTypeBuilder<ScimConnection> builder)
    {
        builder.ToTable("scim_connections", "identity");
        builder.HasKey(item => item.Id).HasName("pk_scim_connections");
        builder.Property(item => item.Id).HasColumnName("id");
        builder.Property(item => item.CreatedAt).HasColumnName("created_at").IsRequired();
        builder.Property(item => item.UpdatedAt).HasColumnName("updated_at").IsRequired();
    }
}