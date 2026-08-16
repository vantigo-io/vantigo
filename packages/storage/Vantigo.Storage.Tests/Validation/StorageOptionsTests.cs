using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Options;

using Vantigo.Configuration;
using Vantigo.Storage.Validation;

namespace Vantigo.Storage.Tests.Validation;

public sealed class StorageOptionsTests
{
    [Theory]
    [InlineData("inbox/message.eml")]
    [InlineData("tenant-1/attachments/part-1")]
    public void Accepts_safe_keys(string key) => StorageKey.ValidateProviderKey(key);

    [Theory]
    [InlineData("/absolute")]
    [InlineData("../traversal")]
    [InlineData("tenant/../secret")]
    [InlineData("tenant/%2e%2e/secret")]
    [InlineData("tenant\\secret")]
    [InlineData("tenant/\u0001")]
    [InlineData("tenant//secret")]
    public void Rejects_unsafe_keys(string key) => Assert.Throws<ArgumentException>(() => StorageKey.ValidateProviderKey(key));

    [Theory]
    [InlineData("communications")]
    [InlineData("customers-1")]
    public void Accepts_safe_scopes(string scope) => StorageKey.ValidateScope(scope);

    [Theory]
    [InlineData("Communications")]
    [InlineData("communications/attachments")]
    [InlineData("../communications")]
    [InlineData("communications.foo")]
    public void Rejects_unsafe_scopes(string scope) => Assert.Throws<ArgumentException>(() => StorageKey.ValidateScope(scope));

    [Fact]
    public void Combines_scope_once_and_rejects_double_prefix()
    {
        Assert.Equal("communications/inbox/message.eml", StorageKey.Combine("communications", "inbox/message.eml"));
        Assert.Throws<ArgumentException>(() => StorageKey.Combine("communications", "communications/message.eml"));
    }

    [Fact]
    public void Requires_provider_authentication_matrix()
    {
        var options = new StorageOptions
        {
            Provider = "S3",
            Authentication = "access-key",
            S3 = new S3StorageOptions
            {
                BucketName = "vantigo-objects",
                ServiceUrl = "http://localhost:9000",
                AccessKey = "access",
                SecretKey = "secret",
            },
        };

        options.Validate();

        Assert.Equal("s3", options.Provider);
        Assert.Throws<InvalidOperationException>(() => new StorageOptions
        {
            Provider = "local",
            Authentication = "access-key",
            Local = new LocalStorageOptions { RootPath = "/tmp/vantigo" },
        }.Validate());
    }

    [Fact]
    public void Binds_documented_keys_and_rejects_unknown_fields()
    {
        var configuration = new ConfigurationBuilder().AddInMemoryCollection(new Dictionary<string, string?>
        {
            ["Storage:PROVIDER"] = "LOCAL",
            ["Storage:AUTHENTICATION"] = "NONE",
            ["Storage:LOCAL:ROOT_PATH"] = "/var/lib/vantigo/objects",
        }).Build();
        var services = new ServiceCollection();
        services.AddStorageOptions(configuration);
        using var provider = services.BuildServiceProvider();
        var options = provider.GetRequiredService<IOptions<StorageOptions>>().Value;

        Assert.Equal("local", options.Provider);
        Assert.Equal("none", options.Authentication);

        var invalid = new ConfigurationBuilder().AddInMemoryCollection(new Dictionary<string, string?>
        {
            ["Storage:Provider"] = "local",
            ["Storage:Authentication"] = "none",
            ["Storage:Local:RootPath"] = "/var/lib/vantigo/objects",
            ["Storage:Local:Unexpected"] = "value",
        }).Build();
        var invalidServices = new ServiceCollection();
        invalidServices.AddStorageOptions(invalid);
        using var invalidProvider = invalidServices.BuildServiceProvider();

        Assert.Throws<OptionsValidationException>(() => invalidProvider.GetRequiredService<IOptions<StorageOptions>>().Value);
    }

    [Fact]
    public void Binds_exact_azure_blob_configuration_hierarchy()
    {
        var configuration = new ConfigurationBuilder().AddInMemoryCollection(new Dictionary<string, string?>
        {
            ["Storage:Provider"] = "azure-blob",
            ["Storage:Authentication"] = "azure-identity",
            ["Storage:Azure_Blob:AccountName"] = "vantigoaccount",
            ["Storage:Azure_Blob:ContainerName"] = "objects",
        }).Build();
        var services = new ServiceCollection();
        services.AddStorageOptions(configuration);
        using var provider = services.BuildServiceProvider();
        var options = provider.GetRequiredService<IOptions<StorageOptions>>().Value;

        Assert.Equal("vantigoaccount", options.AzureBlob.AccountName);
        Assert.Equal("objects", options.AzureBlob.ContainerName);
    }

