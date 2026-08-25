namespace Vantigo.Contracts;

public interface ICustomerDirectory
{
    Task<CustomerDirectoryEntry?> FindCustomerAsync(int customerId, CancellationToken cancellationToken = default);
    Task<ContactDirectoryEntry?> FindContactAsync(int contactId, CancellationToken cancellationToken = default);

    /// <summary>
    /// Finds contacts whose canonical or customer-specific email address matches the
    /// supplied address. Matching is exact after trimming whitespace and applying
    /// <see cref="string.ToLowerInvariant()"/>. The result contains only ids;
    /// it contains one item per matching contact and is ambiguous when more than one
    /// contact matches the address.
    /// </summary>
    /// <remarks>
    /// The default implementation preserves source compatibility for module test
    /// doubles that do not provide this optional capability and fails explicitly.
    /// The Customers module implementation provides the actual lookup; a null result
    /// from that implementation means that no match was found.
    /// </remarks>
    Task<ContactEmailResolution?> FindByEmailAsync(
        string normalizedEmailAddress,
        CancellationToken cancellationToken = default) =>
        throw new NotSupportedException("This customer directory does not support email resolution.");
}

/// <summary>
/// The safe cross-module projection for one customer. Archived customers still
/// resolve — Communications and Energy keep historical references to customer
/// ids — and the flag lets consumers mark them as archived in their views.
/// </summary>
public sealed record CustomerDirectoryEntry(int Id, string Name, bool Archived = false);
public sealed record ContactDirectoryEntry(int Id, string FirstName, string LastName, string? Email);

/// <summary>
/// The id-only result of an email lookup. A lookup is ambiguous when it contains
/// more than one matching contact; callers must not automatically select a contact
/// in that case.
/// </summary>
public sealed record ContactEmailResolution(IReadOnlyList<ContactEmailMatch> Matches)
{
    public bool IsAmbiguous => Matches.Count > 1;
}

/// <summary>
/// The safe cross-module projection for one contact matched by email.
/// </summary>
public sealed record ContactEmailMatch(int ContactId, IReadOnlyList<int> CandidateCustomerIds);