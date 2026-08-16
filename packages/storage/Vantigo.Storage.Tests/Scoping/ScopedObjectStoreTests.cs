using Vantigo.Storage.Abstractions;
using Vantigo.Storage.Initialization;
using Vantigo.Storage.Scoping;

using Vantigo.Storage.Tests.TestSupport;
using Vantigo.Tenancy.Abstractions;

namespace Vantigo.Storage.Tests.Scoping;

public sealed class ScopedObjectStoreTests
{
    [Fact]
    public async Task Marker_types_produce_isolated_prefixes_and_reject_already_scoped_keys()
    {
        var backend = new RecordingBackend();
        var communications = new ScopedObjectStore<CommunicationsScope>(backend);
        var customers = new ScopedObjectStore<CustomerScope>(backend);

        await communications.PutAsync("inbox/message.eml", Stream.Null, "message/rfc822");
        await customers.PutAsync("inbox/message.eml", Stream.Null, "message/rfc822");

        Assert.Equal(
            ["communications/inbox/message.eml", "customers/inbox/message.eml"],
            backend.Keys);
        await Assert.ThrowsAsync<ArgumentException>(() => communications.ExistsAsync("communications/file"));
        await Assert.ThrowsAsync<ArgumentException>(() => customers.ExistsAsync("customers/file"));
    }

    [Fact]
    public async Task Tenant_scoped_crud_uses_the_exact_tenant_and_module_layout()
    {
        var tenant = new TenantId(Guid.Parse("11111111-1111-1111-1111-111111111111"));
        var context = new TestTenantContext(tenant);
        var backend = new RecordingBackend();
        IObjectStore store = new TenantScopedObjectStore<CommunicationsScope>(context, backend);

        await store.PutAsync("inbox/message.eml", Stream.Null, "message/rfc822");
        await store.GetAsync("inbox/message.eml");
        await store.ExistsAsync("inbox/message.eml");
        await store.DeleteAsync("inbox/message.eml");

        Assert.Equal(
            [
                "tenants/11111111-1111-1111-1111-111111111111/communications/inbox/message.eml",
                "tenants/11111111-1111-1111-1111-111111111111/communications/inbox/message.eml",
                "tenants/11111111-1111-1111-1111-111111111111/communications/inbox/message.eml",
                "tenants/11111111-1111-1111-1111-111111111111/communications/inbox/message.eml",
            ],
            backend.Keys);
    }

    [Fact]
    public async Task A_tenant_cannot_read_another_tenants_key()
    {
        var tenantA = new TenantId(Guid.Parse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"));
        var tenantB = new TenantId(Guid.Parse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"));
        var context = new TestTenantContext(tenantA);
        var backend = new RecordingBackend();
        IObjectStore store = new TenantScopedObjectStore<CommunicationsScope>(context, backend);

        await store.PutAsync("file", Stream.Null, "application/octet-stream");
        context.Set(tenantB);

        Assert.False(await store.ExistsAsync("file"));
        Assert.Equal(
            "tenants/bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb/communications/file",
            backend.Keys[^1]);
    }

    [Fact]
    public async Task Unresolved_tenant_fails_closed_before_touching_the_inner_store()
    {
        var context = new TestTenantContext(new TenantId());
        var inner = new RecordingBackend();
        var store = new TenantScopedObjectStore(context, inner);

        await Assert.ThrowsAsync<TenantUnresolvedException>(() => store.PutAsync("file", Stream.Null, "text/plain"));
        await Assert.ThrowsAsync<TenantUnresolvedException>(() => store.GetAsync("file"));
        await Assert.ThrowsAsync<TenantUnresolvedException>(() => store.ExistsAsync("file"));
        await Assert.ThrowsAsync<TenantUnresolvedException>(() => store.DeleteAsync("file"));

        Assert.Empty(inner.Keys);
    }

    private sealed class RecordingBackend : IStorageBackend
    {
        public List<string> Keys { get; } = [];
        public Task PutAsync(string key, Stream content, string contentType, CancellationToken cancellationToken = default)
        {
            Keys.Add(key);
            return Task.CompletedTask;
        }

        public Task<Stream?> GetAsync(string key, CancellationToken cancellationToken = default)
        {
            Keys.Add(key);
            return Task.FromResult<Stream?>(null);
        }

        public Task<bool> ExistsAsync(string key, CancellationToken cancellationToken = default)
        {
            Keys.Add(key);
            return Task.FromResult(false);
        }

        public Task DeleteAsync(string key, CancellationToken cancellationToken = default)
        {
            Keys.Add(key);
            return Task.CompletedTask;
        }
    }
}