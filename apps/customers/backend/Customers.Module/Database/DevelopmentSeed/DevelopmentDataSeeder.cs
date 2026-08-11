using Bogus;

using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Options;

using Vantigo.Configuration;
using Vantigo.Customers.Database.Customers;
using Vantigo.Customers.Domain.Contacts;
using Vantigo.Customers.Domain.Customers;
using Vantigo.Customers.Domain.Customers.Common;
using Vantigo.Customers.Domain.Customers.ValueObjects;

namespace Vantigo.Customers.Database.DevelopmentSeed;

/// <summary>
/// Creates a small, repeatable local dataset. This is deliberately kept outside the
/// migration model: it is useful for development, but is not application data that
/// should be deployed to another environment.
/// </summary>
public static class DevelopmentDataSeeder
{
    private const int FakerSeed = 20260804;
    private const int DefaultCustomerCount = 6;
    private const int DefaultContactCount = 6;
    private const int MaximumSeedCount = 100;
    private static readonly IReadOnlyList<CustomerDefinition> CustomerDefinitions =
    [
        new("aurora", "990000001"),
        new("fjord", "990000002"),
        new("northstar", "990000003"),
        new("solstice", "990000004"),
        new("meadow", "990000005"),
        new("summit", "990000006"),
    ];

    private static readonly IReadOnlyList<ContactDefinition> ContactDefinitions =
    [
        new("astrid", ["aurora", "northstar"], "Managing Director"),
        new("bjorn", ["fjord"], "Finance Lead"),
        new("clara", ["northstar"], "Operations Manager"),
        new("david", ["fjord", "solstice"], "Technical Director"),
        new("elin", ["solstice", "meadow"], "Office Manager"),
        new("frank", ["aurora", "summit"], "Board Chair"),
    ];

    public static async Task SeedAsync(this IServiceProvider services, CancellationToken cancellationToken = default)
    {
        await using var scope = services.CreateAsyncScope();
        var seedData = scope.ServiceProvider.GetRequiredService<IOptions<DevelopmentSeedOptions>>().Value.Data;

        await SeedCustomersAsync(
            scope.ServiceProvider.GetRequiredService<CustomersDbContext>(),
            seedData.Customers,
            seedData.Contacts,
            cancellationToken);
    }

    private static async Task SeedCustomersAsync(
        CustomersDbContext dbContext,
        int customerCount,
        int contactCount,
        CancellationToken cancellationToken)
    {
        customerCount = ClampSeedCount(customerCount, DefaultCustomerCount);
        contactCount = ClampSeedCount(contactCount, DefaultContactCount);
        var customerSeeds = CreateCustomerSeeds(customerCount);
        var customersByKey = new Dictionary<string, Customer>(StringComparer.Ordinal);

        foreach (var seed in customerSeeds)
        {
            var customer = await dbContext.Customers.FirstOrDefaultAsync(
                item => item.Identity != null && (string)item.Identity.Value.Id == seed.LegalId,
                cancellationToken);

            if (customer is null)
            {
                customer = new Customer
                {
                    Name = seed.FriendlyName,
                    Identity = new LegalIdentity
                    {
                        Country = "no",
                        Type = LegalType.Business,
                        Id = seed.LegalId,
                        Name = seed.LegalName,
                        Source = LegalSource.Manual,
                    },
                };
                dbContext.Customers.Add(customer);
            }

            customersByKey.Add(seed.Key, customer);
        }

        await dbContext.SaveChangesAsync(cancellationToken);

        var selectedCustomerKeys = customerSeeds.Select(seed => seed.Key).ToHashSet(StringComparer.Ordinal);
        var contactSeeds = CreateContactSeeds(contactCount, selectedCustomerKeys);
        var contactsByKey = new Dictionary<string, Contact>(StringComparer.Ordinal);
        foreach (var seed in contactSeeds)
        {
            var contact = await dbContext.Contacts.FirstOrDefaultAsync(
                item => item.Email != null && (string)item.Email.Value == seed.Email,
                cancellationToken);

            if (contact is null)
            {
                contact = new Contact
                {
                    FirstName = seed.FirstName,
                    LastName = seed.LastName,
                    Phone = seed.Phone,
                    Email = seed.Email,
                };
                dbContext.Contacts.Add(contact);
            }

            contactsByKey.Add(seed.Key, contact);
        }

        await dbContext.SaveChangesAsync(cancellationToken);

        foreach (var seed in contactSeeds)
        {
            var contact = contactsByKey[seed.Key];
            foreach (var customerKey in seed.CustomerKeys)
            {
                var customer = customersByKey[customerKey];
                var associationExists = await dbContext.CustomersContacts.AnyAsync(
                    association => association.CustomerId == customer.Id && association.ContactId == contact.Id,
                    cancellationToken);

                if (!associationExists)
                {
                    dbContext.CustomersContacts.Add(new CustomerContact
                    {
                        CustomerId = customer.Id,
                        ContactId = contact.Id,
                        Role = seed.Role,
                    });
                }
            }
        }

        await dbContext.SaveChangesAsync(cancellationToken);
    }

