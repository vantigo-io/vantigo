using Microsoft.EntityFrameworkCore;

using Vantigo.Tenancy.Abstractions;
using Vantigo.Tenancy.EntityFramework;

namespace Vantigo.Tenancy.Tests;

public sealed class TenantStampingInterceptorTests
{
    [Fact]
    public void Added_entity_without_tenant_id_is_stamped()
    {
        var tenant = TenantId.New();
        var context = new TestTenantContext(tenant);
        using var db = CreateDbContext(context);
        var entity = new TestTenantEntity { Id = Guid.NewGuid(), Name = "new" };

        db.Entities.Add(entity);
        db.SaveChanges();

        Assert.Equal(tenant.Value, entity.TenantId);
    }

    [Fact]
    public void Modified_entity_from_another_tenant_is_rejected()
    {
        var entityTenant = TenantId.New();
        var currentTenant = TenantId.New();
        var context = new TestTenantContext(entityTenant);
        using (var seedDb = CreateDbContext(context))
        {
            seedDb.Entities.Add(new TestTenantEntity
            {
                Id = Guid.NewGuid(),
                Name = "existing",
                TenantId = entityTenant.Value,
            });
            seedDb.SaveChanges();
        }

        context.Set(currentTenant);
        using var db = CreateDbContext(context);
        var entity = new TestTenantEntity
        {
            Id = Guid.NewGuid(),
            Name = "existing",
            TenantId = entityTenant.Value,
        };
        db.Entities.Attach(entity);
        entity.Name = "changed";

        Assert.Throws<InvalidOperationException>(() => db.SaveChanges());
    }

    private static TestDbContext CreateDbContext(ITenantContext tenantContext) =>
        new(new DbContextOptionsBuilder<TestDbContext>()
            .UseInMemoryDatabase("stamping-" + Guid.NewGuid())
            .AddInterceptors(new TenantStampingInterceptor(tenantContext))
            .Options, tenantContext);

    private sealed class TestDbContext(
        DbContextOptions<TestDbContext> options,
        ITenantContext tenantContext) : DbContext(options)
    {
        public DbSet<TestTenantEntity> Entities => Set<TestTenantEntity>();

        protected override void OnModelCreating(ModelBuilder modelBuilder) =>
            modelBuilder.ApplyTenantOwnership(tenantContext);
    }

    private sealed class TestTenantEntity : ITenantOwned
    {
        public Guid Id { get; set; }
        public string Name { get; set; } = "";
        public Guid TenantId { get; set; }
    }

    private sealed class TestTenantContext(TenantId tenant) : ITenantContext
    {
        public bool IsResolved => !Tenant.IsEmpty;
        public TenantId Current => IsResolved ? Tenant : throw new TenantUnresolvedException();
        private TenantId Tenant { get; set; } = tenant;
        public void Set(TenantId value) => Tenant = value;
    }
}