namespace Vantigo.Identity.Database.Accounts;

/// <summary>Singleton system-wide settings persisted in the identity database.</summary>
public sealed class SystemSetting
{
    public const int SingletonId = 1;

    public int Id { get; set; }

    public bool MaintenanceEnabled { get; set; }

    public string? MaintenanceMessage { get; set; }

    public DateTimeOffset UpdatedAt { get; set; }

    public string UpdatedBy { get; set; } = string.Empty;
}