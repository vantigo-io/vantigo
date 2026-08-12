using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

namespace Vantigo.Identity.Database.Accounts.Configurations;

internal sealed class PasskeyCeremonyEntityTypeConfiguration : IEntityTypeConfiguration<PasskeyCeremony>
{
    public void Configure(EntityTypeBuilder<PasskeyCeremony> builder)
    {
        builder.ToTable("passkey_ceremonies", "identity");
        builder.HasKey(ceremony => ceremony.Id).HasName("pk_passkey_ceremonies");
        builder.Property(ceremony => ceremony.Id).HasColumnName("id");
        builder.Property(ceremony => ceremony.UserId).HasColumnName("user_id");
        builder.Property(ceremony => ceremony.Kind).HasColumnName("kind").HasMaxLength(32).IsRequired();
        builder.Property(ceremony => ceremony.State).HasColumnName("state").HasMaxLength(20000).IsRequired();
        builder.Property(ceremony => ceremony.CredentialName).HasColumnName("credential_name").HasMaxLength(100);
        builder.Property(ceremony => ceremony.ClientAddress).HasColumnName("client_address").HasMaxLength(64);
        builder.Property(ceremony => ceremony.ExpiresAt).HasColumnName("expires_at");
        builder.Property(ceremony => ceremony.Consumed).HasColumnName("consumed");
        builder.HasIndex(ceremony => new { ceremony.ExpiresAt, ceremony.Consumed })
            .HasDatabaseName("ix_passkey_ceremonies_expiration");
        builder.HasOne<ApplicationUser>()
            .WithMany()
            .HasForeignKey(ceremony => ceremony.UserId)
            .HasConstraintName("fk_passkey_ceremonies_users_user_id")
            .OnDelete(DeleteBehavior.Cascade);
    }
}