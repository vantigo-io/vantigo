namespace Vantigo.Identity.Database.Accounts;

/// <summary>
/// Minimal durable operational projection. It contains event kind and time only;
/// it must never carry credentials, request payloads, or user identity data.
/// </summary>
public sealed class OperationalEvent
{
    public long Id { get; set; }

    public required string Kind { get; set; }

    public DateTimeOffset OccurredAt { get; set; }
}