using Microsoft.AspNetCore.Identity;
using Microsoft.AspNetCore.Identity.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore;

namespace Vantigo.Communications.Api.Database.Accounts;

public sealed class AccountsDbContext(DbContextOptions<AccountsDbContext> options)
    : IdentityDbContext<ApplicationUser, IdentityRole<Guid>, Guid>(options)
{
    public DbSet<BootstrapState> BootstrapStates => Set<BootstrapState>();

    protected override void OnModelCreating(ModelBuilder modelBuilder)
    {
        base.OnModelCreating(modelBuilder);
        modelBuilder.HasDefaultSchema("accounts");
        modelBuilder.Entity<ApplicationUser>(entity =>
        {
            entity.Property(user => user.DisplayName).HasMaxLength(200).IsRequired();
        });
        modelBuilder.Entity<BootstrapState>(entity =>
        {
            entity.ToTable("bootstrap_states");
            entity.HasKey(state => state.Id).HasName("pk_bootstrap_states");
        });
    }
}