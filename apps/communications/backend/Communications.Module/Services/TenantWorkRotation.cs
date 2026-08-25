using Vantigo.Tenancy.Abstractions;

namespace Vantigo.Communications.Services;

/// <summary>
/// Rotates the starting tenant of a worker's scan so a busy early tenant
/// cannot permanently starve later ones. Row-level security scopes every
/// query to one tenant's session, so workers must visit tenants one by one;
/// fairness comes from periodically putting every tenant first in line.
/// </summary>
internal static class TenantWorkRotation
{
    internal static IEnumerable<TenantId> Rotate(IReadOnlyList<TenantId> tenants, int rotation)
    {
        if (tenants.Count == 0)
        {
            yield break;
        }

        var offset = (int)((uint)rotation % (uint)tenants.Count);
        for (var index = 0; index < tenants.Count; index++)
        {
            yield return tenants[(offset + index) % tenants.Count];
        }
    }
}