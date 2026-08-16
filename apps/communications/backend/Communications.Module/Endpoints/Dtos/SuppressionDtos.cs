namespace Vantigo.Communications.Endpoints;

internal sealed record CreateSuppressionRequest(string? EmailAddress, string? Reason);
internal sealed record SuppressionResponse(Guid Id, string EmailAddress, string? Reason, DateTimeOffset CreatedAt);