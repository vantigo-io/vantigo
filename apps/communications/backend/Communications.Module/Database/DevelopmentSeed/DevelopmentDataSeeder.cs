using Bogus;

using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Options;

using Vantigo.Communications.Database.Communications;
using Vantigo.Configuration;

namespace Vantigo.Communications.Database.DevelopmentSeed;

internal static class DevelopmentDataSeeder
{
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
            channel = new Channel { Id = DevelopmentChannelId, Type = "email", Address = DevelopmentAddress, DisplayName = "Development Channel", IsActive = false, CreatedAt = DateTimeOffset.UtcNow };
            db.Channels.Add(channel);
        }
        var count = Math.Clamp(options.Data.Messages, 0, 100);
        if (!await db.Conversations.AnyAsync(item => item.ChannelId == channel.Id, cancellationToken))
        {
            var faker = new Faker("en");
            for (var index = 0; index < count; index++)
            {
                var now = DateTimeOffset.UtcNow.AddDays(-index - 1);
                var participant = new Participant { Id = Guid.NewGuid(), ChannelId = channel.Id, Address = $"contact-{index + 1}@example.test", DisplayName = faker.Name.FullName(), CreatedAt = now };
                var conversation = new Conversation { Id = Guid.NewGuid(), ChannelId = channel.Id, Subject = faker.Lorem.Sentence(5), Status = "open", LastActivityAt = now, PreviewText = faker.Lorem.Sentence(), CreatedAt = now };
                var message = new ConversationMessage { Id = Guid.NewGuid(), ConversationId = conversation.Id, Direction = "inbound", Participant = participant, Subject = conversation.Subject, TextBody = faker.Lorem.Paragraph(), OccurredAt = now, CreatedAt = now };
                db.Conversations.Add(conversation);
                db.ConversationMessages.Add(message);
            }
        }
        await db.SaveChangesAsync(cancellationToken);
    }
}