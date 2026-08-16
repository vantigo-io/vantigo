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

        /// <summary>
        /// Matches canonical and customer-specific email addresses after trimming and
        /// applying <see cref="string.ToLowerInvariant()"/>. Email value objects
        /// are persisted in this canonical form, so the equality predicates remain
        /// index-friendly and do not expose email addresses outside this module.
        /// </summary>
        public async Task<ContactEmailResolution?> FindByEmailAsync(
            string normalizedEmailAddress,
            CancellationToken cancellationToken = default)
        {
            ArgumentNullException.ThrowIfNull(normalizedEmailAddress);

            var normalized = normalizedEmailAddress.Trim().ToLowerInvariant();

            var matchingContacts = await db.Contacts
                .AsNoTracking()
                .Where(contact =>
                    (contact.Email != null && (string)contact.Email.Value == normalized) ||
                    db.CustomersContacts.Any(association =>
                        association.ContactId == contact.Id &&
                        association.Email != null &&
                        (string)association.Email.Value == normalized))
                .Select(contact => new
                {
                    ContactId = contact.Id,
                    CanonicalEmailMatches = contact.Email != null && (string)contact.Email.Value == normalized,
                })
                .ToListAsync(cancellationToken);

            if (matchingContacts.Count == 0)
            {
                return null;
            }

            var contactIds = matchingContacts.Select(contact => contact.ContactId).ToArray();
            var canonicalContactIds = matchingContacts
                .Where(contact => contact.CanonicalEmailMatches)
                .Select(contact => contact.ContactId)
                .ToArray();

            var customerAssociations = await db.CustomersContacts
                .AsNoTracking()
                .Where(association =>
                    contactIds.Contains(association.ContactId) &&
                    (canonicalContactIds.Contains(association.ContactId) ||
                     (association.Email != null && (string)association.Email.Value == normalized)))
                .Select(association => new
                {
                    association.ContactId,
                    association.CustomerId,
                })
                .ToListAsync(cancellationToken);

            var customerIdsByContact = customerAssociations
                .GroupBy(association => association.ContactId)
                .ToDictionary(
                    group => group.Key,
                    group => (IReadOnlyList<int>)group
                        .Select(association => association.CustomerId)
                        .Distinct()
                        .Order()
                        .ToArray());

            var matches = matchingContacts
                .OrderBy(contact => contact.ContactId)
                .Select(contact => new ContactEmailMatch(
                    contact.ContactId,
                    customerIdsByContact.GetValueOrDefault(contact.ContactId, [])))
                .ToArray();

            return new ContactEmailResolution(matches);
        }
    }
}