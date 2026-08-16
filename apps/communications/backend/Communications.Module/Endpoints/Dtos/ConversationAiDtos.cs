namespace Vantigo.Communications.Endpoints;

internal sealed record AiDraftRequest(string? Tone, string? Instruction);
internal sealed record AiDraftResponse(Guid InteractionId, string? Subject, string Text, bool ProductDataUsed, string Notice);
internal sealed record AiCustomerSuggestionResponse(Guid InteractionId, int? CustomerId, double? Confidence, string? Rationale, string Outcome);