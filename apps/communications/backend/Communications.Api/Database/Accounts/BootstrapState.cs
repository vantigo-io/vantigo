namespace Vantigo.Communications.Api.Database.Accounts;

public sealed class BootstrapState
{
    public int Id { get; set; }
    public DateTimeOffset CompletedAt { get; set; }
}