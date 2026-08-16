using System.Collections.Concurrent;
using System.Security.Cryptography;

namespace Vantigo.Identity.Services;

/// <summary>
/// Holds the control-plane hand-off for tenant offboarding.
///
/// This is intentionally a safe, process-local stub until the system
/// orchestrator owns cross-module export and deletion. It never deletes data.
/// A real implementation must persist the export request and token in the
/// orchestration domain before enabling a purge worker.
/// </summary>
public sealed class TenantOffboardingService
{
    private readonly ConcurrentDictionary<Guid, OffboardingState> states = new();

    public (OffboardingState State, bool Created) RequestExport(Guid tenantId)
    {
        var created = false;
        var state = states.GetOrAdd(tenantId, id =>
        {
            created = true;
            return new OffboardingState(
                Guid.NewGuid(),
                id,
                Convert.ToHexString(RandomNumberGenerator.GetBytes(32)),
                DateTimeOffset.UtcNow,
                null);
        });
        return (state, created);
    }

    public OffboardingState? Get(Guid tenantId) =>
        states.TryGetValue(tenantId, out var state) ? state : null;

    public bool ValidatePurge(Guid tenantId, Guid exportId, string token)
    {
        if (!states.TryGetValue(tenantId, out var state) || state.ExportId != exportId ||
            token.Length != state.PurgeToken.Length)
            return false;

        try
        {
            return CryptographicOperations.FixedTimeEquals(
                Convert.FromHexString(state.PurgeToken), Convert.FromHexString(token));
        }
        catch (FormatException)
        {
            return false;
        }
    }

    public void MarkPurgeRequested(Guid tenantId)
    {
        if (!states.TryGetValue(tenantId, out var state)) return;
        states[tenantId] = state with { PurgeRequestedAtUtc = DateTimeOffset.UtcNow };
    }

    public void Remove(Guid tenantId, Guid exportId)
    {
        if (states.TryGetValue(tenantId, out var state) && state.ExportId == exportId)
            states.TryRemove(tenantId, out _);
    }
}

public sealed record OffboardingState(
    Guid ExportId,
    Guid TenantId,
    string PurgeToken,
    DateTimeOffset RequestedAtUtc,
    DateTimeOffset? PurgeRequestedAtUtc);