namespace Vantigo.Communications.Services;

public sealed class CommunicationsAiOptions
{
    public bool Enabled { get; set; }
    public string Provider { get; set; } = "openai";
    public string Model { get; set; } = "gpt-4o-mini";
    public string? ApiKey { get; set; }
}