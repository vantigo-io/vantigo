namespace Vantigo.Tenancy;

/// <summary>
/// Marker metadata that tells <see cref="TenantResolutionMiddleware"/> to skip
/// tenant resolution for the decorated endpoint entirely. Use only for
/// process-wide infrastructure endpoints (e.g. health checks) that must not
/// depend on the tenant directory or its backing database.
/// </summary>
[AttributeUsage(AttributeTargets.Class | AttributeTargets.Method | AttributeTargets.Delegate)]
public sealed class SkipTenantResolutionAttribute : Attribute;