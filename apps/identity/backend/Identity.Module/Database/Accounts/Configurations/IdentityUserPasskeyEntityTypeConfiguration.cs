using System.Text.Json;

using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;
using Microsoft.EntityFrameworkCore.Storage.ValueConversion;

namespace Vantigo.Identity.Database.Accounts.Configurations;

internal sealed class IdentityUserPasskeyEntityTypeConfiguration : IEntityTypeConfiguration<IdentityUserPasskey<Guid>>
{
    public void Configure(EntityTypeBuilder<IdentityUserPasskey<Guid>> builder)
    {
        builder.ToTable("user_passkeys", "identity");
        builder.HasKey(passkey => new { passkey.UserId, passkey.CredentialId }).HasName("pk_user_passkeys");
        builder.Property(passkey => passkey.UserId).HasColumnName("user_id");
        builder.Property(passkey => passkey.CredentialId).HasColumnName("credential_id").HasColumnType("bytea");
        builder.Property(passkey => passkey.Data)
            .HasColumnName("data")
            .HasColumnType("text")
            .HasConversion(new ValueConverter<IdentityPasskeyData, string>(
                data => JsonSerializer.Serialize(data, JsonSerializerOptions.Web),
                data => JsonSerializer.Deserialize<IdentityPasskeyData>(data, JsonSerializerOptions.Web)!))
            .IsRequired();
        builder.HasOne<ApplicationUser>()
            .WithMany()
            .HasForeignKey(passkey => passkey.UserId)
            .HasConstraintName("fk_user_passkeys_users_user_id")
            .OnDelete(DeleteBehavior.Cascade);
        builder.HasIndex(passkey => passkey.CredentialId)
            .IsUnique()
            .HasDatabaseName("ux_user_passkeys_credential_id");
    }
}