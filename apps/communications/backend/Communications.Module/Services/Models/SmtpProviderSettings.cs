namespace Vantigo.Communications.Services;

public sealed record SmtpProviderSettings(string Host, int Port, bool UseSsl, string? Username);