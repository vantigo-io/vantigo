using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

using Vantigo.Customers.Api.Database.Accounts;

namespace Vantigo.Customers.Api.Database.Accounts.Configurations;

internal sealed class ApplicationUserEntityTypeConfiguration : IEntityTypeConfiguration<ApplicationUser>
{
    public void Configure(EntityTypeBuilder<ApplicationUser> builder)
    {
        builder.ToTable("users", "accounts");
        builder.HasKey(user => user.Id).HasName("pk_users");
        builder.Property(user => user.Id).HasColumnName("id");
        builder.Property(user => user.DisplayName).HasColumnName("display_name").HasMaxLength(200).IsRequired();
        builder.Property(user => user.UserName).HasColumnName("user_name");
        builder.Property(user => user.NormalizedUserName).HasColumnName("normalized_user_name");
        builder.Property(user => user.Email).HasColumnName("email");
        builder.Property(user => user.NormalizedEmail).HasColumnName("normalized_email");
        builder.Property(user => user.EmailConfirmed).HasColumnName("email_confirmed");
        builder.Property(user => user.PasswordHash).HasColumnName("password_hash");
        builder.Property(user => user.SecurityStamp).HasColumnName("security_stamp");
        builder.Property(user => user.ConcurrencyStamp).HasColumnName("concurrency_stamp");
        builder.Property(user => user.PhoneNumber).HasColumnName("phone_number");
        builder.Property(user => user.PhoneNumberConfirmed).HasColumnName("phone_number_confirmed");
        builder.Property(user => user.TwoFactorEnabled).HasColumnName("two_factor_enabled");
        builder.Property(user => user.LockoutEnd).HasColumnName("lockout_end");
        builder.Property(user => user.LockoutEnabled).HasColumnName("lockout_enabled");
        builder.Property(user => user.AccessFailedCount).HasColumnName("access_failed_count");
        builder.HasIndex(user => user.NormalizedUserName)
            .HasDatabaseName("ux_users_normalized_user_name")
            .IsUnique();
        builder.HasIndex(user => user.NormalizedEmail)
            .HasDatabaseName("ix_users_normalized_email");
    }
}
