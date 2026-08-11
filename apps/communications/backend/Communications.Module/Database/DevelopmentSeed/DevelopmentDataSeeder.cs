using Bogus;

using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Options;

using Vantigo.Communications.Database.Communications;
using Vantigo.Configuration;

namespace Vantigo.Communications.Database.DevelopmentSeed;

/// <summary>
/// Creates a small, repeatable local dataset. This is deliberately kept outside the
/// migration model: it is useful for development, but is not application data that
/// should be deployed to another environment.
/// </summary>
internal static class DevelopmentDataSeeder
{
    private const int FakerSeed = 20260804;
    private const int DefaultMessageCount = 2;
    private const string DevelopmentMailboxAddress = "dev-mailbox@vantigo.local";
    private const string DevelopmentSource = "development-seed";
    private static readonly Guid DevelopmentMailboxId = Guid.Parse("5c6e5f11-6b95-4b7d-8c4f-000000000001");
    private static readonly DateTimeOffset DevelopmentBootstrapCompletedAt = new(2026, 1, 5, 9, 0, 0, TimeSpan.Zero);
    private static readonly DateTimeOffset DevelopmentMailboxCreatedAt = new(2026, 1, 5, 9, 0, 0, TimeSpan.Zero);

    private static readonly IReadOnlyList<MessageDefinition> MessageDefinitions =
    [
        new(
            Guid.Parse("10000000-0000-0000-0000-000000000001"),
            "sent-summary",
            new DateTimeOffset(2026, 1, 12, 10, 15, 0, TimeSpan.Zero),
            [
                new(Guid.Parse("20000000-0000-0000-0000-000000000001"), "alex@example.test", "to", "relay_accepted", 1, new DateTimeOffset(2026, 1, 12, 10, 16, 0, TimeSpan.Zero)),
                new(Guid.Parse("20000000-0000-0000-0000-000000000002"), "finance@example.test", "cc", "relay_accepted", 1, new DateTimeOffset(2026, 1, 12, 10, 16, 0, TimeSpan.Zero)),
            ],
            [
                new(Guid.Parse("30000000-0000-0000-0000-000000000001"), "message_queued", null, new DateTimeOffset(2026, 1, 12, 10, 15, 1, TimeSpan.Zero)),
                new(Guid.Parse("30000000-0000-0000-0000-000000000002"), "relay_accepted", Guid.Parse("20000000-0000-0000-0000-000000000001"), new DateTimeOffset(2026, 1, 12, 10, 16, 0, TimeSpan.Zero)),
                new(Guid.Parse("30000000-0000-0000-0000-000000000003"), "relay_accepted", Guid.Parse("20000000-0000-0000-0000-000000000002"), new DateTimeOffset(2026, 1, 12, 10, 16, 0, TimeSpan.Zero)),
            ]),
        new(
            Guid.Parse("10000000-0000-0000-0000-000000000002"),
            "sent-follow-up",
            new DateTimeOffset(2026, 1, 18, 14, 30, 0, TimeSpan.Zero),
            [
                new(Guid.Parse("20000000-0000-0000-0000-000000000003"), "partner@example.test", "to", "relay_accepted", 1, new DateTimeOffset(2026, 1, 18, 14, 31, 0, TimeSpan.Zero)),
            ],
            [
                new(Guid.Parse("30000000-0000-0000-0000-000000000004"), "message_queued", null, new DateTimeOffset(2026, 1, 18, 14, 30, 0, TimeSpan.Zero)),
                new(Guid.Parse("30000000-0000-0000-0000-000000000005"), "relay_accepted", Guid.Parse("20000000-0000-0000-0000-000000000003"), new DateTimeOffset(2026, 1, 18, 14, 31, 0, TimeSpan.Zero)),
            ]),
    ];

    internal static async Task SeedDevelopmentDataAsync(this WebApplication app)
        => await app.Services.SeedDevelopmentDataAsync(app.Lifetime.ApplicationStopping);

    internal static async Task SeedDevelopmentDataAsync(
        this IServiceProvider services,
        CancellationToken cancellationToken)
    {
        await using var scope = services.CreateAsyncScope();
        var seedData = scope.ServiceProvider.GetRequiredService<IOptions<DevelopmentSeedOptions>>().Value.Data;
        var messageCount = seedData.Messages;

        await SeedCommunicationsAsync(
            scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>(),
            messageCount,
            cancellationToken);
    }

