using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Options;

namespace Vantigo.Configuration;

/// <summary>
/// The centrally configured object storage backend.
/// </summary>
public sealed class StorageOptions
{
    public const string ConfigurationSectionName = "Storage";

    public string? Provider { get; set; }
    public string? Authentication { get; set; }
    public S3StorageOptions S3 { get; set; } = new();
    public AzureBlobStorageOptions AzureBlob { get; set; } = new();
    public LocalStorageOptions Local { get; set; } = new();

    public bool IsConfigured => !string.IsNullOrWhiteSpace(Provider);

    /// <summary>
    /// Normalizes values accepted from environment variables and validates the
    /// provider/authentication matrix without ever including secret values in errors.
    /// </summary>
    public void Validate()
    {
        Provider = Normalize(Provider);
        Authentication = Normalize(Authentication);

        if (!IsConfigured)
            return;

        Require(Authentication, "Authentication");
        switch (Provider)
        {
            case "s3":
                RequireAuthentication("access-key");
                S3.Validate();
                break;
            case "azure-blob":
                if (Authentication is not ("azure-identity" or "connection-string" or "sas"))
                    throw Invalid("Authentication must be azure-identity, connection-string, or sas when Provider is azure-blob.");
                AzureBlob.Validate(Authentication);
                break;
            case "local":
                RequireAuthentication("none");
                Local.Validate();
                break;
            default:
                throw Invalid("Provider must be one of s3, azure-blob, or local.");
        }
    }

    private void RequireAuthentication(string expected)
    {
        if (!string.Equals(Authentication, expected, StringComparison.Ordinal))
            throw Invalid($"Authentication must be {expected} when Provider is {Provider}.");
    }

    internal static string? Normalize(string? value) =>
        string.IsNullOrWhiteSpace(value) ? null : value.Trim().ToLowerInvariant();

    internal static void Require(string? value, string name)
    {
        if (string.IsNullOrWhiteSpace(value))
            throw Invalid($"{name} is required when Storage:Provider is configured.");
    }

    public static InvalidOperationException Invalid(string message) =>
        new($"Storage configuration error: {message}");
}

public sealed class S3StorageOptions
{
    public string? BucketName { get; set; }
    public string? ServiceUrl { get; set; }
    public string? AccessKey { get; set; }
    public string? SecretKey { get; set; }
    public string Region { get; set; } = "us-east-1";
    public bool ForcePathStyle { get; set; } = true;

    public void Validate()
    {
        StorageOptions.Require(BucketName, "S3:BucketName");
        StorageOptions.Require(AccessKey, "S3:AccessKey");
        StorageOptions.Require(SecretKey, "S3:SecretKey");
        StorageOptions.Require(Region, "S3:Region");

        if (BucketName!.Length is < 3 or > 63 ||
            !BucketName.All(c => (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c is '.' or '-') ||
            !IsAsciiAlphaNumeric(BucketName[0]) || !IsAsciiAlphaNumeric(BucketName[^1]) ||
            BucketName.Contains("..", StringComparison.Ordinal) ||
            BucketName.Contains(".-", StringComparison.Ordinal) ||
            BucketName.Contains("-.", StringComparison.Ordinal))
            throw StorageOptions.Invalid("S3:BucketName must be a safe DNS-compatible bucket name.");

        if (ServiceUrl is not null &&
            (!Uri.TryCreate(ServiceUrl, UriKind.Absolute, out var uri) ||
             (uri.Scheme != Uri.UriSchemeHttp && uri.Scheme != Uri.UriSchemeHttps) ||
             !string.IsNullOrEmpty(uri.UserInfo) || !string.IsNullOrEmpty(uri.Query) ||
             !string.IsNullOrEmpty(uri.Fragment)))
            throw StorageOptions.Invalid("S3:ServiceUrl must be an absolute HTTP(S) URL without credentials, query, or fragment.");
    }

    private static bool IsAsciiAlphaNumeric(char value) =>
        (value >= 'a' && value <= 'z') || (value >= '0' && value <= '9');
}

public sealed class AzureBlobStorageOptions
{
    public string? AccountName { get; set; }
    public string? ContainerName { get; set; }
    public string? ConnectionString { get; set; }
    public string? SasToken { get; set; }
    public bool CreateContainerIfMissing { get; set; }

