using Vantigo.Communications.Infrastructure.Storage;
using Vantigo.Storage.Abstractions;

namespace Vantigo.Communications.Module.Tests.Integration;

public sealed class StorageScopeTests
{
    private sealed class OtherStorageScope : IStorageScope
    {
        public static string Name => "other";
    }

    [Fact]
    public async Task Typed_scopes_isolate_physical_keys_while_callers_use_relative_keys()
    {
        var physical = new InMemoryObjectStore();
        var communications = physical.CommunicationsStore;
        var other = new TestScopedObjectStore<OtherStorageScope>(physical);

        await communications.PutAsync("same-key", new MemoryStream("communications"u8.ToArray()), "text/plain");
        await other.PutAsync("same-key", new MemoryStream("other"u8.ToArray()), "text/plain");

        Assert.Contains("communications/same-key", physical.PhysicalKeys);
        Assert.Contains("other/same-key", physical.PhysicalKeys);
        Assert.DoesNotContain("communications/communications/same-key", physical.PhysicalKeys);
        Assert.Equal("communications", await ReadAsync(await communications.GetAsync("same-key")));
        Assert.Equal("other", await ReadAsync(await other.GetAsync("same-key")));
    }

    private static async Task<string> ReadAsync(Stream? stream)
    {
        await using var content = stream;
        using var reader = new StreamReader(content!);
        return await reader.ReadToEndAsync();
    }
}