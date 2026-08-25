using System.Data.Common;

using Microsoft.AspNetCore.Http;
using Microsoft.EntityFrameworkCore;
using Microsoft.EntityFrameworkCore.Diagnostics;
using Microsoft.Extensions.DependencyInjection;

using Vantigo.Contracts.Authorization;
using Vantigo.Identity.Authorization;
using Vantigo.Identity.Database.Accounts;
using Vantigo.Identity.Services;

namespace Vantigo.Identity.Tests.Integration;

/// <summary>
/// Listing access groups must stay a fixed number of database round-trips no
/// matter how many groups exist — the old per-group detail loading made the
/// control plane O(N) on a hot path.
/// </summary>
[Collection(IdentityApiCollection.Name)]
public sealed class AccessGroupQueryCountTests(IdentityApiFactory factory)
{
    [Fact]
    public async Task Group_listing_query_count_does_not_grow_with_the_number_of_groups()
    {
        var counter = new CountingCommandInterceptor();
        var options = new DbContextOptionsBuilder<AccountsDbContext>()
            .UseNpgsql(factory.RuntimeConnectionString, npgsql => npgsql.MigrationsHistoryTable("__EFMigrationsHistory", "identity"))
            .AddInterceptors(counter)
            .Options;
        await using var db = new AccountsDbContext(options);
        var catalog = factory.Services.GetRequiredService<IPermissionCatalog>();
        var service = new AccessGroupManagementService(
            db, catalog, new AuthorizationMutationService(db, catalog), new AuthorizationAuditWriter(), new HttpContextAccessor());

        await CreateGroupsAsync(db, 2);
        var withFewGroups = await CountListingQueriesAsync(service, counter);

        await CreateGroupsAsync(db, 8);
        var withManyGroups = await CountListingQueriesAsync(service, counter);

        Assert.Equal(withFewGroups, withManyGroups);
    }

    private async Task CreateGroupsAsync(AccountsDbContext db, int count)
    {
        for (var index = 0; index < count; index++)
        {
            var group = new AccessGroup
            {
                DisplayName = $"Query count group {Guid.NewGuid():N}",
                Source = AccessGroupSource.Local,
                IsActive = true,
                CreatedAt = DateTimeOffset.UtcNow,
                UpdatedAt = DateTimeOffset.UtcNow,
                ConcurrencyStamp = Guid.NewGuid().ToString("N"),
            };
            db.AccessGroups.Add(group);
            db.AccessGroupMemberships.Add(new AccessGroupMembership
            {
                GroupId = group.Id,
                UserId = factory.OwnerId,
                Source = AccessGroupSource.Local,
                Override = AccessGroupMembershipOverride.ForceMember,
                UpdatedAt = DateTimeOffset.UtcNow,
            });
        }

        await db.SaveChangesAsync();
        db.ChangeTracker.Clear();
    }

    private static async Task<int> CountListingQueriesAsync(AccessGroupManagementService service, CountingCommandInterceptor counter)
    {
        counter.Reset();
        var groups = await service.ListAsync(CancellationToken.None);
        var details = await service.DetailsForManyAsync(groups, CancellationToken.None);
        Assert.Equal(groups.Count, details.Count);
        return counter.Count;
    }

    private sealed class CountingCommandInterceptor : DbCommandInterceptor
    {
        private int _count;

        public int Count => Volatile.Read(ref _count);

        public void Reset() => Volatile.Write(ref _count, 0);

        public override InterceptionResult<DbDataReader> ReaderExecuting(
            DbCommand command, CommandEventData eventData, InterceptionResult<DbDataReader> result)
        {
            Interlocked.Increment(ref _count);
            return result;
        }

        public override ValueTask<InterceptionResult<DbDataReader>> ReaderExecutingAsync(
            DbCommand command, CommandEventData eventData, InterceptionResult<DbDataReader> result,
            CancellationToken cancellationToken = default)
        {
            Interlocked.Increment(ref _count);
            return ValueTask.FromResult(result);
        }
    }
}