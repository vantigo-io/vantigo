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

    /// <summary>Outbox jobs that completed (external delivery accepted or nothing sendable).</summary>
    internal static readonly Counter<long> OutboxJobsCompleted = Meter.CreateCounter<long>(
        "communications.outbox.jobs_completed",
        description: "Outbox jobs that reached the completed state.");

    /// <summary>Outbox send attempts that failed and were scheduled for retry.</summary>
    internal static readonly Counter<long> OutboxJobsRetried = Meter.CreateCounter<long>(
        "communications.outbox.jobs_retried",
        description: "Outbox jobs whose send attempt failed and was scheduled for retry.");

    /// <summary>
    /// Outbox jobs that exhausted their attempts. Every increment is
    /// undelivered customer email; alert on this.
    /// </summary>
    internal static readonly Counter<long> OutboxJobsFailed = Meter.CreateCounter<long>(
        "communications.outbox.jobs_failed",
        description: "Outbox jobs that terminally failed after exhausting their attempts.");
}