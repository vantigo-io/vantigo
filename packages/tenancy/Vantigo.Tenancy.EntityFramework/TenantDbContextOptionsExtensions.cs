using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;

namespace Vantigo.Tenancy.EntityFramework;

/// <summary>
/// EF Core options helpers for Vantigo tenancy.
/// </summary>
public static class TenantDbContextOptionsExtensions
{
    /// <summary>
    /// Adds the tenant stamping and connection interceptors to a DbContext. The
    /// connection interceptor applies the session-scoped tenant setting on every
    /// connection open, so RLS covers transaction-less reads and transactional
    /// work alike. See docs/tenancy.md.
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