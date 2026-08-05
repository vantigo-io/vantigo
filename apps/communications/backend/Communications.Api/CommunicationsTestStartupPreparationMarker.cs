namespace Vantigo.Communications.Api;

/// <summary>
/// Internal opt-in used only by Communications WebApplicationFactory fixtures.
/// It is intentionally DI-only so production configuration cannot bypass the CLI.
/// </summary>
internal sealed class CommunicationsTestStartupPreparationMarker;