    [Theory]
    [InlineData("connection-string", "AzureBlob:SasToken must not be supplied for connection-string authentication")]
    [InlineData("sas", "AzureBlob:ConnectionString must not be supplied for sas authentication")]
    public void Rejects_credentials_for_the_wrong_azure_authentication_path(string authentication, string expected)
    {
        var options = new StorageOptions
        {
            Provider = "azure-blob",
            Authentication = authentication,
            AzureBlob = new AzureBlobStorageOptions
            {
                AccountName = "vantigoaccount",
                ContainerName = "objects",
                ConnectionString = "DefaultEndpointsProtocol=https;AccountName=secret-account;AccountKey=secret-key;EndpointSuffix=core.windows.net",
                SasToken = "sv=2024-01-01&sig=secret-signature",
            },
        };

        var exception = Assert.Throws<InvalidOperationException>(options.Validate);

        Assert.Contains(expected, exception.Message, StringComparison.Ordinal);
        Assert.DoesNotContain("secret-account", exception.Message, StringComparison.Ordinal);
        Assert.DoesNotContain("secret-key", exception.Message, StringComparison.Ordinal);
        Assert.DoesNotContain("secret-signature", exception.Message, StringComparison.Ordinal);
    }

    [Theory]
    [InlineData("s3", "access-key", "S3:SecretKey")]
    [InlineData("azure-blob", "connection-string", "AzureBlob:ConnectionString")]
    [InlineData("azure-blob", "sas", "AzureBlob:SasToken")]
    [InlineData("local", "none", "Local:RootPath")]
    public void Missing_required_values_are_actionable_and_secret_safe(string provider, string authentication, string expected)
    {
        var options = new StorageOptions
        {
            Provider = provider,
            Authentication = authentication,
            S3 = new S3StorageOptions
            {
                BucketName = provider == "s3" ? "vantigo-objects" : null,
                AccessKey = provider == "s3" ? "access" : null,
            },
            AzureBlob = new AzureBlobStorageOptions
            {
                AccountName = provider == "azure-blob" ? "vantigoaccount" : null,
                ContainerName = provider == "azure-blob" ? "objects" : null,
            },
            Local = new LocalStorageOptions
            {
                RootPath = null,
            },
        };

        var exception = Assert.Throws<InvalidOperationException>(options.Validate);

        Assert.Contains(expected, exception.Message, StringComparison.Ordinal);
        Assert.DoesNotContain("secret-key", exception.Message, StringComparison.OrdinalIgnoreCase);
        Assert.DoesNotContain("secret-signature", exception.Message, StringComparison.OrdinalIgnoreCase);
    }

    [Fact]
    public void Binds_environment_variable_azure_hierarchy()
    {
        var variables = new Dictionary<string, string>
        {
            ["STORAGE__PROVIDER"] = "azure-blob",
            ["STORAGE__AUTHENTICATION"] = "azure-identity",
            ["STORAGE__AZURE_BLOB__ACCOUNT_NAME"] = "envaccount",
            ["STORAGE__AZURE_BLOB__CONTAINER_NAME"] = "envobjects",
        };
        var previous = variables.ToDictionary(pair => pair.Key, pair => Environment.GetEnvironmentVariable(pair.Key));
        try
        {
            foreach (var pair in variables) Environment.SetEnvironmentVariable(pair.Key, pair.Value);
            var configuration = new ConfigurationBuilder().AddEnvironmentVariables().Build();
            var services = new ServiceCollection();
            services.AddStorageOptions(configuration);
            using var provider = services.BuildServiceProvider();
            var options = provider.GetRequiredService<IOptions<StorageOptions>>().Value;

            Assert.Equal("azure-blob", options.Provider);
            Assert.Equal("azure-identity", options.Authentication);
            Assert.Equal("envaccount", options.AzureBlob.AccountName);
            Assert.Equal("envobjects", options.AzureBlob.ContainerName);
        }
        finally
        {
            foreach (var pair in previous) Environment.SetEnvironmentVariable(pair.Key, pair.Value);
        }
    }
}