namespace Vantigo.Products.Api;

/// <summary>
/// Test-only startup preparation registered by WebApplicationFactory fixtures. It is
/// deliberately a DI marker rather than a configuration escape hatch for production.
/// </summary>
internal sealed record ProductApiTestStartupPreparation(
    bool ApplyMigrations,
    bool SeedDevelopmentData);