    private static async Task SeedCommunicationsAsync(
        CommunicationsDbContext dbContext,
        int messageCount,
        CancellationToken cancellationToken)
    {
        await using var transaction = await dbContext.Database.BeginTransactionAsync(cancellationToken);

        var mailboxById = await dbContext.SharedMailboxes
            .SingleOrDefaultAsync(item => item.Id == DevelopmentMailboxId, cancellationToken);
        var mailboxByAddress = await dbContext.SharedMailboxes
            .SingleOrDefaultAsync(item => item.FromAddress == DevelopmentMailboxAddress, cancellationToken);
        if (mailboxById is not null && mailboxByAddress is not null && mailboxById.Id != mailboxByAddress.Id)
        {
            throw new InvalidOperationException(
                $"Development mailbox ID {DevelopmentMailboxId} and address {DevelopmentMailboxAddress} refer to different mailboxes.");
        }

        var mailbox = mailboxById ?? mailboxByAddress;

        if (mailbox is null)
        {
            mailbox = new SharedMailbox
            {
                Id = DevelopmentMailboxId,
                FromAddress = DevelopmentMailboxAddress,
                DisplayName = "Development Mailbox",
                CreatedAt = DevelopmentMailboxCreatedAt,
                IsActive = false,
            };
            dbContext.SharedMailboxes.Add(mailbox);
        }
        else
        {
            mailbox.IsActive = false;
        }

        var messageSeeds = CreateMessageSeeds(messageCount);
        var messages = new Dictionary<Guid, EmailMessage>();
        foreach (var seed in messageSeeds)
        {
            var message = await dbContext.EmailMessages.SingleOrDefaultAsync(item => item.Id == seed.Id, cancellationToken);
            if (message is null)
            {
                message = new EmailMessage
                {
                    Id = seed.Id,
                    MailboxId = mailbox.Id,
                    Subject = seed.Subject,
                    TextBody = seed.TextBody,
                    CreatedAt = seed.CreatedAt,
                    CreatedByUserId = null,
                    Source = DevelopmentSource,
                };
                dbContext.EmailMessages.Add(message);
            }

            messages.Add(seed.Id, message);
        }

        await dbContext.SaveChangesAsync(cancellationToken);

        foreach (var seed in messageSeeds)
        {
            var message = messages[seed.Id];
            foreach (var deliverySeed in seed.Deliveries)
            {
                if (await dbContext.RecipientDeliveries.AnyAsync(item => item.Id == deliverySeed.Id, cancellationToken))
                {
                    continue;
                }

                dbContext.RecipientDeliveries.Add(new RecipientDelivery
                {
                    Id = deliverySeed.Id,
                    MessageId = message.Id,
                    EmailAddress = deliverySeed.EmailAddress,
                    RecipientType = deliverySeed.RecipientType,
                    Status = deliverySeed.Status,
                    Attempts = deliverySeed.Attempts,
                    AcceptedAt = deliverySeed.AcceptedAt,
                    CreatedAt = seed.CreatedAt,
                });
            }

            foreach (var eventSeed in seed.Events)
            {
                if (await dbContext.MessageEvents.AnyAsync(item => item.Id == eventSeed.Id, cancellationToken))
                {
                    continue;
                }

                dbContext.MessageEvents.Add(new MessageEvent
                {
                    Id = eventSeed.Id,
                    MessageId = message.Id,
                    DeliveryId = eventSeed.DeliveryId,
                    EventType = eventSeed.EventType,
                    OccurredAt = eventSeed.OccurredAt,
                });
            }

        }

        // Deliberately do not create OutboxJob rows. The development history is
        // already terminal and must never cause a real SMTP submission.
        await dbContext.SaveChangesAsync(cancellationToken);
        await transaction.CommitAsync(cancellationToken);
    }

    private static IReadOnlyList<DevelopmentMessageSeed> CreateMessageSeeds(int messageCount)
    {
        var faker = new Faker<DevelopmentMessageSeed>("en")
            .UseSeed(FakerSeed)
            .RuleFor(message => message.Subject, value => value.Lorem.Sentence(5))
            .RuleFor(message => message.TextBody, value => value.Lorem.Paragraphs(2));

        var seeds = new List<DevelopmentMessageSeed>(messageCount);
        for (var index = 0; index < messageCount; index++)
        {
            var definition = index < MessageDefinitions.Count
                ? MessageDefinitions[index]
                : CreateAdditionalMessageDefinition(index);
            var generated = faker.Generate();
            generated.Id = definition.Id;
            generated.CreatedAt = definition.CreatedAt;
            generated.Deliveries = definition.Deliveries;
            generated.Events = definition.Events;
            seeds.Add(generated);
        }

        return seeds;
    }

    private static MessageDefinition CreateAdditionalMessageDefinition(int index)
    {
        var deliveryId = DevelopmentGuid("20000000-0000-0000-0000-", index + 2);
        var messageId = DevelopmentGuid("10000000-0000-0000-0000-", index + 1);
        var queuedEventId = DevelopmentGuid("30000000-0000-0000-0000-", index * 2 + 2);
        var acceptedEventId = DevelopmentGuid("30000000-0000-0000-0000-", index * 2 + 3);
        var createdAt = new DateTimeOffset(2026, 1, 25, 11, 0, 0, TimeSpan.Zero).AddDays(index - 2);
        var acceptedAt = createdAt.AddMinutes(1);

        return new MessageDefinition(
            messageId,
            $"historical-{index + 1}",
            createdAt,
            [new(deliveryId, $"recipient-{index + 1}@example.test", "to", "relay_accepted", 1, acceptedAt)],
            [
                new(queuedEventId, "message_queued", null, createdAt),
                new(acceptedEventId, "relay_accepted", deliveryId, acceptedAt),
            ]);
    }

    private static Guid DevelopmentGuid(string prefix, int suffix) =>
        Guid.Parse($"{prefix}{suffix:000000000000}");

    private sealed class DevelopmentMessageSeed
    {
        public Guid Id { get; set; }
        public string Subject { get; set; } = string.Empty;
        public string TextBody { get; set; } = string.Empty;
        public DateTimeOffset CreatedAt { get; set; }
        public IReadOnlyList<DeliveryDefinition> Deliveries { get; set; } = [];
        public IReadOnlyList<EventDefinition> Events { get; set; } = [];
    }

    private sealed record MessageDefinition(
        Guid Id,
        string Key,
        DateTimeOffset CreatedAt,
        IReadOnlyList<DeliveryDefinition> Deliveries,
        IReadOnlyList<EventDefinition> Events);

    private sealed record DeliveryDefinition(
        Guid Id,
        string EmailAddress,
        string RecipientType,
        string Status,
        int Attempts,
        DateTimeOffset? AcceptedAt);

    private sealed record EventDefinition(
        Guid Id,
        string EventType,
        Guid? DeliveryId,
        DateTimeOffset OccurredAt);
}