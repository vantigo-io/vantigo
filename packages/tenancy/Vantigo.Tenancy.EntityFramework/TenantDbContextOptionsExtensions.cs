using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;

namespace Vantigo.Tenancy.EntityFramework;

/// <summary>
/// EF Core options helpers for Vantigo tenancy.
/// </summary>
public static class TenantDbContextOptionsExtensions
{
    /// <summary>
    /// Adds tenant stamping and transaction-local GUC interceptors to a DbContext.
    /// SaveChanges uses an EF-created transaction, while queries that rely on RLS
    /// must run inside an explicit transaction (or another EF-created transaction).
    /// </summary>
    public static DbContextOptionsBuilder UseTenancy(
        this DbContextOptionsBuilder optionsBuilder,
        IServiceProvider serviceProvider)
    {
        ArgumentNullException.ThrowIfNull(optionsBuilder);
        ArgumentNullException.ThrowIfNull(serviceProvider);

        optionsBuilder.AddInterceptors(
            serviceProvider.GetRequiredService<TenantStampingInterceptor>(),
            serviceProvider.GetRequiredService<TenantConnectionInterceptor>());
        return optionsBuilder;
    }
}