using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

namespace Vantigo.Identity.Database.Accounts.Configurations;

internal sealed class ProfileAvatarEntityTypeConfiguration : IEntityTypeConfiguration<ProfileAvatar>
{
    public void Configure(EntityTypeBuilder<ProfileAvatar> builder)
    {
        builder.ToTable("profile_avatars", "identity");
        builder.HasKey(avatar => avatar.UserId).HasName("pk_profile_avatars");
        builder.Property(avatar => avatar.UserId).HasColumnName("user_id");
        builder.Property(avatar => avatar.Data).HasColumnName("data").HasColumnType("bytea").IsRequired();
        builder.Property(avatar => avatar.ContentType).HasColumnName("content_type").HasMaxLength(32).IsRequired();
        builder.Property(avatar => avatar.UpdatedAt).HasColumnName("updated_at").IsRequired();
        builder.Property(avatar => avatar.Version).HasColumnName("version").IsRequired();
        builder.HasOne<ApplicationUser>()
            .WithOne()
            .HasForeignKey<ProfileAvatar>(avatar => avatar.UserId)
            .HasConstraintName("fk_profile_avatars_users_user_id")
            .OnDelete(DeleteBehavior.Cascade);
    }
}