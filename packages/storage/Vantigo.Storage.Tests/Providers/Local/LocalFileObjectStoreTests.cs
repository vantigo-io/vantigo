using System.Runtime.InteropServices;
using System.Text;

using Microsoft.Extensions.FileProviders;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Options;

using Vantigo.Configuration;
using Vantigo.Storage.Abstractions;
using Vantigo.Storage.Initialization;
using Vantigo.Storage.Providers.Local;
using Vantigo.Storage.Scoping;

namespace Vantigo.Storage.Tests.Providers.Local;

public sealed class LocalFileObjectStoreTests : IAsyncLifetime
{
    private sealed class TestHostEnvironment(string environmentName) : IHostEnvironment
    {
        public string EnvironmentName { get; set; } = environmentName;
        public string ApplicationName { get; set; } = "storage-tests";
        public string ContentRootPath { get; set; } = AppContext.BaseDirectory;
        public IFileProvider ContentRootFileProvider { get; set; } = new NullFileProvider();
    }

    private sealed class CommunicationsScope : IStorageScope
    {
        public static string Name => "communications";
    }

    private readonly string root = Path.Combine(Path.GetTempPath(), $"vantigo-storage-{Guid.NewGuid():N}");
    private LocalFileObjectStore? store;

    public Task InitializeAsync()
    {
        store = new LocalFileObjectStore(new OptionsWrapper<StorageOptions>(new StorageOptions
        {
            Provider = "local",
            Authentication = "none",
            Local = new LocalStorageOptions { RootPath = root },
        }), new TestHostEnvironment(Environments.Development));
        return Task.CompletedTask;
    }

    public Task DisposeAsync()
    {
        if (Directory.Exists(root)) Directory.Delete(root, recursive: true);
        return Task.CompletedTask;
    }

    [Fact]
    public async Task Performs_scoped_crud_and_atomic_replacement()
    {
        IObjectStore scoped = new ScopedObjectStore<CommunicationsScope>(new StorageBackendAdapter(store!));
        await scoped.PutAsync("inbox/message.eml", new MemoryStream(Encoding.UTF8.GetBytes("first")), "message/rfc822");
        await scoped.PutAsync("inbox/message.eml", new MemoryStream(Encoding.UTF8.GetBytes("second")), "message/rfc822");

        await using var content = await scoped.GetAsync("inbox/message.eml");
        using var reader = new StreamReader(content!);
        Assert.Equal("second", await reader.ReadToEndAsync());
        Assert.True(await scoped.ExistsAsync("inbox/message.eml"));
        await scoped.DeleteAsync("inbox/message.eml");
        Assert.False(await scoped.ExistsAsync("inbox/message.eml"));
    }

    [Fact]
    public async Task Rejects_traversal_and_scope_prefix()
    {
        var scoped = new ScopedObjectStore<CommunicationsScope>(new StorageBackendAdapter(store!));
        await Assert.ThrowsAsync<ArgumentException>(() => scoped.GetAsync("../outside"));
        await Assert.ThrowsAsync<ArgumentException>(() => scoped.GetAsync("communications/file"));
    }

    [Fact]
    public async Task Rejects_symlink_escape_when_supported()
    {
        await store!.InitializeAsync();
        var outside = Path.Combine(Path.GetTempPath(), $"vantigo-outside-{Guid.NewGuid():N}");
        var link = Path.Combine(root, "link");
        try
        {
            Directory.CreateDirectory(outside);
            try { Directory.CreateSymbolicLink(link, outside); }
            catch (Exception) { return; }
            await Assert.ThrowsAnyAsync<Exception>(async () =>
            {
                await using var stream = await store.GetAsync("link/file");
            });
        }
        finally
        {
            if (Directory.Exists(link)) Directory.Delete(link);
            if (Directory.Exists(outside)) Directory.Delete(outside, recursive: true);
        }
    }

    [Fact]
    public async Task Rejects_group_writable_root_on_unix_by_default()
    {
        if (RuntimeInformation.IsOSPlatform(OSPlatform.Windows)) return;
        await store!.InitializeAsync();
        File.SetUnixFileMode(root, UnixFileMode.UserRead | UnixFileMode.UserWrite | UnixFileMode.UserExecute | UnixFileMode.OtherWrite);
        var second = new LocalFileObjectStore(new OptionsWrapper<StorageOptions>(new StorageOptions
        {
            Provider = "local",
            Authentication = "none",
            Local = new LocalStorageOptions { RootPath = root },
        }), new TestHostEnvironment(Environments.Development));
        await Assert.ThrowsAsync<InvalidOperationException>(() => second.InitializeAsync());
    }

    [Fact]
    public async Task Rejects_insecure_root_escape_hatch_outside_development()
    {
        var insecure = new LocalFileObjectStore(new OptionsWrapper<StorageOptions>(new StorageOptions
        {
            Provider = "local",
            Authentication = "none",
            Local = new LocalStorageOptions
            {
                RootPath = root,
                AllowInsecureRootForDevelopment = true,
            },
        }), new TestHostEnvironment(Environments.Production));

        var exception = await Assert.ThrowsAsync<InvalidOperationException>(() => insecure.InitializeAsync());

        Assert.Contains("AllowInsecureRootForDevelopment=true", exception.Message, StringComparison.Ordinal);
        Assert.Contains("only permitted in the Development environment", exception.Message, StringComparison.Ordinal);
        Assert.Contains(Environments.Production, exception.Message, StringComparison.Ordinal);
    }

    [Fact]
    public async Task Accepts_insecure_root_escape_hatch_in_development()
    {
        Directory.CreateDirectory(root);
        if (!RuntimeInformation.IsOSPlatform(OSPlatform.Windows))
        {
            File.SetUnixFileMode(root,
                UnixFileMode.UserRead | UnixFileMode.UserWrite | UnixFileMode.UserExecute | UnixFileMode.OtherWrite);
        }

        var insecure = new LocalFileObjectStore(new OptionsWrapper<StorageOptions>(new StorageOptions
        {
            Provider = "local",
            Authentication = "none",
            Local = new LocalStorageOptions
            {
                RootPath = root,
                AllowInsecureRootForDevelopment = true,
            },
        }), new TestHostEnvironment(Environments.Development));

        await insecure.InitializeAsync();
    }
}