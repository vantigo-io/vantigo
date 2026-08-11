namespace Vantigo.Contracts.Web;

public sealed record AuthError(
    string Code,
    string Message,
    IReadOnlyDictionary<string, string[]>? Fields = null);