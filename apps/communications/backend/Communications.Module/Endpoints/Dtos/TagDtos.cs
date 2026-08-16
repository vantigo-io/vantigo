namespace Vantigo.Communications.Endpoints;

internal sealed record CreateTagRequest(string? Name, string? Color);
internal sealed record TagResponse(Guid Id, string Name, string? Color);