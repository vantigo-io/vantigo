using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Infrastructure;

using Vantigo.Customers.Database.Customers.Configurations;
using Vantigo.Customers.Domain.Contacts;
using Vantigo.Customers.Domain.Customers;
using Vantigo.Customers.Domain.Timeline;
using Vantigo.Tenancy.Abstractions;
using Vantigo.Tenancy.EntityFramework;

namespace Vantigo.Customers.Database.Customers;

public sealed class CustomersDbContext(
    DbContextOptions<CustomersDbContext> options,
    ITenantContext? tenantContext = null) : DbContext(options)
{
    internal ITenantContext TenantContext => _tenantContext;

    private readonly ITenantContext _tenantContext = tenantContext ?? new UnresolvedTenantContext();

    public DbSet<Customer> Customers => Set<Customer>();
    public DbSet<Contact> Contacts => Set<Contact>();
    public DbSet<CustomerContact> CustomersContacts => Set<CustomerContact>();
    public DbSet<CustomerTimelineEntry> CustomerTimelineEntries => Set<CustomerTimelineEntry>();
    public DbSet<CustomerTimelineEntryRevision> CustomerTimelineEntryRevisions => Set<CustomerTimelineEntryRevision>();

    protected override void OnModelCreating(ModelBuilder modelBuilder)
    {
        modelBuilder.HasDefaultSchema("customers");

        // Configurations are applied explicitly (rather than scanned from the assembly)
        // to stay trimming- and NativeAOT-friendly.
        modelBuilder.ApplyConfiguration(new CustomerEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new ContactEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new CustomerContactEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new CustomerTimelineEntryEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new CustomerTimelineEntryRevisionEntityTypeConfiguration());
        modelBuilder.ApplyTenantOwnership(_tenantContext);
    }

    private sealed class UnresolvedTenantContext : ITenantContext
    {
        public bool IsResolved => false;

        public TenantId Current => throw new TenantUnresolvedException();
    }
}

internal sealed class CustomersModelCacheKeyFactory : IModelCacheKeyFactory
{
    public object Create(DbContext context, bool designTime) =>
        context is CustomersDbContext customers
            ? (context.GetType(), customers.TenantContext, designTime)
            : (context.GetType(), designTime);

    public object Create(DbContext context) => Create(context, false);
}