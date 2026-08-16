using Microsoft.AspNetCore.Antiforgery;

using Vantigo.Communications.Authorization;
using Vantigo.Communications.Services;
using Vantigo.Contracts.AspNetCore.Authorization;

using static Vantigo.Communications.Endpoints.CommunicationEndpointHelpers;

namespace Vantigo.Communications.Endpoints;

internal static class ConversationAiEndpoints
{
    internal static void MapConversationAiEndpoints(this IEndpointRouteBuilder api)
    {
        api.MapPost("/conversations/{id:guid}/ai/draft", DraftAi).RequirePermission(CommunicationsPermissions.ConversationsReply).RequirePermission(CommunicationsPermissions.ConversationsView);
        api.MapPost("/conversations/{id:guid}/ai/customer-suggestion", CustomerSuggestionAi).RequirePermission(CommunicationsPermissions.ConversationsManage).RequirePermission(CommunicationsPermissions.ConversationsView);
    }

    private static async Task<IResult> DraftAi(Guid id, AiDraftRequest? request, HttpContext http, IAntiforgery antiforgery, ICommunicationsAiService ai, CancellationToken ct)
    {
        var csrf = await ValidateAntiforgery(http, antiforgery); if (csrf is not null) return csrf;
        if (request?.Tone?.Trim().ToLowerInvariant() is not ("concise" or "friendly" or "formal") || string.IsNullOrWhiteSpace(request.Instruction) || request.Instruction.Length > 1000)
            return Error(StatusCodes.Status400BadRequest, "invalid_request", "Tone must be concise, friendly, or formal and instruction must be 1-1000 characters.");
        var result = await ai.DraftAsync(id, request.Tone.Trim().ToLowerInvariant(), request.Instruction.Trim(), CurrentUserId(http), ct);
        if (result.Unavailable) return Error(StatusCodes.Status503ServiceUnavailable, result.ErrorCode!, result.ErrorMessage!);
        if (!result.Succeeded) return Error(result.ErrorCode == "not_found" ? StatusCodes.Status404NotFound : StatusCodes.Status422UnprocessableEntity, result.ErrorCode!, result.ErrorMessage!);
        return TypedResults.Ok(new AiDraftResponse(result.InteractionId!.Value, result.Subject, result.Text!, result.ProductDataUsed, "Editable draft only; nothing was sent or queued."));
    }

    private static async Task<IResult> CustomerSuggestionAi(Guid id, HttpContext http, IAntiforgery antiforgery, ICommunicationsAiService ai, CancellationToken ct)
    {
        var csrf = await ValidateAntiforgery(http, antiforgery); if (csrf is not null) return csrf;
        var result = await ai.SuggestCustomerAsync(id, CurrentUserId(http), ct);
        if (result.Unavailable) return Error(StatusCodes.Status503ServiceUnavailable, result.ErrorCode!, result.ErrorMessage!);
        if (result.ErrorCode == "not_found") return TypedResults.NotFound();
        if (result.ErrorCode == "protected_existing_customer") return Error(StatusCodes.Status409Conflict, result.ErrorCode, result.ErrorMessage!);
        if (result.ErrorCode == "insufficient_candidates") return Error(StatusCodes.Status422UnprocessableEntity, result.ErrorCode, result.ErrorMessage!);
        if (result.ErrorCode is not null && !result.Succeeded && result.Outcome == "invalid") return Error(StatusCodes.Status422UnprocessableEntity, result.ErrorCode, result.ErrorMessage!);
        return TypedResults.Ok(new AiCustomerSuggestionResponse(result.InteractionId!.Value, result.CustomerId, result.Confidence, result.Rationale, result.Outcome ?? "none"));
    }
}