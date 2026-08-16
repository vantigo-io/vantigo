using Microsoft.EntityFrameworkCore;

using Vantigo.Communications.Authorization;
using Vantigo.Communications.Database.Communications;
using Vantigo.Communications.Services;
using Vantigo.Contracts.AspNetCore.Authorization;

using static Vantigo.Communications.Endpoints.CommunicationEndpointHelpers;

namespace Vantigo.Communications.Endpoints;

internal static class SuppressionEndpoints
{
    internal static void MapSuppressionEndpoints(this IEndpointRouteBuilder api)
    {
        api.MapGet("/suppressions", ListSuppressions).RequirePermission(CommunicationsPermissions.SuppressionsManage);
        api.MapGet("/suppressions/{id:guid}", GetSuppression).RequirePermission(CommunicationsPermissions.SuppressionsManage);
        api.MapPost("/suppressions", CreateSuppression).RequirePermission(CommunicationsPermissions.SuppressionsManage);
        api.MapDelete("/suppressions/{id:guid}", DeleteSuppression).RequirePermission(CommunicationsPermissions.SuppressionsManage);
    }

    private static async Task<IResult> ListSuppressions(CommunicationsDbContext db, CancellationToken ct) => TypedResults.Ok(await db.Suppressions.AsNoTracking().OrderByDescending(item => item.CreatedAt).Select(item => new SuppressionResponse(item.Id, item.NormalizedEmailAddress, item.Reason, item.CreatedAt)).ToListAsync(ct));
    private static async Task<IResult> GetSuppression(Guid id, CommunicationsDbContext db, CancellationToken ct) { var item = await db.Suppressions.AsNoTracking().SingleOrDefaultAsync(item => item.Id == id, ct); return item is null ? TypedResults.NotFound() : TypedResults.Ok(new SuppressionResponse(item.Id, item.NormalizedEmailAddress, item.Reason, item.CreatedAt)); }
    private static async Task<IResult> CreateSuppression(CreateSuppressionRequest? request, HttpContext http, CommunicationsDbContext db, CancellationToken ct) { var errors = CommunicationValidation.ValidateSuppression(request); if (errors.Count > 0) return ValidationError(errors); var normalized = EmailSuppression.Normalize(request!.EmailAddress!); var existing = await db.Suppressions.SingleOrDefaultAsync(item => item.NormalizedEmailAddress == normalized, ct); if (existing is not null) return TypedResults.Ok(new SuppressionResponse(existing.Id, existing.NormalizedEmailAddress, existing.Reason, existing.CreatedAt)); var item = new Suppression { Id = Guid.NewGuid(), NormalizedEmailAddress = normalized, Reason = request.Reason, CreatedAt = DateTimeOffset.UtcNow }; db.Suppressions.Add(item); await db.SaveChangesAsync(ct); return TypedResults.Created(CommunicationPath(http, $"/suppressions/{item.Id}"), new SuppressionResponse(item.Id, item.NormalizedEmailAddress, item.Reason, item.CreatedAt)); }
    private static async Task<IResult> DeleteSuppression(Guid id, CommunicationsDbContext db, CancellationToken ct) { var item = await db.Suppressions.SingleOrDefaultAsync(item => item.Id == id, ct); if (item is null) return TypedResults.NotFound(); db.Suppressions.Remove(item); await db.SaveChangesAsync(ct); return TypedResults.NoContent(); }
}