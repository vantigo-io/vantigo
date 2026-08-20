using Vantigo.Configuration;

namespace Vantigo.Identity.Services;

/// <summary>The module keys that may be stored on a tenant.</summary>
public static class TenantModuleCatalog
{
    // These flags are pricing/navigation metadata only. Module enablement is
    // deliberately not an authorization boundary; centralized authorization
    // will decide access independently when that control plane is connected.
    private static readonly HashSet<string> KnownKeys = new(StringComparer.Ordinal)
    {
        "communications",
        "customers",
        "energy",
        "products",
    };

    public static IReadOnlyCollection<string> KnownModuleKeys => KnownKeys;

    public static bool TryNormalize(
        IEnumerable<string>? values,
        out string[] normalized,
        out string? invalidKey)
    {
        normalized = [];
        invalidKey = null;

        var keys = values?.ToArray() ?? [];
        var result = new HashSet<string>(StringComparer.Ordinal);
        foreach (var value in keys)
        {
            var key = value?.Trim().ToLowerInvariant();
            if (string.IsNullOrWhiteSpace(key) || !KnownKeys.Contains(key))
            {
                invalidKey = value;
                return false;
            }

            result.Add(key);
        }

        normalized = result.OrderBy(key => key, StringComparer.Ordinal).ToArray();
        return true;
    }

    /// <summary>
    /// Maps the host's per-module enablement flags (<see cref="ModuleHostingOptions"/>,
    /// the same source that decides which modules are registered, migrated, and seeded)
    /// to the tenant module keys that should be enabled by default.
    /// </summary>
    public static string[] ResolveHostEnabledKeys(ModuleHostingOptions hostingOptions)
    {
        var keys = new List<string>(KnownKeys.Count);
        if (hostingOptions.Customers.Enabled) keys.Add("customers");
        if (hostingOptions.Communications.Enabled) keys.Add("communications");
        if (hostingOptions.Products.Enabled) keys.Add("products");
        if (hostingOptions.Energy.Enabled) keys.Add("energy");
        return [.. keys.OrderBy(key => key, StringComparer.Ordinal)];
    }
}