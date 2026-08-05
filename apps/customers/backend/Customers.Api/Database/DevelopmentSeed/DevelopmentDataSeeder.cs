using System.Globalization;

using Bogus;

using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;

using Vantigo.Customers.Api.Database.Accounts;
using Vantigo.Customers.Api.Database.Customers;
using Vantigo.Customers.Api.Domain.Contacts;
using Vantigo.Customers.Api.Domain.Customers;
using Vantigo.Customers.Api.Domain.Customers.Common;
using Vantigo.Customers.Api.Domain.Customers.ValueObjects;
using Vantigo.Customers.Api.Endpoints.Auth;

namespace Vantigo.Customers.Api.Database.DevelopmentSeed;

/// <summary>
/// Creates a small, repeatable local dataset. This is deliberately kept outside the
/// migration model: it is useful for development, but is not application data that
/// should be deployed to another environment.
/// </summary>
internal static class DevelopmentDataSeeder
{
    private const int FakerSeed = 20260804;
    private const int DefaultCustomerCount = 6;
    private const int DefaultContactCount = 6;
    private const int MaximumSeedCount = 100;
    private static readonly Guid DevelopmentOwnerId = new("7f4d1e5b-8a62-4b7e-9c13-2d5f6a708194");
    private static readonly Guid DevelopmentOwnerRoleId = new("8e5c2f6c-9b73-4c8f-ad24-3e607b8192a5");
    private static readonly Guid DevelopmentUserRoleId = new("9f6d307d-ac84-4d90-be35-4f718c92a3b6");
    private static readonly DateTimeOffset DevelopmentBootstrapCompletedAt =
        new(2026, 8, 4, 0, 0, 0, TimeSpan.Zero);

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

    internal static async Task SeedDevelopmentDataAsync(this WebApplication app)
    {
        await using var scope = app.Services.CreateAsyncScope();

        var accountsDbContext = scope.ServiceProvider.GetRequiredService<AccountsDbContext>();
        await SeedDevelopmentOwnerAsync(
            accountsDbContext,
            scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>(),
            scope.ServiceProvider.GetRequiredService<RoleManager<IdentityRole<Guid>>>(),
            app.Configuration,
            app.Lifetime.ApplicationStopping);

        await SeedCustomersAsync(
            scope.ServiceProvider.GetRequiredService<CustomersDbContext>(),
            app.Configuration,
            app.Lifetime.ApplicationStopping);
    }

    private static async Task SeedDevelopmentOwnerAsync(
        AccountsDbContext dbContext,
        UserManager<ApplicationUser> userManager,
        RoleManager<IdentityRole<Guid>> roleManager,
        IConfiguration configuration,
        CancellationToken cancellationToken)
    {
        var adminConfiguration = configuration.GetSection("Development:Seed:Admin");
        var email = adminConfiguration["Email"]?.Trim();
        var displayName = adminConfiguration["DisplayName"]?.Trim();
        var password = adminConfiguration["Password"];

        if (string.IsNullOrWhiteSpace(email) ||
            string.IsNullOrWhiteSpace(displayName) ||
            string.IsNullOrWhiteSpace(password))
        {
            throw new InvalidOperationException(
                "Development:Seed:Admin requires Email, DisplayName, and Password in Development configuration.");
        }

        await using var transaction = await dbContext.Database.BeginTransactionAsync(
            System.Data.IsolationLevel.Serializable,
            cancellationToken);

        await EnsureRoleAsync(roleManager, AuthRoles.Owner, cancellationToken);
        await EnsureRoleAsync(roleManager, AuthRoles.User, cancellationToken);

        var user = await userManager.FindByEmailAsync(email);
        if (user is null)
        {
            user = new ApplicationUser
            {
                Id = DevelopmentOwnerId,
                UserName = email,
                Email = email,
                EmailConfirmed = true,
                DisplayName = displayName,
            };

            var createResult = await userManager.CreateAsync(user, password);
            EnsureIdentitySuccess(createResult, "The development Owner account could not be created.");
        }

        await EnsureUserRoleAsync(userManager, user, AuthRoles.Owner);
        await EnsureUserRoleAsync(userManager, user, AuthRoles.User);

        // Keep the one-time bootstrap endpoint unavailable after this account has
        // been created, matching the marker written by the bootstrap endpoint.
        if (!await dbContext.BootstrapStates.AnyAsync(state => state.Id == 1, cancellationToken))
        {
            dbContext.BootstrapStates.Add(new BootstrapState
            {
                Id = 1,
                CompletedAt = DevelopmentBootstrapCompletedAt,
            });
            await dbContext.SaveChangesAsync(cancellationToken);
        }

        await transaction.CommitAsync(cancellationToken);
    }

    private static async Task EnsureRoleAsync(
        RoleManager<IdentityRole<Guid>> roleManager,
        string roleName,
        CancellationToken cancellationToken)
    {
        if (await roleManager.FindByNameAsync(roleName) is not null)
        {
            return;
        }

        var roleId = roleName switch
        {
            AuthRoles.Owner => DevelopmentOwnerRoleId,
            AuthRoles.User => DevelopmentUserRoleId,
            _ => throw new InvalidOperationException($"Unknown development role '{roleName}'."),
        };
        var result = await roleManager.CreateAsync(new IdentityRole<Guid>
        {
            Id = roleId,
            Name = roleName,
            NormalizedName = roleName.ToUpperInvariant(),
        });
        EnsureIdentitySuccess(result, $"The development role '{roleName}' could not be created.");
    }

    private static async Task EnsureUserRoleAsync(
        UserManager<ApplicationUser> userManager,
        ApplicationUser user,
        string roleName)
    {
        if (await userManager.IsInRoleAsync(user, roleName))
        {
            return;
        }

        var result = await userManager.AddToRoleAsync(user, roleName);
        EnsureIdentitySuccess(result, $"The development account could not be assigned the '{roleName}' role.");
    }

    private static async Task SeedCustomersAsync(
        CustomersDbContext dbContext,
        IConfiguration configuration,
        CancellationToken cancellationToken)
    {
        var customerCount = ReadSeedCount(configuration, "Customers", DefaultCustomerCount);
        var contactCount = ReadSeedCount(configuration, "Contacts", DefaultContactCount);
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
                (990000000 + number).ToString(CultureInfo.InvariantCulture)));
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

    private static int ReadSeedCount(IConfiguration configuration, string name, int defaultValue)
    {
        var key = $"Development:Seed:Data:{name}";
        var configuredValue = configuration[key];
        if (configuredValue is null)
        {
            return defaultValue;
        }

        if (!int.TryParse(
                configuredValue.Trim(),
                NumberStyles.Integer,
                CultureInfo.InvariantCulture,
                out var count) ||
            count < 0 ||
            count > MaximumSeedCount)
        {
            throw new InvalidOperationException(
                $"{key} must be an integer from 0 through {MaximumSeedCount}; received '{configuredValue}'.");
        }

        return count;
    }

    private static void EnsureIdentitySuccess(IdentityResult result, string message)
    {
        if (result.Succeeded)
        {
            return;
        }

        var errors = string.Join("; ", result.Errors.Select(error => $"{error.Code}: {error.Description}"));
        throw new InvalidOperationException($"{message} {errors}");
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