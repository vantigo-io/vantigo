using Microsoft.AspNetCore.Http.HttpResults;
using Microsoft.EntityFrameworkCore;

using Vantigo.Customers.Database.Customers;
using Vantigo.Customers.Domain.Contacts;
using Vantigo.Customers.Endpoints.Contacts.Dtos;
using Vantigo.Customers.Endpoints.Dtos;

namespace Vantigo.Customers.Endpoints.Contacts;

/// <summary>
/// Lists contacts with pagination, sorting and free-text search across the name
/// parts, phone and email. Each contact is returned together with the number of
/// customers it is associated with — and, when there is exactly one, that customer.
/// </summary>
internal static class GetContactsEndpoint
{
    private const int DefaultPageSize = 25;
    private const int MaxPageSize = 100;

    internal static async Task<Results<Ok<PaginatedResponse<Response>>, ProblemHttpResult>> Handler(
        [AsParameters] Request request,
        CustomersDbContext dbContext,
        CancellationToken cancellationToken)
    {
        if (Validate(request) is { } problem)
        {
            return problem;
        }

        var page = request.Page ?? 1;
        var pageSize = request.PageSize ?? DefaultPageSize;

        var query = dbContext.Contacts.AsNoTracking();

        if (!string.IsNullOrWhiteSpace(request.Search))
        {
            // Multi-word searches like "Anders Refsdal" are split into terms, where
            // every term must match at least one of the searchable fields. This way
            // a full name matches even though the parts live in separate columns.
            var terms = request.Search.Split(' ', StringSplitOptions.RemoveEmptyEntries | StringSplitOptions.TrimEntries);

            foreach (var term in terms)
            {
                var pattern = $"%{EscapeLikePattern(term)}%";

                query = query.Where(c =>
                    EF.Functions.ILike(c.FirstName, pattern) ||
                    EF.Functions.ILike(c.LastName, pattern) ||
                    (c.MiddleName != null && EF.Functions.ILike((string)c.MiddleName.Value, pattern)) ||
                    (c.Prefix != null && EF.Functions.ILike((string)c.Prefix.Value, pattern)) ||
                    (c.Suffix != null && EF.Functions.ILike((string)c.Suffix.Value, pattern)) ||
                    (c.Phone != null && EF.Functions.ILike((string)c.Phone.Value, pattern)) ||
                    (c.Email != null && EF.Functions.ILike((string)c.Email.Value, pattern)));
            }
        }

        var totalCount = await query.CountAsync(cancellationToken);

        var contacts = await ApplySorting(query, request)
            .Skip((page - 1) * pageSize)
            .Take(pageSize)
            .ToListAsync(cancellationToken);

        var associationsByContact = await LoadAssociations(dbContext, contacts, cancellationToken);

        var items = contacts
            .Select(contact => new Response
            {
                Contact = ContactResponse.FromDomain(contact),
                CustomerCount = associationsByContact.GetValueOrDefault(contact.Id)?.Count ?? 0,
                Customer = associationsByContact.GetValueOrDefault(contact.Id) is [var single]
                    ? single
                    : null,
            })
            .ToList();

        return TypedResults.Ok(PaginatedResponse<Response>.Create(items, page, pageSize, totalCount));
    }

    /// <summary>
    /// Loads the customer associations for the current page of contacts in a single
    /// query, so the count and single-customer projection never fan out per contact.
    /// </summary>
    private static async Task<Dictionary<int, List<CustomerReference>>> LoadAssociations(
        CustomersDbContext dbContext,
        List<Contact> contacts,
        CancellationToken cancellationToken)
    {
        var contactIds = contacts.Select(c => c.Id).ToList();

        var associations = await dbContext.CustomersContacts
            .AsNoTracking()
            .Where(cc => contactIds.Contains(cc.ContactId))
            .Select(cc => new
            {
                cc.ContactId,
                Customer = new CustomerReference { Id = cc.CustomerId, Name = cc.Customer.Name },
            })
            .ToListAsync(cancellationToken);

        return associations
            .GroupBy(a => a.ContactId)
            .ToDictionary(group => group.Key, group => group.Select(a => a.Customer).ToList());
    }

    private static ProblemHttpResult? Validate(Request request)
    {
        var errors = new List<string>();

        if (request.Page is < 1)
        {
            errors.Add($"'page' must be 1 or greater, but was {request.Page}.");
        }

        if (request.PageSize is < 1 or > MaxPageSize)
        {
            errors.Add($"'pageSize' must be between 1 and {MaxPageSize}, but was {request.PageSize}.");
        }

        if (request.SortBy is not (null or SortFields.Id or SortFields.Name))
        {
            errors.Add($"'sortBy' must be one of '{SortFields.Id}' or '{SortFields.Name}', but was '{request.SortBy}'.");
        }

        if (request.SortDirection is not (null or SortDirections.Ascending or SortDirections.Descending))
        {
            errors.Add($"'sortDirection' must be one of '{SortDirections.Ascending}' or '{SortDirections.Descending}', but was '{request.SortDirection}'.");
        }

        if (errors.Count == 0)
        {
            return null;
        }

        return TypedResults.Problem(
            title: "Invalid query parameters",
            detail: string.Join(" ", errors),
            statusCode: StatusCodes.Status400BadRequest);
    }

    private static IOrderedQueryable<Contact> ApplySorting(IQueryable<Contact> query, Request request)
    {
        var descending = request.SortDirection == SortDirections.Descending;

        return (request.SortBy, descending) switch
        {
            (SortFields.Id, false) => query.OrderBy(c => c.Id),
            (SortFields.Id, true) => query.OrderByDescending(c => c.Id),
            (_, true) => query.OrderByDescending(c => c.FirstName).ThenByDescending(c => c.LastName).ThenByDescending(c => c.Id),
            _ => query.OrderBy(c => c.FirstName).ThenBy(c => c.LastName).ThenBy(c => c.Id),
        };
    }

    private static string EscapeLikePattern(string value) => value
        .Replace(@"\", @"\\")
        .Replace("%", @"\%")
        .Replace("_", @"\_");

    internal readonly record struct Request
    {
        /// <summary>The 1-based page to retrieve. Defaults to 1.</summary>
        public int? Page { get; init; }

        /// <summary>The number of contacts per page, between 1 and 100. Defaults to 25.</summary>
        public int? PageSize { get; init; }

        /// <summary>The field to sort by, either "id" or "name". Defaults to "name" (first, then last name).</summary>
        public string? SortBy { get; init; }

        /// <summary>The sort direction, either "asc" or "desc". Defaults to "asc".</summary>
        public string? SortDirection { get; init; }

        /// <summary>Case-insensitive free-text search matching the name parts, phone and email.</summary>
        public string? Search { get; init; }
    }

    internal readonly record struct Response
    {
        public required ContactResponse Contact { get; init; }

        /// <summary>The number of customers the contact is associated with.</summary>
        public required int CustomerCount { get; init; }

        /// <summary>The associated customer, when the contact is associated with exactly one.</summary>
        public CustomerReference? Customer { get; init; }
    }

    internal readonly record struct CustomerReference
    {
        public required int Id { get; init; }
        public required string Name { get; init; }
    }

    internal static class SortFields
    {
        internal const string Id = "id";
        internal const string Name = "name";
    }

    internal static class SortDirections
    {
        internal const string Ascending = "asc";
        internal const string Descending = "desc";
    }
}