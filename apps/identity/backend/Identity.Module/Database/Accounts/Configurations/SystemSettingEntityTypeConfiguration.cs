using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

namespace Vantigo.Identity.Database.Accounts.Configurations;

internal sealed class SystemSettingEntityTypeConfiguration : IEntityTypeConfiguration<SystemSetting>
{
    public void Configure(EntityTypeBuilder<SystemSetting> builder)
    {
        builder.ToTable("system_settings", "identity");
        builder.HasKey(setting => setting.Id).HasName("pk_system_settings");
        builder.Property(setting => setting.Id).HasColumnName("id");
        builder.Property(setting => setting.MaintenanceEnabled)
            .HasColumnName("maintenance_enabled")
            .IsRequired();
        builder.Property(setting => setting.MaintenanceMessage)
            .HasColumnName("maintenance_message")
            .HasMaxLength(500);
        builder.Property(setting => setting.UpdatedAt)
            .HasColumnName("updated_at")
            .IsRequired();
        builder.Property(setting => setting.UpdatedBy)
            .HasColumnName("updated_by")
            .HasMaxLength(256)
            .IsRequired();
    }
}