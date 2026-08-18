using System.Text.Json;

using Bogus;

using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Options;

using Vantigo.Configuration;
using Vantigo.Customers.Database.Customers;
using Vantigo.Customers.Domain.Contacts;
using Vantigo.Customers.Domain.Customers;
using Vantigo.Customers.Domain.Customers.Common;
using Vantigo.Customers.Domain.Customers.ValueObjects;
using Vantigo.Customers.Domain.Timeline;
using Vantigo.Tenancy;
using Vantigo.Tenancy.Abstractions;
using Vantigo.Tenancy.EntityFramework;

namespace Vantigo.Customers.Database.DevelopmentSeed;

/// <summary>
/// Creates a small, repeatable local dataset. This is deliberately kept outside the
/// migration model: it is useful for development, but is not application data that
/// should be deployed to another environment.
/// </summary>
public static class DevelopmentDataSeeder
{
    private const int FakerSeed = 20260804;
    private static readonly DateTimeOffset SeedNow = new(2026, 8, 10, 0, 0, 0, TimeSpan.Zero);
    private const int DefaultCustomerCount = 250;
    private const int DefaultContactCount = 300;
    private const int MaximumSeedCount = 500;
    private const string TimelineProducer = "development.seed";
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
        var tenant = await scope.ServiceProvider.GetRequiredService<ITenantDirectory>()
            .GetDefaultTenantAsync(cancellationToken);
        using var tenantScope = AmbientTenantContext.Enter(tenant);

