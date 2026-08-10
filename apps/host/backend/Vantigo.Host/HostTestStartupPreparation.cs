namespace Vantigo.Host;

/// <summary>Test-only startup preparation requested by WebApplicationFactory fixtures.</summary>
public sealed record HostTestStartupPreparation(bool ApplyMigrations, bool SeedDevelopmentData);