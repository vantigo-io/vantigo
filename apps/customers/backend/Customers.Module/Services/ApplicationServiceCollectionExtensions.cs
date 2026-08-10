using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;

using Vantigo.Contracts;
using Vantigo.Customers.Database.Customers;
using Vantigo.Customers.Domain.Contacts;
using Vantigo.Customers.Domain.Customers;

namespace Vantigo.Customers.Services;

internal static class ApplicationServiceCollectionExtensions
{
    internal static IServiceCollection AddCustomerTimeline(this IServiceCollection services)
    {
        services.AddScoped<ICustomerTimelineRecorder, CustomerTimelineRecorder>();
        services.AddScoped<ICustomerDirectory, CustomerDirectory>();
        return services;
    }

    private sealed class CustomerDirectory(CustomersDbContext db) : ICustomerDirectory
    {
        public async Task<CustomerDirectoryEntry?> FindCustomerAsync(int customerId, CancellationToken cancellationToken = default)
            => await db.Customers.AsNoTracking().Where(customer => customer.Id == customerId)
                .Select(customer => new CustomerDirectoryEntry(customer.Id, (string)customer.Name))
                .SingleOrDefaultAsync(cancellationToken);

        public async Task<ContactDirectoryEntry?> FindContactAsync(int contactId, CancellationToken cancellationToken = default)
            => await db.Contacts.AsNoTracking().Where(contact => contact.Id == contactId)
                .Select(contact => new ContactDirectoryEntry(contact.Id, (string)contact.FirstName, (string)contact.LastName, contact.Email == null ? null : (string)contact.Email))
                .SingleOrDefaultAsync(cancellationToken);
    }
}