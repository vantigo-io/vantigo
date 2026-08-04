using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

using Vantigo.Customers.Api.Database.Accounts;

namespace Vantigo.Customers.Api.Database.Accounts.Configurations;

internal sealed class IdentityUserTokenEntityTypeConfiguration : IEntityTypeConfiguration<IdentityUserToken<Guid>>
{
    public void Configure(EntityTypeBuilder<IdentityUserToken<Guid>> builder)
    {
        builder.ToTable("user_tokens", "accounts");
        builder.HasKey(token => new { token.UserId, token.LoginProvider, token.Name }).HasName("pk_user_tokens");
        builder.Property(token => token.UserId).HasColumnName("user_id");
        builder.Property(token => token.LoginProvider).HasColumnName("login_provider");
        builder.Property(token => token.Name).HasColumnName("name");
        builder.Property(token => token.Value).HasColumnName("value");
        builder.HasOne<ApplicationUser>()
            .WithMany()
            .HasForeignKey(token => token.UserId)
            .HasConstraintName("fk_user_tokens_users_user_id")
            .OnDelete(DeleteBehavior.Cascade);
    }
}