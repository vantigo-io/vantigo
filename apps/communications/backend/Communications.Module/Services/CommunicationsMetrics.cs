using System.Diagnostics.Metrics;

namespace Vantigo.Communications.Services;

/// <summary>Module-level metrics; the host exports this meter over OpenTelemetry.</summary>
internal static class CommunicationsMetrics
{
    internal const string MeterName = "Vantigo.Communications";

    private static readonly Meter Meter = new(MeterName);

    /// <summary>
    /// Counts outbox jobs reclaimed after a crash that happened between the
    /// external send and the completion commit. Delivery is at-least-once, so
    /// the job is deliberately resent — but each occurrence is a possible
    /// duplicate email and must be visible, not silent.
    /// </summary>
    internal static readonly Counter<long> PossibleDuplicateSends = Meter.CreateCounter<long>(
        "communications.outbox.possible_duplicate_sends",
        description: "Outbox jobs re-claimed after a crash window in which the external send may already have happened.");
}