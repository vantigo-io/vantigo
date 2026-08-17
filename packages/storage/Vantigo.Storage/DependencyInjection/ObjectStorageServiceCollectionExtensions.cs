using Azure.Core;

using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.DependencyInjection.Extensions;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Options;

using Vantigo.Configuration;
using Vantigo.Storage.Abstractions;
using Vantigo.Storage.Initialization;
using Vantigo.Storage.Providers.AzureBlob;
using Vantigo.Storage.Providers.Local;
using Vantigo.Storage.Providers.S3;
using Vantigo.Storage.Scoping;
using Vantigo.Tenancy.Abstractions;

namespace Vantigo.Storage;

public static class ObjectStorageServiceCollectionExtensions
{
    /// <summary>
    /// Registers the configured provider and only the marker-type scoped stores for app
    /// consumers. If Storage is not configured, resolution remains available but all
    /// actual storage operations fail closed with a clear error.
    /// </summary>
    public static IServiceCollection AddVantigoObjectStorage(this IServiceCollection services, IConfiguration configuration)
    {
        if (!services.Any(descriptor => descriptor.ServiceType == typeof(IConfigureOptions<StorageOptions>)))
            services.AddStorageOptions(configuration);
        services.TryAddSingleton<ITenantContext, UnresolvedTenantContext>();
        services.AddSingleton<IStorageBackend>(provider =>
        {
            ValidateAzureIdentityConfiguration(configuration);
            var options = provider.GetRequiredService<IOptions<StorageOptions>>().Value;
            IStorageBackend backend = options.IsConfigured
                ? new StorageBackendAdapter(CreateConfiguredBackend(options, provider))
                : new UnavailableObjectStore("Object storage is unavailable because the Storage:Provider configuration is not configured.");
            return backend;
        });
        services.AddSingleton(typeof(IObjectStore<>), typeof(TenantScopedObjectStore<>));
        services.AddHostedService<ObjectStorageStartupInitializer>();
        return services;
    }

    private static IObjectStore CreateConfiguredBackend(StorageOptions options, IServiceProvider services) => options.Provider switch
    {
        "s3" => new S3ObjectStore(Options.Create(options)),
        "azure-blob" => new AzureBlobObjectStore(
            Options.Create(options),
            options.Authentication == "azure-identity"
                ? services.GetService<TokenCredential>() ?? throw StorageOptions.Invalid(
                    "Azure Blob azure-identity authentication requires the global Azure identity TokenCredential. " +
                    "Register AddVantigoAzureIdentity with identity enabled, or choose connection-string or sas authentication.")
                : null),
        "local" => new LocalFileObjectStore(
            Options.Create(options),
            services.GetRequiredService<IHostEnvironment>()),
        _ => throw StorageOptions.Invalid("Provider must be one of s3, azure-blob, or local.")
    };

    private static void ValidateAzureIdentityConfiguration(IConfiguration configuration)
    {
        if (!string.Equals(configuration["Storage:Provider"]?.Trim(), "azure-blob", StringComparison.OrdinalIgnoreCase) ||
            !string.Equals(configuration["Storage:Authentication"]?.Trim(), "azure-identity", StringComparison.OrdinalIgnoreCase))
            return;

        var enabled = configuration["Azure:Identity:Enabled"] ?? configuration["AZURE__IDENTITY__ENABLED"];
        if (bool.TryParse(enabled, out var parsed) && !parsed)
            throw StorageOptions.Invalid(
                "Azure Blob azure-identity authentication requires Azure identity to be enabled. " +
                "Enable Azure identity with AZURE__IDENTITY__ENABLED=true or choose connection-string or sas authentication.");
    }

    private sealed class UnavailableObjectStore(string reason) : IStorageBackend
    {
        private InvalidOperationException Unavailable() => new(reason);
        public Task PutAsync(string key, Stream content, string contentType, CancellationToken cancellationToken = default) => Task.FromException(Unavailable());
        public Task<Stream?> GetAsync(string key, CancellationToken cancellationToken = default) => Task.FromException<Stream?>(Unavailable());
        public Task<bool> ExistsAsync(string key, CancellationToken cancellationToken = default) => Task.FromException<bool>(Unavailable());
        public Task DeleteAsync(string key, CancellationToken cancellationToken = default) => Task.FromException(Unavailable());
    }

    private sealed class UnresolvedTenantContext : ITenantContext
    {
        public bool IsResolved => false;
        public TenantId Current => throw new TenantUnresolvedException();
    }

    private sealed class ObjectStorageStartupInitializer(IStorageBackend backend) : IHostedService
    {
        public Task StartAsync(CancellationToken cancellationToken) =>
            backend is IStorageInitializer initializer
                ? initializer.InitializeAsync(cancellationToken)
                : Task.CompletedTask;
        public Task StopAsync(CancellationToken cancellationToken) => Task.CompletedTask;
    }
}