using Microsoft.EntityFrameworkCore;

using Vantigo.Communications.Authorization;
using Vantigo.Communications.Database.Communications;
using Vantigo.Contracts.AspNetCore.Authorization;

using static Vantigo.Communications.Endpoints.CommunicationEndpointHelpers;

namespace Vantigo.Communications.Endpoints;

internal static class TagEndpoints
{
    internal static void MapTagEndpoints(this IEndpointRouteBuilder api)
    {
        api.MapGet("/tags", ListTags).RequirePermission(CommunicationsPermissions.ConversationsView);
        api.MapPost("/tags", CreateTag).RequirePermission(CommunicationsPermissions.ConversationsManage);
        api.MapPut("/conversations/{id:guid}/tags/{tagId:guid}", AddTag).RequirePermission(CommunicationsPermissions.ConversationsManage);
        api.MapDelete("/conversations/{id:guid}/tags/{tagId:guid}", RemoveTag).RequirePermission(CommunicationsPermissions.ConversationsManage);
    }

    private static async Task<IResult> ListTags(CommunicationsDbContext db, CancellationToken ct) => TypedResults.Ok(await db.Tags.AsNoTracking().OrderBy(item => item.Name).Select(item => new TagResponse(item.Id, item.Name, item.Color)).ToListAsync(ct));
    private static async Task<IResult> CreateTag(CreateTagRequest? request, HttpContext http, CommunicationsDbContext db, CancellationToken ct)
    {
        if (string.IsNullOrWhiteSpace(request?.Name) || request.Name.Length > 100) return Error(StatusCodes.Status400BadRequest, "invalid_request", "A tag name is required.");
        var tag = new Tag { Id = Guid.NewGuid(), Name = request.Name.Trim(), Color = request.Color?.Trim() }; db.Tags.Add(tag); try { await db.SaveChangesAsync(ct); } catch (DbUpdateException exception) when (IsUniqueViolation(exception)) { return Error(StatusCodes.Status409Conflict, "tag_exists", "A tag with this name already exists."); }
        return TypedResults.Created(CommunicationPath(http, $"/tags/{tag.Id}"), new TagResponse(tag.Id, tag.Name, tag.Color));
    }
    private static async Task<IResult> AddTag(Guid id, Guid tagId, CommunicationsDbContext db, CancellationToken ct) { if (!await db.Conversations.AnyAsync(item => item.Id == id, ct) || !await db.Tags.AnyAsync(item => item.Id == tagId, ct)) return TypedResults.NotFound(); if (!await db.ConversationTags.AnyAsync(item => item.ConversationId == id && item.TagId == tagId, ct)) { db.ConversationTags.Add(new ConversationTag { ConversationId = id, TagId = tagId }); await db.SaveChangesAsync(ct); } return TypedResults.NoContent(); }
    private static async Task<IResult> RemoveTag(Guid id, Guid tagId, CommunicationsDbContext db, CancellationToken ct) { var link = await db.ConversationTags.FindAsync([id, tagId], ct); if (link is null) return TypedResults.NotFound(); db.ConversationTags.Remove(link); await db.SaveChangesAsync(ct); return TypedResults.NoContent(); }
}