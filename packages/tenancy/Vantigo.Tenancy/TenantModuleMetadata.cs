namespace Vantigo.Tenancy;

/// <summary>Endpoint metadata identifying a tenant-entitled business module.</summary>
public sealed class TenantModuleMetadata(string moduleKey)
{
    /// <summary>The canonical module key.</summary>
    public string ModuleKey { get; } = moduleKey;
}