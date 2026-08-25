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
    /// implements <see cref="ITenantOwned"/>. The filter references the tenant
    /// through the <paramref name="context"/> instance, which EF Core rewrites
    /// to the executing context and evaluates per query as a parameter. Any
    /// other capture (a tenant context service, an ambient accessor) would be
    /// baked into the cached query plan as a constant — every later query of
    /// the same shape would silently run with the first tenant's id.
    /// </summary>
    public static ModelBuilder ApplyTenantOwnership<TContext>(
        this ModelBuilder modelBuilder,
        TContext context)
        where TContext : DbContext, ITenantDbContext
    {
        ArgumentNullException.ThrowIfNull(modelBuilder);
        ArgumentNullException.ThrowIfNull(context);

        var currentTenantProperty = context.GetType().GetProperty(
            nameof(ITenantDbContext.CurrentTenantId),
            BindingFlags.Public | BindingFlags.Instance)
            ?? throw new InvalidOperationException(
                $"{context.GetType().Name} must implement {nameof(ITenantDbContext)}.{nameof(ITenantDbContext.CurrentTenantId)} as a public property.");

        foreach (var entityType in modelBuilder.Model.GetEntityTypes()
                     .Where(entityType => typeof(ITenantOwned).IsAssignableFrom(entityType.ClrType)))
        {
            ApplyTenantOwnershipMethod
                .MakeGenericMethod(entityType.ClrType)
                .Invoke(null, [modelBuilder, context, currentTenantProperty]);
        }

        return modelBuilder;
    }

    private static void ApplyTenantOwnershipForEntity<TEntity>(
        ModelBuilder modelBuilder,
        DbContext context,
        PropertyInfo currentTenantProperty)
        where TEntity : class, ITenantOwned
    {
        var entityBuilder = modelBuilder.Entity<TEntity>();
        entityBuilder.Property(entity => entity.TenantId).IsRequired();

        // entity => entity.TenantId == <context>.CurrentTenantId, with the
        // context appearing as a constant of its own type so EF's query-filter
        // rewriting convention re-binds it to the executing context instance.
        var parameter = Expression.Parameter(typeof(TEntity), "entity");
        var filter = Expression.Lambda<Func<TEntity, bool>>(
            Expression.Equal(
                Expression.Property(parameter, nameof(ITenantOwned.TenantId)),
                Expression.Property(Expression.Constant(context), currentTenantProperty)),
            parameter);
        entityBuilder.HasQueryFilter(filter);
    }
}