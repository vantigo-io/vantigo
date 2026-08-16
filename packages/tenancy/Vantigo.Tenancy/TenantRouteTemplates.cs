namespace Vantigo.Tenancy;

/// <summary>Builds the shared route templates used by tenant-scoped endpoints.</summary>
public static class TenantRouteTemplates
{
    private const string ApiVersionPrefix = "/api/v";

    /// <summary>
    /// Inserts the tenant route segment after the API version segment. This keeps
    /// version constraints and version parameter names intact.
    /// </summary>
    public static string AddTenantSegment(string prefix)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(prefix);

        if (!prefix.StartsWith(ApiVersionPrefix, StringComparison.Ordinal))
            throw new ArgumentException("Tenant route prefixes must start with /api/v.", nameof(prefix));

        var versionEnd = prefix.IndexOf('/', ApiVersionPrefix.Length);
        if (versionEnd < 0)
            return $"{prefix}/t/{{{TenantResolutionMiddleware.TenantSlugRouteKey}}}";

        return prefix[..versionEnd] + "/t/{" +
            TenantResolutionMiddleware.TenantSlugRouteKey + "}" + prefix[versionEnd..];
    }

    /// <summary>Gets the canonical module key from a versioned business prefix.</summary>
    public static bool TryGetModuleKey(string prefix, out string moduleKey)
    {
        moduleKey = string.Empty;
        if (!prefix.StartsWith(ApiVersionPrefix, StringComparison.Ordinal)) return false;

        var moduleStart = prefix.LastIndexOf('/') + 1;
        if (moduleStart <= 0 || moduleStart == prefix.Length) return false;

        var candidate = prefix[moduleStart..];
        if (candidate is not ("customers" or "communications" or "products" or "energy")) return false;

        moduleKey = candidate;
        return true;
    }
}