    public void Validate(string authentication)
    {
        StorageOptions.Require(AccountName, "AzureBlob:AccountName");
        StorageOptions.Require(ContainerName, "AzureBlob:ContainerName");

        if (AccountName!.Length is < 3 or > 24 ||
            !AccountName.All(c => (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')))
            throw StorageOptions.Invalid("AzureBlob:AccountName must contain only lowercase letters and digits.");
        if (ContainerName!.Length is < 3 or > 63 ||
            !ContainerName.All(c => (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') ||
            !IsAsciiAlphaNumeric(ContainerName[0]) || !IsAsciiAlphaNumeric(ContainerName[^1]) ||
            ContainerName.Contains("--", StringComparison.Ordinal))
            throw StorageOptions.Invalid("AzureBlob:ContainerName must be a safe Azure Blob container name.");

        switch (authentication)
        {
            case "azure-identity":
                if (!string.IsNullOrWhiteSpace(ConnectionString) || !string.IsNullOrWhiteSpace(SasToken))
                    throw StorageOptions.Invalid("AzureBlob connection-string and sas credentials must not be supplied for azure-identity.");
                break;
            case "connection-string":
                StorageOptions.Require(ConnectionString, "AzureBlob:ConnectionString");
                if (!string.IsNullOrWhiteSpace(SasToken))
                    throw StorageOptions.Invalid("AzureBlob:SasToken must not be supplied for connection-string authentication.");
                break;
            case "sas":
                StorageOptions.Require(SasToken, "AzureBlob:SasToken");
                if (!string.IsNullOrWhiteSpace(ConnectionString))
                    throw StorageOptions.Invalid("AzureBlob:ConnectionString must not be supplied for sas authentication.");
                break;
        }
    }

    private static bool IsAsciiAlphaNumeric(char value) =>
        (value >= 'a' && value <= 'z') || (value >= '0' && value <= '9');
}

public sealed class LocalStorageOptions
{
    public string? RootPath { get; set; }

    /// <summary>
    /// Development-only escape hatch for a root that is group/world writable.
    /// Keep false in every production deployment.
    /// </summary>
    public bool AllowInsecureRootForDevelopment { get; set; }

    public void Validate()
    {
        StorageOptions.Require(RootPath, "Local:RootPath");
        if (!Path.IsPathFullyQualified(RootPath!))
            throw StorageOptions.Invalid("Local:RootPath must be an absolute path.");
    }
}

public static class StorageConfigurationExtensions
{
    public static IServiceCollection AddStorageOptions(this IServiceCollection services, IConfiguration configuration)
    {
        var section = configuration.GetSection(StorageOptions.ConfigurationSectionName);
        services.AddOptions<StorageOptions>()
            .Configure(options => StorageOptionsBinder.Bind(section, options))
            .ValidateOnStart();
        services.AddSingleton<IValidateOptions<StorageOptions>>(_ => new StorageOptionsValidator(section));
        return services;
    }

    internal static IConfigurationSection SelectAzureSection(IConfiguration section)
    {
        var preferredSection = section.GetSection("Azure_Blob");
        return preferredSection.GetChildren().Any() ? preferredSection : section.GetSection("AzureBlob");
    }

    private static class StorageOptionsBinder
    {
        public static void Bind(IConfiguration section, StorageOptions options)
        {
            options.Provider = Read(section, "Provider", "PROVIDER");
            options.Authentication = Read(section, "Authentication", "AUTHENTICATION");

            var s3 = section.GetSection("S3");
            options.S3.BucketName = Read(s3, "BucketName", "BUCKET_NAME");
            options.S3.ServiceUrl = Read(s3, "ServiceUrl", "SERVICE_URL");
            options.S3.AccessKey = Read(s3, "AccessKey", "ACCESS_KEY");
            options.S3.SecretKey = Read(s3, "SecretKey", "SECRET_KEY");
            options.S3.Region = Read(s3, "Region", "REGION") ?? "us-east-1";
            options.S3.ForcePathStyle = ReadBool(s3, options.S3.ForcePathStyle, "ForcePathStyle", "FORCE_PATH_STYLE");

            // Azure_Blob is the environment-variable spelling of AzureBlob. The
            // legacy compact spelling remains accepted, with Azure_Blob taking
            // precedence when both are supplied.
            var azure = SelectAzureSection(section);
            options.AzureBlob.AccountName = Read(azure, "AccountName", "ACCOUNT_NAME");
            options.AzureBlob.ContainerName = Read(azure, "ContainerName", "CONTAINER_NAME");
            options.AzureBlob.ConnectionString = Read(azure, "ConnectionString", "CONNECTION_STRING");
            options.AzureBlob.SasToken = Read(azure, "SasToken", "SAS_TOKEN");
            options.AzureBlob.CreateContainerIfMissing = ReadBool(azure, options.AzureBlob.CreateContainerIfMissing,
                "CreateContainerIfMissing", "CREATE_CONTAINER_IF_MISSING");

            var local = section.GetSection("Local");
            options.Local.RootPath = Read(local, "RootPath", "ROOT_PATH");
            options.Local.AllowInsecureRootForDevelopment = ReadBool(local, options.Local.AllowInsecureRootForDevelopment,
                "AllowInsecureRootForDevelopment", "ALLOW_INSECURE_ROOT_FOR_DEVELOPMENT");
        }

        private static string? Read(IConfiguration section, params string[] keys)
        {
            foreach (var key in keys)
            {
                var value = section[key];
                if (value is not null) return value;
            }
            return null;
        }

        private static bool ReadBool(IConfiguration section, bool fallback, params string[] keys)
        {
            var value = Read(section, keys);
            return value is not null && bool.TryParse(value, out var parsed) ? parsed : fallback;
        }
    }

    private sealed class StorageOptionsValidator(IConfiguration section) : IValidateOptions<StorageOptions>
    {
        public ValidateOptionsResult Validate(string? name, StorageOptions options)
        {
            try
            {
                ValidateKnownKeys(section);
                ValidateBoolean(section.GetSection("S3"), "ForcePathStyle", "FORCE_PATH_STYLE");
                ValidateBoolean(StorageConfigurationExtensions.SelectAzureSection(section), "CreateContainerIfMissing", "CREATE_CONTAINER_IF_MISSING");
                ValidateBoolean(section.GetSection("Local"), "AllowInsecureRootForDevelopment", "ALLOW_INSECURE_ROOT_FOR_DEVELOPMENT");
                options.Validate();
                return ValidateOptionsResult.Success;
            }
            catch (Exception exception) when (exception is InvalidOperationException or ArgumentException)
            {
                return ValidateOptionsResult.Fail(exception.Message);
            }
        }

        private static void ValidateBoolean(IConfiguration section, params string[] keys)
        {
            foreach (var key in keys)
            {
                var value = section[key];
                if (value is not null && !bool.TryParse(value, out _))
                    throw StorageOptions.Invalid($"Storage:{key} must be true or false.");
            }
        }

        private static void ValidateKnownKeys(IConfiguration section)
        {
            ValidateSection(section, ["Provider", "Authentication", "S3", "AzureBlob", "Azure_Blob", "Local"],
                new Dictionary<string, string[]>(StringComparer.OrdinalIgnoreCase)
                {
                    ["S3"] = ["BucketName", "BUCKET_NAME", "ServiceUrl", "SERVICE_URL", "AccessKey", "ACCESS_KEY", "SecretKey", "SECRET_KEY", "Region", "REGION", "ForcePathStyle", "FORCE_PATH_STYLE"],
                    ["AzureBlob"] = ["AccountName", "ACCOUNT_NAME", "ContainerName", "CONTAINER_NAME", "ConnectionString", "CONNECTION_STRING", "SasToken", "SAS_TOKEN", "CreateContainerIfMissing", "CREATE_CONTAINER_IF_MISSING"],
                    ["Azure_Blob"] = ["AccountName", "ACCOUNT_NAME", "ContainerName", "CONTAINER_NAME", "ConnectionString", "CONNECTION_STRING", "SasToken", "SAS_TOKEN", "CreateContainerIfMissing", "CREATE_CONTAINER_IF_MISSING"],
                    ["Local"] = ["RootPath", "ROOT_PATH", "AllowInsecureRootForDevelopment", "ALLOW_INSECURE_ROOT_FOR_DEVELOPMENT"],
                });
        }

        private static void ValidateSection(IConfiguration section, string[] scalarKeys,
            Dictionary<string, string[]> nestedKeys)
        {
            foreach (var child in section.GetChildren())
            {
                var key = child.Key;
                if (nestedKeys.TryGetValue(key, out var allowedChildren))
                {
                    if (child.Value is not null || child.GetChildren().Any(grandchild =>
                        !allowedChildren.Contains(grandchild.Key, StringComparer.OrdinalIgnoreCase)))
                        throw StorageOptions.Invalid($"Unknown or malformed configuration key '{child.Path}'.");
                    continue;
                }

                if (!scalarKeys.Contains(key, StringComparer.OrdinalIgnoreCase) || child.GetChildren().Any())
                    throw StorageOptions.Invalid($"Unknown or malformed configuration key '{child.Path}'.");
            }
        }
    }
}