    private static IReadOnlyList<CustomerSeed> CreateCustomerSeeds(int count)
    {
        var faker = new Faker<CustomerSeed>("en")
            .UseSeed(FakerSeed)
            .RuleFor(customer => customer.FriendlyName, value => value.Company.CompanyName())
            .RuleFor(customer => customer.LegalName, value => value.Company.CompanyName());

        return CreateCustomerDefinitions(count)
            .Select(definition =>
            {
                var generated = faker.Generate();
                generated.Key = definition.Key;
                generated.LegalId = definition.LegalId;
                return generated;
            })
            .ToArray();
    }

    private static IReadOnlyList<ContactSeed> CreateContactSeeds(
        int count,
        IReadOnlySet<string> selectedCustomerKeys)
    {
        var faker = new Faker<ContactSeed>("en")
            .UseSeed(FakerSeed)
            .RuleFor(contact => contact.FirstName, value => value.Name.FirstName())
            .RuleFor(contact => contact.LastName, value => value.Name.LastName())
            .RuleFor(contact => contact.Phone, value => value.Phone.PhoneNumber("+47 ### ## ###"));

        return CreateContactDefinitions(count)
            .Select(definition =>
            {
                var generated = faker.Generate();
                generated.Key = definition.Key;
                generated.Email = $"{definition.Key}@seed.vantigo.local";
                generated.CustomerKeys = definition.CustomerKeys
                    .Where(selectedCustomerKeys.Contains)
                    .ToArray();
                generated.Role = definition.Role;
                return generated;
            })
            .ToArray();
    }

    private static IReadOnlyList<CustomerDefinition> CreateCustomerDefinitions(int count)
    {
        var definitions = CustomerDefinitions.Take(count).ToList();
        for (var index = definitions.Count; index < count; index++)
        {
            var number = index + 1;
            definitions.Add(new(
                $"synthetic-customer-{number}",
                (990000000 + number).ToString(System.Globalization.CultureInfo.InvariantCulture)));
        }

        return definitions;
    }

    private static IReadOnlyList<ContactDefinition> CreateContactDefinitions(int count)
    {
        var definitions = ContactDefinitions
            .Take(count)
            .ToList();

        for (var index = definitions.Count; index < count; index++)
        {
            var number = index + 1;
            definitions.Add(new(
                $"synthetic-contact-{number}",
                [$"synthetic-customer-{number}"],
                "Development Contact"));
        }

        return definitions;
    }

    private static int ClampSeedCount(int value, int defaultValue)
    {
        if (value < 0 || value > MaximumSeedCount)
        {
            throw new InvalidOperationException(
                $"Development seed count must be an integer from 0 through {MaximumSeedCount}; received '{value}'.");
        }

        return value == 0 ? defaultValue : value;
    }

    private sealed class CustomerSeed
    {
        public string Key { get; set; } = string.Empty;
        public string LegalId { get; set; } = string.Empty;
        public string FriendlyName { get; set; } = string.Empty;
        public string LegalName { get; set; } = string.Empty;
    }

    private sealed class ContactSeed
    {
        public string Key { get; set; } = string.Empty;
        public string FirstName { get; set; } = string.Empty;
        public string LastName { get; set; } = string.Empty;
        public string Phone { get; set; } = string.Empty;
        public string Email { get; set; } = string.Empty;
        public IReadOnlyList<string> CustomerKeys { get; set; } = [];
        public string Role { get; set; } = string.Empty;
    }

    private sealed record CustomerDefinition(string Key, string LegalId);

    private sealed record ContactDefinition(
        string Key,
        IReadOnlyList<string> CustomerKeys,
        string Role);
}