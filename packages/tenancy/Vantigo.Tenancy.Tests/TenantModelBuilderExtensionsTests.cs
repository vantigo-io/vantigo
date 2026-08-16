using Microsoft.EntityFrameworkCore;

using Vantigo.Tenancy.Abstractions;
using Vantigo.Tenancy.EntityFramework;

namespace Vantigo.Tenancy.Tests;

public sealed class TenantModelBuilderExtensionsTests
{
    [Fact]
    public void Query_filters_follow_the_current_tenant_for_each_owned_entity_type()
    {
        var first = TenantId.New();
        var context = new MutableTenantContext(first);
        var databaseName = "filters-" + Guid.NewGuid();

        using (var seedDb = CreateDbContext(databaseName, context))
        {
            seedDb.Orders.Add(new TestOrder { Id = 1, TenantId = first.Value });
            seedDb.Notes.Add(new TestNote { Id = 1, TenantId = first.Value });
            seedDb.Orders.Add(new TestOrder { Id = 2, TenantId = Guid.NewGuid() });
            seedDb.Notes.Add(new TestNote { Id = 2, TenantId = Guid.NewGuid() });
            seedDb.SaveChanges();
        }

        using var db = CreateDbContext(databaseName, context);
        Assert.Equal([1], db.Orders.AsNoTracking().Select(order => order.Id).ToArray());
        Assert.Equal([1], db.Notes.AsNoTracking().Select(note => note.Id).ToArray());
    }

    private static FilterDbContext CreateDbContext(string databaseName, ITenantContext tenantContext) =>
        new(new DbContextOptionsBuilder<FilterDbContext>()
            .UseInMemoryDatabase(databaseName)
            .Options, tenantContext);

    private sealed class FilterDbContext(
        DbContextOptions<FilterDbContext> options,
        ITenantContext tenantContext) : DbContext(options)
    {
        private readonly ITenantContext _tenantContext = tenantContext;
        public DbSet<TestOrder> Orders => Set<TestOrder>();
        public DbSet<TestNote> Notes => Set<TestNote>();

        protected override void OnModelCreating(ModelBuilder modelBuilder)
            => modelBuilder.ApplyTenantOwnership(_tenantContext);
    }

    private sealed class TestOrder : ITenantOwned
    {
        public int Id { get; set; }
        public Guid TenantId { get; set; }
    }

    private sealed class TestNote : ITenantOwned
    {
        public int Id { get; set; }
        public Guid TenantId { get; set; }
    }

    private sealed class MutableTenantContext(TenantId tenant) : ITenantContext
    {
        public bool IsResolved => !Tenant.IsEmpty;
        public TenantId Current => IsResolved ? Tenant : throw new TenantUnresolvedException();
        private TenantId Tenant { get; set; } = tenant;
        public void Set(TenantId value) => Tenant = value;
    }
}