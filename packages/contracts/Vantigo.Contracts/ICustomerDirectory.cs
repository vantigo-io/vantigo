namespace Vantigo.Contracts;

public interface ICustomerDirectory
{
    Task<CustomerDirectoryEntry?> FindCustomerAsync(int customerId, CancellationToken cancellationToken = default);
    Task<ContactDirectoryEntry?> FindContactAsync(int contactId, CancellationToken cancellationToken = default);
}

public sealed record CustomerDirectoryEntry(int Id, string Name);
public sealed record ContactDirectoryEntry(int Id, string FirstName, string LastName, string? Email);