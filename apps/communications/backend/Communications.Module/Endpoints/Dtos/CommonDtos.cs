namespace Vantigo.Communications.Endpoints;

internal sealed record CommunicationErrorResponse(CommunicationError Error);
internal sealed record CommunicationError(string Code, string Message, IReadOnlyDictionary<string, string[]>? Fields = null);