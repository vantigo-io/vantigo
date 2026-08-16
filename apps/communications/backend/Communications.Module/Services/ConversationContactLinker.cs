using Microsoft.EntityFrameworkCore;

using Vantigo.Communications.Database.Communications;
using Vantigo.Contracts;

namespace Vantigo.Communications.Services;

internal static class CustomerAssociationSources
{
    internal const string Manual = "manual";
    internal const string Automatic = "automatic";
}

internal enum ConversationContactLinkStatus
{
    PreservedUserAssociation,
    AutomaticallyLinked,
    CandidatesPersisted,
    NoMatch,
    Ambiguous,
}

internal sealed record ConversationContactLinkResult(
    ConversationContactLinkStatus Status,
    int? CustomerId = null,
    IReadOnlyList<int>? CandidateCustomerIds = null,
    int? ContactId = null);

internal interface IConversationContactLinker
{
    Task<ConversationContactLinkResult> LinkAsync(
        Conversation conversation,
        Participant sender,
        CancellationToken cancellationToken = default);
}

/// <summary>
/// Resolves an inbound sender through the public customer directory and persists the
/// resulting association in one transaction. The directory returns contact ids and
/// candidate customer ids only; no customer-private data crosses this boundary.
/// </summary>
internal sealed class ConversationContactLinker(
    CommunicationsDbContext db,
    ICustomerDirectory customerDirectory) : IConversationContactLinker
{
    public async Task<ConversationContactLinkResult> LinkAsync(
        Conversation conversation,
        Participant sender,
        CancellationToken cancellationToken = default)
    {
        var normalizedAddress = NormalizeEmail(sender.Address);
        var resolution = await customerDirectory.FindByEmailAsync(normalizedAddress, cancellationToken);

        var ownsTransaction = db.Database.IsRelational() && db.Database.CurrentTransaction is null;
        await using var transaction = ownsTransaction
            ? await db.Database.BeginTransactionAsync(System.Data.IsolationLevel.Serializable, cancellationToken)
            : null;

        var targetConversation = db.Entry(conversation).State is not EntityState.Detached
            ? conversation
            : await db.Conversations.Include(item => item.CustomerCandidates)
                .SingleOrDefaultAsync(item => item.Id == conversation.Id, cancellationToken) ?? conversation;
        if (db.Entry(targetConversation).State == EntityState.Detached)
            db.Conversations.Add(targetConversation);
        else if (db.Entry(targetConversation).State != EntityState.Added &&
                 !db.Entry(targetConversation).Collection(item => item.CustomerCandidates).IsLoaded)
            await db.Entry(targetConversation).Collection(item => item.CustomerCandidates).LoadAsync(cancellationToken);

        var targetSender = db.Entry(sender).State is not EntityState.Detached
            ? sender
            : await db.Participants.SingleOrDefaultAsync(item => item.Id == sender.Id, cancellationToken) ?? sender;
        if (db.Entry(targetSender).State == EntityState.Detached)
            db.Participants.Add(targetSender);

        if (targetConversation.CustomerCandidates is null)
            targetConversation.CustomerCandidates = [];

        var hasManualAssociation = targetConversation.CustomerId is not null &&
            !string.Equals(targetConversation.CustomerAssociationSource, CustomerAssociationSources.Automatic, StringComparison.Ordinal);

        ConversationContactLinkResult result;
        if (resolution is null || resolution.Matches.Count == 0)
        {
            targetSender.ContactId = null;
            ClearAutomaticAssociation(targetConversation, hasManualAssociation);
            ClearCandidates(targetConversation);
            result = new ConversationContactLinkResult(
                ConversationContactLinkStatus.NoMatch,
                hasManualAssociation ? targetConversation.CustomerId : null,
                [],
                null);
        }
        else if (resolution.IsAmbiguous)
        {
            targetSender.ContactId = null;
            ClearAutomaticAssociation(targetConversation, hasManualAssociation);
            ClearCandidates(targetConversation);
            result = new ConversationContactLinkResult(
                ConversationContactLinkStatus.Ambiguous,
                hasManualAssociation ? targetConversation.CustomerId : null,
                [],
                null);
        }
        else
        {
            var match = resolution.Matches[0];
            var candidateCustomerIds = match.CandidateCustomerIds
                .Distinct()
                .OrderBy(item => item)
                .ToArray();

            if (candidateCustomerIds.Length == 1)
            {
                targetSender.ContactId = match.ContactId;
                ClearCandidates(targetConversation);
                ClearSuggestion(targetConversation);

                if (!hasManualAssociation)
                {
                    targetConversation.CustomerId = candidateCustomerIds[0];
                    targetConversation.CustomerAssociationSource = CustomerAssociationSources.Automatic;
                    result = new ConversationContactLinkResult(
                        ConversationContactLinkStatus.AutomaticallyLinked,
                        targetConversation.CustomerId,
                        [],
                        match.ContactId);
                }
                else
                {
                    result = new ConversationContactLinkResult(
                        ConversationContactLinkStatus.PreservedUserAssociation,
                        targetConversation.CustomerId,
                        [],
                        match.ContactId);
                }
            }
            else if (candidateCustomerIds.Length > 1)
            {
                targetSender.ContactId = match.ContactId;
                ReconcileCandidates(targetConversation, candidateCustomerIds);
                ClearSuggestion(targetConversation);

                if (!hasManualAssociation)
                {
                    targetConversation.CustomerId = null;
                    targetConversation.CustomerAssociationSource = null;
                }

                result = new ConversationContactLinkResult(
                    ConversationContactLinkStatus.CandidatesPersisted,
                    hasManualAssociation ? targetConversation.CustomerId : null,
                    candidateCustomerIds,
                    match.ContactId);
            }
            else
            {
                targetSender.ContactId = null;
                ClearAutomaticAssociation(targetConversation, hasManualAssociation);
                ClearCandidates(targetConversation);
                result = new ConversationContactLinkResult(
                    ConversationContactLinkStatus.NoMatch,
                    hasManualAssociation ? targetConversation.CustomerId : null,
                    [],
                    null);
            }
        }

        await db.SaveChangesAsync(cancellationToken);
        if (ownsTransaction)
            await transaction!.CommitAsync(cancellationToken);

        // Inbound processors commonly pass the newly-created tracked entities. When
        // this service had to reload an existing row, mirror the association fields
        // back to those arguments so repeated pipeline work observes the same state.
        if (!ReferenceEquals(targetConversation, conversation))
        {
            conversation.CustomerId = targetConversation.CustomerId;
            conversation.CustomerAssociationSource = targetConversation.CustomerAssociationSource;
            conversation.SuggestedCustomerId = targetConversation.SuggestedCustomerId;
            conversation.SuggestedCustomerConfidence = targetConversation.SuggestedCustomerConfidence;
            conversation.SuggestedCustomerReasoning = targetConversation.SuggestedCustomerReasoning;
        }

        sender.ContactId = targetSender.ContactId;
        return result;
    }

    private static void ClearAutomaticAssociation(Conversation conversation, bool hasManualAssociation)
    {
        if (!hasManualAssociation)
        {
            conversation.CustomerId = null;
            conversation.CustomerAssociationSource = null;
        }

        ClearSuggestion(conversation);
    }

    private static void ClearSuggestion(Conversation conversation)
    {
        conversation.SuggestedCustomerId = null;
        conversation.SuggestedCustomerConfidence = null;
        conversation.SuggestedCustomerReasoning = null;
    }

    private void ClearCandidates(Conversation conversation)
    {
        foreach (var candidate in conversation.CustomerCandidates.ToArray())
        {
            if (db.Entry(candidate).State == EntityState.Added)
                db.Entry(candidate).State = EntityState.Detached;
            else if (db.Entry(candidate).State != EntityState.Detached)
                db.ConversationCustomerCandidates.Remove(candidate);
        }
        conversation.CustomerCandidates.Clear();
    }

    private void ReconcileCandidates(Conversation conversation, IReadOnlyList<int> customerIds)
    {
        var wanted = customerIds.ToHashSet();
        foreach (var candidate in conversation.CustomerCandidates.Where(item => !wanted.Contains(item.CustomerId)).ToArray())
        {
            if (db.Entry(candidate).State == EntityState.Added)
                db.Entry(candidate).State = EntityState.Detached;
            else if (db.Entry(candidate).State != EntityState.Detached)
                db.ConversationCustomerCandidates.Remove(candidate);
            conversation.CustomerCandidates.Remove(candidate);
        }

        var existing = conversation.CustomerCandidates.Select(item => item.CustomerId).ToHashSet();
        var now = DateTimeOffset.UtcNow;
        foreach (var customerId in customerIds.Where(item => !existing.Contains(item)))
        {
            db.ConversationCustomerCandidates.Add(new ConversationCustomerCandidate
            {
                ConversationId = conversation.Id,
                CustomerId = customerId,
                CreatedAt = now,
            });
        }
    }

    internal static string NormalizeEmail(string address) => address.Trim().ToLowerInvariant();
}