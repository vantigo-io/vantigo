using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;

using Vantigo.Communications.Database.Communications;
using Vantigo.Communications.Services;
using Vantigo.Contracts;

namespace Vantigo.Communications.Module.Tests.Integration;

[Collection(CommunicationsModuleCollection.Name)]
public sealed class ConversationContactLinkerTests(CommunicationsModuleFactory factory)
{
    [Fact]
    public async Task One_customer_match_links_contact_and_customer_with_normalized_email()
    {
        await factory.ResetChannelStateAsync();
        factory.CustomerDirectory.Resolution = new ContactEmailResolution([new ContactEmailMatch(42, [7])]);
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        var (conversation, sender) = await CreateInboundAsync(db, $" PERSON-{Guid.NewGuid():N}@EXAMPLE.TEST ");

        var result = await scope.ServiceProvider.GetRequiredService<IConversationContactLinker>().LinkAsync(conversation, sender);

        Assert.Equal(ConversationContactLinkStatus.AutomaticallyLinked, result.Status);
        Assert.Equal(42, sender.ContactId);
        Assert.Equal(7, conversation.CustomerId);
        Assert.Equal(CustomerAssociationSources.Automatic, conversation.CustomerAssociationSource);
        Assert.Empty(await db.ConversationCustomerCandidates.ToListAsync());
        Assert.StartsWith("person-", factory.CustomerDirectory.LastEmail);
        Assert.EndsWith("@example.test", factory.CustomerDirectory.LastEmail);
    }

    [Fact]
    public async Task Multiple_customer_candidates_are_persisted_without_selecting_one_and_are_idempotent()
    {
        await factory.ResetChannelStateAsync();
        factory.CustomerDirectory.Resolution = new ContactEmailResolution([new ContactEmailMatch(42, [9, 7, 9])]);
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        var (conversation, sender) = await CreateInboundAsync(db, $"person-{Guid.NewGuid():N}@example.test");
        var linker = scope.ServiceProvider.GetRequiredService<IConversationContactLinker>();

        var result = await linker.LinkAsync(conversation, sender);
        await linker.LinkAsync(conversation, sender);

        Assert.Equal(ConversationContactLinkStatus.CandidatesPersisted, result.Status);
        Assert.Equal(42, sender.ContactId);
        Assert.Null(conversation.CustomerId);
        var candidateIds = await db.ConversationCustomerCandidates.OrderBy(item => item.CustomerId).Select(item => item.CustomerId).ToArrayAsync();
        Assert.Equal(new[] { 7, 9 }, candidateIds);
    }

    [Fact]
    public async Task Ambiguous_contact_resolution_does_not_link_or_persist_candidates()
    {
        await factory.ResetChannelStateAsync();
        factory.CustomerDirectory.Resolution = new ContactEmailResolution([
            new ContactEmailMatch(42, [7]),
            new ContactEmailMatch(43, [8]),
        ]);
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        var (conversation, sender) = await CreateInboundAsync(db, $"person-{Guid.NewGuid():N}@example.test");

        var result = await scope.ServiceProvider.GetRequiredService<IConversationContactLinker>().LinkAsync(conversation, sender);

        Assert.Equal(ConversationContactLinkStatus.Ambiguous, result.Status);
        Assert.Null(sender.ContactId);
        Assert.Null(conversation.CustomerId);
        Assert.Empty(await db.ConversationCustomerCandidates.ToListAsync());
    }

    [Fact]
    public async Task No_match_clears_stale_automatic_association_and_candidates()
    {
        await factory.ResetChannelStateAsync();
        factory.CustomerDirectory.Resolution = null;
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        var (conversation, sender) = await CreateInboundAsync(db, $"unknown-{Guid.NewGuid():N}@example.test");
        conversation.CustomerId = 12;
        conversation.CustomerAssociationSource = CustomerAssociationSources.Automatic;
        db.ConversationCustomerCandidates.Add(new ConversationCustomerCandidate { ConversationId = conversation.Id, CustomerId = 99, CreatedAt = DateTimeOffset.UtcNow });
        await db.SaveChangesAsync();

        var result = await scope.ServiceProvider.GetRequiredService<IConversationContactLinker>().LinkAsync(conversation, sender);

        Assert.Equal(ConversationContactLinkStatus.NoMatch, result.Status);
        Assert.Null(conversation.CustomerId);
        Assert.Null(conversation.CustomerAssociationSource);
        Assert.Empty(await db.ConversationCustomerCandidates.ToListAsync());
    }

    [Fact]
    public async Task Manual_association_is_preserved_while_match_updates_contact_and_candidates()
    {
        await factory.ResetChannelStateAsync();
        factory.CustomerDirectory.Resolution = new ContactEmailResolution([new ContactEmailMatch(42, [7, 8])]);
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        var (conversation, sender) = await CreateInboundAsync(db, $"person-{Guid.NewGuid():N}@example.test");
        conversation.CustomerId = 123;
        conversation.CustomerAssociationSource = CustomerAssociationSources.Manual;
        await db.SaveChangesAsync();

        var result = await scope.ServiceProvider.GetRequiredService<IConversationContactLinker>().LinkAsync(conversation, sender);

        Assert.Equal(ConversationContactLinkStatus.CandidatesPersisted, result.Status);
        Assert.Equal(123, conversation.CustomerId);
        Assert.Equal(CustomerAssociationSources.Manual, conversation.CustomerAssociationSource);
        var candidateIds = await db.ConversationCustomerCandidates.OrderBy(item => item.CustomerId).Select(item => item.CustomerId).ToArrayAsync();
        Assert.Equal(new[] { 7, 8 }, candidateIds);
    }

    [Fact]
    public async Task A_stale_automatic_customer_is_relinked_to_the_new_single_match()
    {
        await factory.ResetChannelStateAsync();
        factory.CustomerDirectory.Resolution = new ContactEmailResolution([new ContactEmailMatch(42, [77])]);
        await using var scope = factory.Services.CreateAsyncScope();
        var db = scope.ServiceProvider.GetRequiredService<CommunicationsDbContext>();
        var (conversation, sender) = await CreateInboundAsync(db, $"person-{Guid.NewGuid():N}@example.test");
        conversation.CustomerId = 12;
        conversation.CustomerAssociationSource = CustomerAssociationSources.Automatic;
        await db.SaveChangesAsync();
        var linker = scope.ServiceProvider.GetRequiredService<IConversationContactLinker>();

        await linker.LinkAsync(conversation, sender);
        await linker.LinkAsync(conversation, sender);

        Assert.Equal(77, conversation.CustomerId);
        Assert.Equal(CustomerAssociationSources.Automatic, conversation.CustomerAssociationSource);
        Assert.Empty(await db.ConversationCustomerCandidates.ToListAsync());
    }

    private static async Task<(Conversation Conversation, Participant Sender)> CreateInboundAsync(CommunicationsDbContext db, string address)
    {
        var channel = await db.Channels.SingleAsync();
        var now = DateTimeOffset.UtcNow;
        var sender = new Participant { Id = Guid.NewGuid(), ChannelId = channel.Id, Address = address, CreatedAt = now };
        var conversation = new Conversation { Id = Guid.NewGuid(), ChannelId = channel.Id, LastActivityAt = now, CreatedAt = now };
        conversation.Participants.Add(new ConversationParticipant { ConversationId = conversation.Id, ParticipantId = sender.Id, Participant = sender });
        db.Conversations.Add(conversation);
        await db.SaveChangesAsync();
        return (conversation, sender);
    }
}