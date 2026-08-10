using Microsoft.EntityFrameworkCore;

using Vantigo.Customers.Database.Customers.Configurations;
using Vantigo.Customers.Domain.Contacts;
using Vantigo.Customers.Domain.Customers;
using Vantigo.Customers.Domain.Timeline;

namespace Vantigo.Customers.Database.Customers;

public sealed class CustomersDbContext(DbContextOptions<CustomersDbContext> options) : DbContext(options)
{
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
    }
}