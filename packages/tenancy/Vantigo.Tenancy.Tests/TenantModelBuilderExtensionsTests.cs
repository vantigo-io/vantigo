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

    [Fact]
    public void Query_filters_are_evaluated_per_context_instance_not_per_compiled_query()
    {
        var tenantA = TenantId.New();
        var tenantB = TenantId.New();
        var databaseName = "per-context-" + Guid.NewGuid();

        using (var seedDb = CreateDbContext(databaseName, new MutableTenantContext(tenantA)))
        {
            seedDb.Orders.Add(new TestOrder { Id = 1, TenantId = tenantA.Value });
            seedDb.Orders.Add(new TestOrder { Id = 2, TenantId = tenantB.Value });
            seedDb.SaveChanges();
        }

        // The same query shape runs for tenant A first, so its compiled plan is
        // cached; tenant B's context must still see tenant B's rows. A filter
        // capturing anything but the DbContext instance bakes tenant A into the
        // cached plan and silently serves it to every later tenant.
        using (var dbA = CreateDbContext(databaseName, new MutableTenantContext(tenantA)))
        {
            Assert.Equal([1], dbA.Orders.AsNoTracking().Select(order => order.Id).ToArray());
        }

        using (var dbB = CreateDbContext(databaseName, new MutableTenantContext(tenantB)))
        {
            Assert.Equal([2], dbB.Orders.AsNoTracking().Select(order => order.Id).ToArray());
        }
    }

    private static FilterDbContext CreateDbContext(string databaseName, ITenantContext tenantContext) =>
        new(new DbContextOptionsBuilder<FilterDbContext>()
            .UseInMemoryDatabase(databaseName)
            .Options, tenantContext);

    private sealed class FilterDbContext(
        DbContextOptions<FilterDbContext> options,
        ITenantContext tenantContext) : DbContext(options), ITenantDbContext
    {
        private readonly ITenantContext _tenantContext = tenantContext;

        public Guid CurrentTenantId => _tenantContext.Current.Value;

        public DbSet<TestOrder> Orders => Set<TestOrder>();
        public DbSet<TestNote> Notes => Set<TestNote>();

        protected override void OnModelCreating(ModelBuilder modelBuilder)
            => modelBuilder.ApplyTenantOwnership(this);
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