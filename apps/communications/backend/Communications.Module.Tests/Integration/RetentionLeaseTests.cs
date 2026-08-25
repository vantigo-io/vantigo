using Microsoft.Extensions.DependencyInjection;

using Npgsql;

using Vantigo.Communications.Services;

namespace Vantigo.Communications.Module.Tests.Integration;

/// <summary>
/// Retention must run on exactly one replica per cycle: the advisory lease
/// makes a concurrent second run a no-op instead of a double-processing race.
/// </summary>
[Collection(CommunicationsModuleCollection.Name)]
public sealed class RetentionLeaseTests(CommunicationsModuleFactory factory)
{
    [Fact]
    public async Task A_concurrent_second_lease_holder_skips_instead_of_double_running()
    {
        var dataSource = factory.Services.GetRequiredService<NpgsqlDataSource>();
        var executions = 0;
        var firstIsRunning = new TaskCompletionSource();
        var releaseFirst = new TaskCompletionSource();

        var first = CommunicationsAdvisoryLease.TryRunAsync(
            dataSource,
            CommunicationsAdvisoryLease.RetentionKey,
            async () =>
            {
                Interlocked.Increment(ref executions);
                firstIsRunning.SetResult();
                await releaseFirst.Task;
            },
            CancellationToken.None);

        await firstIsRunning.Task;
        var secondRan = await CommunicationsAdvisoryLease.TryRunAsync(
            dataSource,
            CommunicationsAdvisoryLease.RetentionKey,
            () =>
            {
                Interlocked.Increment(ref executions);
                return Task.CompletedTask;
            },
            CancellationToken.None);

        releaseFirst.SetResult();
        Assert.True(await first);
        Assert.False(secondRan);
        Assert.Equal(1, executions);

        // Once released, the lease is available again.
        Assert.True(await CommunicationsAdvisoryLease.TryRunAsync(
            dataSource,
            CommunicationsAdvisoryLease.RetentionKey,
            () => Task.CompletedTask,
            CancellationToken.None));
    }
}