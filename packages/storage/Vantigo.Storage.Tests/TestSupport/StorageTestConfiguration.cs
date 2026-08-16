using Microsoft.Extensions.Configuration;

namespace Vantigo.Storage.Tests.TestSupport;

internal static class StorageTestConfiguration
{
    public static IConfiguration Build(params (string Key, string? Value)[] values) =>
        new ConfigurationBuilder()
            .AddInMemoryCollection(values.ToDictionary(value => value.Key, value => value.Value))
            .Build();
}