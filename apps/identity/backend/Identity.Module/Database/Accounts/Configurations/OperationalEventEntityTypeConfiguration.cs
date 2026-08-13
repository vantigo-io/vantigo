using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

namespace Vantigo.Identity.Database.Accounts.Configurations;

internal sealed class OperationalEventEntityTypeConfiguration : IEntityTypeConfiguration<OperationalEvent>
{
    public void Configure(EntityTypeBuilder<OperationalEvent> builder)
    {
        builder.ToTable("operational_events", "identity");
        builder.HasKey(item => item.Id).HasName("pk_operational_events");
        builder.Property(item => item.Id).HasColumnName("id").ValueGeneratedOnAdd();
        builder.Property(item => item.Kind).HasColumnName("kind").HasMaxLength(64).IsRequired();
        builder.Property(item => item.OccurredAt).HasColumnName("occurred_at").IsRequired();
        builder.HasIndex(item => item.Kind).IsUnique().HasDatabaseName("ux_operational_events_kind");
    }
}