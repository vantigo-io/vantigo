using Microsoft.AspNetCore.Identity;
using Microsoft.AspNetCore.Identity.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore;

using Vantigo.Customers.Api.Database.Accounts.Configurations;

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
        modelBuilder.ApplyConfiguration(new ApplicationUserEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new IdentityRoleEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new IdentityUserClaimEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new IdentityUserRoleEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new IdentityUserLoginEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new IdentityUserTokenEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new IdentityRoleClaimEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new BootstrapStateEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new InvitationEntityTypeConfiguration());
    }
}
