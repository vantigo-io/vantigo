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
}