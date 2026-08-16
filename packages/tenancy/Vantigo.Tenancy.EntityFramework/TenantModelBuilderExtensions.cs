using System.Linq.Expressions;
using System.Reflection;

using Microsoft.EntityFrameworkCore;

using Vantigo.Tenancy.Abstractions;

namespace Vantigo.Tenancy.EntityFramework;

/// <summary>
/// Adds tenant ownership conventions to an EF Core model.
/// </summary>
public static class TenantModelBuilderExtensions
{
    private static readonly MethodInfo ApplyTenantOwnershipMethod =
        typeof(TenantModelBuilderExtensions).GetMethod(
            nameof(ApplyTenantOwnershipForEntity),
            BindingFlags.NonPublic | BindingFlags.Static)!;

    /// <summary>
    /// Requires a tenant id and adds a tenant query filter to every entity that
    /// implements <see cref="ITenantOwned"/>. The context instance is captured,
    /// rather than its current value, so the tenant is resolved when each query
    /// is evaluated.
    /// </summary>
    public static ModelBuilder ApplyTenantOwnership(
        this ModelBuilder modelBuilder,
        ITenantContext tenantContext)
    {
        ArgumentNullException.ThrowIfNull(modelBuilder);
        ArgumentNullException.ThrowIfNull(tenantContext);

        foreach (var entityType in modelBuilder.Model.GetEntityTypes()
                     .Where(entityType => typeof(ITenantOwned).IsAssignableFrom(entityType.ClrType)))
        {
            ApplyTenantOwnershipMethod
                .MakeGenericMethod(entityType.ClrType)
                .Invoke(null, [modelBuilder, tenantContext]);
        }

        return modelBuilder;
    }

    private static void ApplyTenantOwnershipForEntity<TEntity>(
        ModelBuilder modelBuilder,
        ITenantContext tenantContext)
        where TEntity : class, ITenantOwned
    {
        var entityBuilder = modelBuilder.Entity<TEntity>();
        entityBuilder.Property(entity => entity.TenantId).IsRequired();

        // Capturing the context object (not Current.Value) lets EF parameterize
        // the property access and evaluate the active tenant for each query.
        Expression<Func<TEntity, bool>> filter =
            entity => entity.TenantId == tenantContext.Current.Value;
        entityBuilder.HasQueryFilter(filter);
    }

}