using Microsoft.AspNetCore.Identity;
using Microsoft.AspNetCore.Identity.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore;

namespace Vantigo.Customers.Api.Database.Accounts;

public sealed class AccountsDbContext(DbContextOptions<AccountsDbContext> options)
    : IdentityDbContext<ApplicationUser, IdentityRole<Guid>, Guid>(options)
{
    public DbSet<BootstrapState> BootstrapStates => Set<BootstrapState>();

    public DbSet<Invitation> Invitations => Set<Invitation>();

    protected override void OnModelCreating(ModelBuilder modelBuilder)
    {
        base.OnModelCreating(modelBuilder);

        modelBuilder.HasDefaultSchema("accounts");
        modelBuilder.Entity<ApplicationUser>(builder =>
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
        });

        modelBuilder.Entity<IdentityRole<Guid>>(builder =>
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
        });

        modelBuilder.Entity<IdentityUserClaim<Guid>>(builder =>
        {
            builder.ToTable("user_claims", "accounts");
            builder.HasKey(claim => claim.Id).HasName("pk_user_claims");
            builder.Property(claim => claim.Id).HasColumnName("id");
            builder.Property(claim => claim.UserId).HasColumnName("user_id");
            builder.Property(claim => claim.ClaimType).HasColumnName("claim_type");
            builder.Property(claim => claim.ClaimValue).HasColumnName("claim_value");
            builder.HasOne<ApplicationUser>()
                .WithMany()
                .HasForeignKey(claim => claim.UserId)
                .HasConstraintName("fk_user_claims_users_user_id")
                .OnDelete(DeleteBehavior.Cascade);
            builder.HasIndex(claim => claim.UserId).HasDatabaseName("ix_user_claims_user_id");
        });

        modelBuilder.Entity<IdentityUserRole<Guid>>(builder =>
        {
            builder.ToTable("user_roles", "accounts");
            builder.HasKey(userRole => new { userRole.UserId, userRole.RoleId }).HasName("pk_user_roles");
            builder.Property(userRole => userRole.UserId).HasColumnName("user_id");
            builder.Property(userRole => userRole.RoleId).HasColumnName("role_id");
            builder.HasOne<ApplicationUser>()
                .WithMany()
                .HasForeignKey(userRole => userRole.UserId)
                .HasConstraintName("fk_user_roles_users_user_id")
                .OnDelete(DeleteBehavior.Cascade);
            builder.HasOne<IdentityRole<Guid>>()
                .WithMany()
                .HasForeignKey(userRole => userRole.RoleId)
                .HasConstraintName("fk_user_roles_roles_role_id")
                .OnDelete(DeleteBehavior.Cascade);
            builder.HasIndex(userRole => userRole.RoleId).HasDatabaseName("ix_user_roles_role_id");
        });

        modelBuilder.Entity<IdentityUserLogin<Guid>>(builder =>
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
        });

        modelBuilder.Entity<IdentityUserToken<Guid>>(builder =>
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
        });

        modelBuilder.Entity<IdentityRoleClaim<Guid>>(builder =>
        {
            builder.ToTable("role_claims", "accounts");
            builder.HasKey(claim => claim.Id).HasName("pk_role_claims");
            builder.Property(claim => claim.Id).HasColumnName("id");
            builder.Property(claim => claim.RoleId).HasColumnName("role_id");
            builder.Property(claim => claim.ClaimType).HasColumnName("claim_type");
            builder.Property(claim => claim.ClaimValue).HasColumnName("claim_value");
            builder.HasOne<IdentityRole<Guid>>()
                .WithMany()
                .HasForeignKey(claim => claim.RoleId)
                .HasConstraintName("fk_role_claims_roles_role_id")
                .OnDelete(DeleteBehavior.Cascade);
            builder.HasIndex(claim => claim.RoleId).HasDatabaseName("ix_role_claims_role_id");
        });

        modelBuilder.Entity<BootstrapState>(builder =>
        {
            builder.ToTable("bootstrap_states", "accounts");
            builder.HasKey(state => state.Id).HasName("pk_bootstrap_states");
            builder.Property(state => state.Id).HasColumnName("id");
            builder.Property(state => state.CompletedAt).HasColumnName("completed_at").IsRequired();
        });

        modelBuilder.Entity<Invitation>(builder =>
        {
            builder.ToTable("invitations", "accounts");
            builder.HasKey(invitation => invitation.Id).HasName("pk_invitations");
            builder.Property(invitation => invitation.Id).HasColumnName("id");
            builder.Property(invitation => invitation.Email).HasColumnName("email").HasMaxLength(256).IsRequired();
            builder.Property(invitation => invitation.NormalizedEmail).HasColumnName("normalized_email").HasMaxLength(256).IsRequired();
            builder.Property(invitation => invitation.Role).HasColumnName("role").HasMaxLength(32).IsRequired();
            builder.Property(invitation => invitation.DisplayName).HasColumnName("display_name").HasMaxLength(200);
            builder.Property(invitation => invitation.TokenHash).HasColumnName("token_hash").HasMaxLength(64).IsRequired();
            builder.Property(invitation => invitation.CreatedAt).HasColumnName("created_at").IsRequired();
            builder.Property(invitation => invitation.ExpiresAt).HasColumnName("expires_at").IsRequired();
            builder.Property(invitation => invitation.RevokedAt).HasColumnName("revoked_at");
            builder.Property(invitation => invitation.AcceptedAt).HasColumnName("accepted_at");
            builder.Property(invitation => invitation.InvitedByUserId).HasColumnName("invited_by_user_id").IsRequired();
            builder.HasIndex(invitation => invitation.TokenHash).IsUnique().HasDatabaseName("ux_invitations_token_hash");
            builder.HasIndex(invitation => invitation.NormalizedEmail)
                .HasDatabaseName("ux_invitations_active_normalized_email")
                .HasFilter("accepted_at IS NULL AND revoked_at IS NULL")
                .IsUnique();
        });
    }
}