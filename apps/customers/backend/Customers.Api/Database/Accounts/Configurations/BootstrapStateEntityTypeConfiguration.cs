using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Metadata.Builders;

using Vantigo.Customers.Api.Database.Accounts;

namespace Vantigo.Customers.Api.Database.Accounts.Configurations;

internal sealed class BootstrapStateEntityTypeConfiguration : IEntityTypeConfiguration<BootstrapState>
{
    public void Configure(EntityTypeBuilder<BootstrapState> builder)
    {
        builder.ToTable("bootstrap_states", "accounts");
        builder.HasKey(state => state.Id).HasName("pk_bootstrap_states");
        builder.Property(state => state.Id).HasColumnName("id");
        builder.Property(state => state.CompletedAt).HasColumnName("completed_at").IsRequired();
    }
}
