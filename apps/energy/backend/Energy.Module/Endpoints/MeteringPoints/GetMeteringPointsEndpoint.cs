using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Energy.Database.Energy;
using Vantigo.Energy.Endpoints.Dtos;
using Vantigo.Energy.Endpoints.MeteringPoints.Dtos;

namespace Vantigo.Energy.Endpoints.MeteringPoints;

internal static class GetMeteringPointsEndpoint
{
    internal static async Task<Results<Ok<PaginatedResponse<MeteringPointResponse>>, ProblemHttpResult>> Handler(
        [AsParameters] Request request, EnergyDbContext db, CancellationToken cancellationToken)
    {
        if (request.Page is < 1 || request.PageSize is < 1 or > 100)
            return TypedResults.Problem(title: "Invalid query parameters", detail: "Page must be at least 1 and pageSize must be between 1 and 100.", statusCode: 400);

        var page = request.Page ?? 1;
        var pageSize = request.PageSize ?? 25;
        var query = db.MeteringPoints.AsNoTracking();
        if (!string.IsNullOrWhiteSpace(request.Search))
        {
            var pattern = $"%{EscapeLikePattern(request.Search.Trim())}%";
            query = query.Where(point => EF.Functions.ILike(point.Gsrn.Value, pattern) ||
                EF.Functions.ILike(point.MeterNumber, pattern) || EF.Functions.ILike(point.Address.City, pattern) ||
                EF.Functions.ILike(point.Address.StreetAddress, pattern));
        }

        var total = await query.CountAsync(cancellationToken);
        var points = await query.OrderBy(point => point.Id).Skip((page - 1) * pageSize).Take(pageSize).ToListAsync(cancellationToken);
        return TypedResults.Ok(PaginatedResponse<MeteringPointResponse>.Create(
            points.Select(MeteringPointResponse.FromDomain).ToList(), page, pageSize, total));
    }

    private static string EscapeLikePattern(string value) => value.Replace(@"\", @"\\").Replace("%", @"\%").Replace("_", @"\_");

    internal readonly record struct Request(string? Search, int? Page, int? PageSize);
}