using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

using Vantigo.Customers.Api.Database.Accounts;

namespace Vantigo.Customers.Api.Database.Accounts.Configurations;

internal sealed class IdentityUserLoginEntityTypeConfiguration : IEntityTypeConfiguration<IdentityUserLogin<Guid>>
{
    public void Configure(EntityTypeBuilder<IdentityUserLogin<Guid>> builder)
    {
        builder.ToTable("user_logins", "accounts");
        builder.HasKey(login => new { login.LoginProvider, login.ProviderKey }).HasName("pk_user_logins");
        builder.Property(login => login.LoginProvider).HasColumnName("login_provider");
        builder.Property(login => login.ProviderKey).HasColumnName("provider_key");
        builder.Property(login => login.ProviderDisplayName).HasColumnName("provider_display_name");
        builder.Property(login => login.UserId).HasColumnName("user_id");
        builder.HasOne<ApplicationUser>()
            .WithMany()
            .HasForeignKey(login => login.UserId)
            .HasConstraintName("fk_user_logins_users_user_id")
            .OnDelete(DeleteBehavior.Cascade);
        builder.HasIndex(login => login.UserId).HasDatabaseName("ix_user_logins_user_id");
    }
}