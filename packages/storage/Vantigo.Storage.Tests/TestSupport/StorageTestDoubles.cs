using Microsoft.Extensions.FileProviders;
using Microsoft.Extensions.Hosting;

using Vantigo.Storage.Abstractions;
using Vantigo.Tenancy.Abstractions;

namespace Vantigo.Storage.Tests.TestSupport;

internal sealed class TestHostEnvironment : IHostEnvironment
{
    public TestHostEnvironment() : this(Environments.Development)
    {
    }

    public TestHostEnvironment(string environmentName)
    {
        EnvironmentName = environmentName;
    }

    public string EnvironmentName { get; set; }
    public string ApplicationName { get; set; } = "storage-tests";
    public string ContentRootPath { get; set; } = AppContext.BaseDirectory;
    public IFileProvider ContentRootFileProvider { get; set; } = new NullFileProvider();
}

internal sealed class CommunicationsScope : IStorageScope
{
    public static string Name => "communications";
}

internal sealed class CustomerScope : IStorageScope
{
    public static string Name => "customers";
}

internal sealed class InvalidScope : IStorageScope
{
    public static string Name => "Communications";
}

internal sealed class TestTenantContext(TenantId tenantId) : ITenantContext
{
    public bool IsResolved => !tenantId.IsEmpty;

    public TenantId Current => IsResolved
        ? tenantId
        : throw new TenantUnresolvedException();

    public void Set(TenantId value) => tenantId = value;
}