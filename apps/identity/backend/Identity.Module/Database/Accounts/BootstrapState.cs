namespace Vantigo.Identity.Database.Accounts;

/// <summary>
/// A singleton marker that records that the one-time local owner bootstrap was
/// successfully consumed.
/// </summary>
public sealed class BootstrapState
{
    public int Id { get; set; }
    public DateTimeOffset CompletedAt { get; set; }
}