        await SeedCustomersAsync(
            scope.ServiceProvider.GetRequiredService<CustomersDbContext>(),
            scope.ServiceProvider.GetRequiredService<ITenantCounterService>(),
            seedData.Customers,
            seedData.Contacts,
            cancellationToken);
    }

    private static async Task SeedCustomersAsync(
        CustomersDbContext dbContext,
        ITenantCounterService tenantCounterService,
        int customerCount,
        int contactCount,
        CancellationToken cancellationToken)
    {
        customerCount = ClampSeedCount(customerCount, DefaultCustomerCount);
        contactCount = ClampSeedCount(contactCount, DefaultContactCount);
        var customerSeeds = CreateCustomerSeeds(customerCount);
        var customersByKey = new Dictionary<string, Customer>(StringComparer.Ordinal);
        await using var transaction = await dbContext.Database.BeginTransactionAsync(cancellationToken);

        foreach (var (seed, seedIndex) in customerSeeds.Select((seed, index) => (seed, index)))
        {
            var customer = await dbContext.Customers.FirstOrDefaultAsync(
                item => item.Identity != null && (string)item.Identity.Value.Id == seed.LegalId,
                cancellationToken);

            if (customer is null)
            {
                // Deterministic variety so the customer overview has meaningful key figures:
                // a mix of business/person types, a few countries, some disabled customers
                // and creation dates spread over roughly the last year, weighted toward
                // recent customers (the first customer is always recent).
                var number = seedIndex + 1;
                var ageDays = (int)Math.Round(365 * Math.Pow((double)seedIndex / Math.Max(customerSeeds.Count - 1, 1), 1.7));
                var createdAt = SeedNow.AddDays(-ageDays).AddHours(-(number % 12));
                var updatedAt = createdAt.AddDays(number % 3 * 7);
                if (updatedAt > SeedNow)
                {
                    updatedAt = SeedNow;
                }

                customer = new Customer
                {
                    Name = seed.FriendlyName,
                    Identity = new LegalIdentity
                    {
                        Country = number % 4 == 0 ? "se" : number % 5 == 0 ? "dk" : "no",
                        Type = number % 3 == 0 ? LegalType.Person : LegalType.Business,
                        Id = seed.LegalId,
                        Name = seed.LegalName,
                        Source = LegalSource.Manual,
                    },
                    Status = number % 5 == 0 ? CustomerStatus.Disabled : CustomerStatus.Active,
                    CreatedAt = createdAt,
                    UpdatedAt = updatedAt,
                    CustomerNumber = await tenantCounterService.NextAsync(
                        dbContext, "customer-number", cancellationToken),
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
        await SeedCustomerTimelineAsync(dbContext, customerSeeds, customersByKey, contactSeeds, contactsByKey, cancellationToken);
        await transaction.CommitAsync(cancellationToken);
    }

    private static async Task SeedCustomerTimelineAsync(
        CustomersDbContext dbContext,
        IReadOnlyList<CustomerSeed> customerSeeds,
        IReadOnlyDictionary<string, Customer> customersByKey,
        IReadOnlyList<ContactSeed> contactSeeds,
        IReadOnlyDictionary<string, Contact> contactsByKey,
        CancellationToken cancellationToken)
    {
        var customerIds = customersByKey.Values.Select(customer => customer.Id).ToArray();
        var existing = await dbContext.CustomerTimelineEntries
            .Where(entry => customerIds.Contains(entry.CustomerId) && entry.Producer == TimelineProducer)
            .Select(entry => new { entry.CustomerId, entry.EventType })
            .ToListAsync(cancellationToken);
        var existingKeys = existing
            .Select(entry => $"{entry.CustomerId}:{entry.EventType}")
            .ToHashSet(StringComparer.Ordinal);
        var entries = new List<CustomerTimelineEntry>();

        foreach (var (seed, index) in customerSeeds.Select((item, itemIndex) => (item, itemIndex)))
        {
            var customer = customersByKey[seed.Key];
            AddTimelineEntry(entries, existingKeys, customer, "customer.created", customer.CreatedAt,
                $"Customer onboarded: {(string)customer.Name}",
                new { customerId = customer.Id, customerName = (string)customer.Name });

            var linkedContact = contactSeeds.FirstOrDefault(contact => contact.CustomerKeys.Contains(seed.Key, StringComparer.Ordinal));
            if (linkedContact is not null && contactsByKey.TryGetValue(linkedContact.Key, out var contact))
            {
                var activityAt = customer.CreatedAt.AddDays(2 + index % 29);
                if (activityAt > SeedNow) activityAt = SeedNow.AddHours(-(index % 6));
                AddTimelineEntry(entries, existingKeys, customer, "customer.contact_attached", activityAt,
                    $"Contact added: {linkedContact.FirstName} {linkedContact.LastName}",
                    new { customerId = customer.Id, contactId = contact.Id, role = linkedContact.Role });
            }
        }

        dbContext.CustomerTimelineEntries.AddRange(entries);
        await dbContext.SaveChangesAsync(cancellationToken);
    }

    private static void AddTimelineEntry(
        ICollection<CustomerTimelineEntry> entries,
        IReadOnlySet<string> existingKeys,
        Customer customer,
        string eventType,
        DateTimeOffset occurredAt,
        string summary,
        object payload)
    {
        if (existingKeys.Contains($"{customer.Id}:{eventType}")) return;
        var payloadJson = JsonSerializer.Serialize(payload);
        var entry = new CustomerTimelineEntry
        {
            CustomerId = customer.Id,
            Provenance = TimelineProvenance.Generated,
            Producer = TimelineProducer,
            EventType = eventType,
            OccurredOn = DateOnly.FromDateTime(occurredAt.UtcDateTime),
            OccurredAt = occurredAt,
            Summary = summary,
            PayloadJson = payloadJson,
            PayloadVersion = 1,
            CurrentRevision = 1,
            State = TimelineState.Active,
            ActorKind = TimelineActorKind.System,
            ActorDisplay = "Development seed",
            CreatedAt = occurredAt,
            UpdatedAt = occurredAt,
        };
        entry.Revisions.Add(new CustomerTimelineEntryRevision
        {
            RevisionNumber = 1,
            Provenance = entry.Provenance,
            Producer = entry.Producer,
            EventType = entry.EventType,
            OccurredOn = entry.OccurredOn,
            OccurredAt = entry.OccurredAt,
            Summary = entry.Summary,
            PayloadJson = payloadJson,
            PayloadVersion = entry.PayloadVersion,
            State = entry.State,
            ActorKind = entry.ActorKind,
            ActorDisplay = entry.ActorDisplay,
            CreatedAt = occurredAt,
        });
        entries.Add(entry);
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