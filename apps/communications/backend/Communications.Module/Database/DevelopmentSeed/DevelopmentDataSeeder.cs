using Bogus;

using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Options;

using Vantigo.Communications.Database.Communications;
using Vantigo.Configuration;

namespace Vantigo.Communications.Database.DevelopmentSeed;

internal static class DevelopmentDataSeeder
{
    private const int FakerSeed = 20260804;
    private const int MaximumSeedCount = 500;
    private static readonly DateTimeOffset SeedNow = new(2026, 8, 10, 0, 0, 0, TimeSpan.Zero);
    private const string DevelopmentAddress = "dev-mailbox@vantigo.local";
    private static readonly Guid DevelopmentChannelId = Guid.Parse("5c6e5f11-6b95-4b7d-8c4f-000000000001");

    internal static async Task SeedDevelopmentDataAsync(this WebApplication app)
        => await app.Services.SeedDevelopmentDataAsync(app.Lifetime.ApplicationStopping);

    internal static async Task SeedDevelopmentDataAsync(this IServiceProvider services, CancellationToken cancellationToken)
    {
        await using var scope = services.CreateAsyncScope();
        var options = scope.ServiceProvider.GetRequiredService<IOptions<DevelopmentSeedOptions>>().Value;
        if (!options.Enabled) return;
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        var channel = await db.Channels.SingleOrDefaultAsync(item => item.Id == DevelopmentChannelId, cancellationToken);
        if (channel is null)
        {
            channel = new Channel { Id = DevelopmentChannelId, Type = "email", Address = DevelopmentAddress, DisplayName = "Development Channel", IsActive = false, CreatedAt = SeedNow };
            db.Channels.Add(channel);
        }
        var count = Math.Clamp(options.Data.Messages, 0, MaximumSeedCount);
        if (!await db.Conversations.AnyAsync(item => item.ChannelId == channel.Id, cancellationToken))
        {
            Randomizer.Seed = new Random(FakerSeed);
            var faker = new Faker("en");
            var openCount = Math.Max(10, (int)Math.Ceiling(count * 0.15));
            for (var index = 0; index < count; index++)
            {
                var ageDays = (int)Math.Round(365 * Math.Pow((double)index / Math.Max(count - 1, 1), 1.55));
                var isNeedsAttention = index < 10;
                var occurredAt = isNeedsAttention
                    ? SeedNow.AddDays(-(3 + index)).AddHours(-(index % 8))
                    : SeedNow.AddDays(-ageDays).AddHours(-(index % 12));
                var conversationId = DeterministicGuid("conversation", index);
                var participant = new Participant
                {
                    Id = DeterministicGuid("participant", index),
                    ChannelId = channel.Id,
                    Address = $"contact-{index + 1}@example.test",
                    DisplayName = faker.Name.FullName(),
                    CreatedAt = occurredAt,
                };
                var status = index < openCount ? "open" : index % 3 == 0 ? "archived" : "closed";
                var conversation = new Conversation
                {
                    Id = conversationId,
                    ChannelId = channel.Id,
                    Subject = faker.Lorem.Sentence(5),
                    Status = status,
                    LastActivityAt = occurredAt,
                    PreviewText = faker.Lorem.Sentence(),
                    CreatedAt = occurredAt,
                };
                conversation.Participants.Add(new ConversationParticipant
                {
                    ConversationId = conversationId,
                    ParticipantId = participant.Id,
                    Participant = participant,
                    Role = "from",
                });
                var message = new ConversationMessage
                {
                    Id = DeterministicGuid("message", index),
                    ConversationId = conversationId,
                    Direction = isNeedsAttention || index % 4 != 0 ? "inbound" : "outbound",
                    Participant = participant,
                    Subject = conversation.Subject,
                    TextBody = faker.Lorem.Paragraph(),
                    OccurredAt = occurredAt,
                    CreatedAt = occurredAt,
                };
                db.Conversations.Add(conversation);
                db.ConversationMessages.Add(message);
            }
        }
        await db.SaveChangesAsync(cancellationToken);
    }

    private static Guid DeterministicGuid(string scope, int index)
    {
        var prefix = scope switch
        {
            "conversation" => "10000000-0000-0000-0000-",
            "participant" => "20000000-0000-0000-0000-",
            "message" => "30000000-0000-0000-0000-",
            _ => "40000000-0000-0000-0000-",
        };
        return Guid.Parse(prefix + index.ToString("D12", System.Globalization.CultureInfo.InvariantCulture));
    }
}