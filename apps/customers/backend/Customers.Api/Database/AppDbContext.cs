using Microsoft.EntityFrameworkCore;

using Vantigo.Customers.Api.Database.Entities;
using Vantigo.Customers.Api.Domain.Contacts;
using Vantigo.Customers.Api.Domain.Customers;

namespace Vantigo.Customers.Api.Database;

public sealed class AppDbContext(DbContextOptions<AppDbContext> options) : DbContext(options)
{
    public DbSet<Customer> Customers => Set<Customer>();
    public DbSet<Contact> Contacts => Set<Contact>();
    public DbSet<CustomerContact> CustomersContacts => Set<CustomerContact>();

    protected override void OnModelCreating(ModelBuilder modelBuilder)
    {
        // Configurations are applied explicitly (rather than scanned from the assembly)
        // to stay trimming- and NativeAOT-friendly.
        modelBuilder.ApplyConfiguration(new CustomerEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new ContactEntityTypeConfiguration());
        modelBuilder.ApplyConfiguration(new CustomerContactEntityTypeConfiguration());
    }
}