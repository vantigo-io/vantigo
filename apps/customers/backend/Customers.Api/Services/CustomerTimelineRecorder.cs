using System.Text.Json;

using Vantigo.Customers.Api.Database.Customers;
using Vantigo.Customers.Api.Domain.Contacts;
using Vantigo.Customers.Api.Domain.Customers;
using Vantigo.Customers.Api.Domain.Timeline;

namespace Vantigo.Customers.Api.Services;

public interface ICustomerTimelineRecorder
{
    void RecordCustomerCreated(Customer customer);
    void RecordCustomerUpdated(Customer customer, CustomerSnapshot before);
    void RecordContactAttached(Customer customer, CustomerContact association);
    void RecordContactRelationshipUpdated(Customer customer, CustomerContact association);
    void RecordContactDetached(CustomerContact association);
    void RecordContactRemoved(CustomerContact association);
}

/// <summary>
/// Stages explicit, endpoint-owned generated events in the current DbContext. It never
/// saves independently, which keeps the domain mutation and its event in one unit of work.
/// </summary>
internal sealed class CustomerTimelineRecorder(CustomersDbContext dbContext) : ICustomerTimelineRecorder
{
    private const string Producer = "customers.api";

    public void RecordCustomerCreated(Customer customer)
    {
        var snapshot = CustomerSnapshot.From(customer);
        Add(customer.Id, "customer.created", $"Customer created: {snapshot.Name}",
            new { customerId = customer.Id, customerName = snapshot.Name, legalIdentity = snapshot.Identity });
    }

    public void RecordCustomerUpdated(Customer customer, CustomerSnapshot before)
    {
        var after = CustomerSnapshot.From(customer);
        var legalIdentityChanged = before.Identity != after.Identity;
        var legalIdentityChange = !legalIdentityChanged
            ? null
            : before.Identity is null
                ? "legal identity added"
                : after.Identity is null ? "legal identity removed" : "legal identity updated";
        var summary = legalIdentityChange is null
            ? $"Customer updated: {after.Name}"
            : $"Customer updated: {after.Name} — {legalIdentityChange}";

        var beforePayload = new Dictionary<string, object?> { ["customerName"] = before.Name };
        var afterPayload = new Dictionary<string, object?> { ["customerName"] = after.Name };
        var changesPayload = new Dictionary<string, object?>();
        if (legalIdentityChanged)
        {
            beforePayload["legalIdentity"] = before.Identity;
            afterPayload["legalIdentity"] = after.Identity;
            changesPayload["legalIdentity"] = new { before = before.Identity, after = after.Identity };
        }

        if (before.Name != after.Name)
        {
            changesPayload["customerName"] = new { before = before.Name, after = after.Name };
        }

        Add(customer.Id, "customer.updated", summary, new
        {
            customerId = customer.Id,
            before = beforePayload,
            after = afterPayload,
            changes = changesPayload,
        }, payloadVersion: 2);
    }

    public void RecordContactAttached(Customer customer, CustomerContact association) =>
        AddContact(customer.Id, "customer.contact_attached", association, "Contact attached");

    public void RecordContactRelationshipUpdated(Customer customer, CustomerContact association) =>
        AddContact(customer.Id, "customer.contact_relationship_updated", association, "Contact relationship updated");

    public void RecordContactDetached(CustomerContact association) =>
        AddContact(association.CustomerId, "customer.contact_detached", association, "Contact unlinked");

    public void RecordContactRemoved(CustomerContact association) =>
        AddContact(association.CustomerId, "customer.contact_removed", association, "Contact removed");

    private void AddContact(int customerId, string eventType, CustomerContact association, string summary)
    {
        var firstName = (string)association.Contact.FirstName;
        var middleName = association.Contact.MiddleName is { } middle ? (string)middle : null;
        var lastName = (string)association.Contact.LastName;
        var contactName = string.Join(" ", new[] { firstName, middleName, lastName }.Where(part => !string.IsNullOrWhiteSpace(part)));
        var action = eventType switch
        {
            "customer.contact_attached" => "Contact linked",
            "customer.contact_detached" => "Contact unlinked",
            "customer.contact_removed" => "Contact removed",
            _ => summary,
        };
        Add(customerId, eventType, $"{action}: {contactName} (#{association.ContactId})", new
        {
            customerId,
            contactId = association.ContactId,
            displayName = contactName,
            firstName,
            middleName,
            lastName,
            role = (string)association.Role,
            phone = association.Phone is { } phone ? (string)phone : null,
            email = association.Email is { } email ? (string)email : null,
        });
    }

    private void Add(int customerId, string type, string summary, object payload, int payloadVersion = 1)
    {
        var now = DateTimeOffset.UtcNow;
        var entry = new CustomerTimelineEntry
        {
            CustomerId = customerId,
            Provenance = TimelineProvenance.Generated,
            Producer = Producer,
            EventType = type,
            OccurredOn = DateOnly.FromDateTime(now.UtcDateTime),
            OccurredAt = now,
            Summary = summary,
            PayloadJson = JsonSerializer.Serialize(payload),
            PayloadVersion = payloadVersion,
            CurrentRevision = 1,
            State = TimelineState.Active,
            ActorKind = TimelineActorKind.System,
            ActorDisplay = "System",
            CreatedAt = now,
            UpdatedAt = now,
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
            PayloadJson = entry.PayloadJson,
            PayloadVersion = entry.PayloadVersion,
            State = entry.State,
            ActorKind = entry.ActorKind,
            ActorDisplay = entry.ActorDisplay,
            CreatedAt = now,
        });

        dbContext.CustomerTimelineEntries.Add(entry);
    }
}

public sealed record CustomerSnapshot(string Name, CustomerIdentitySnapshot? Identity)
{
    public static CustomerSnapshot From(Customer customer) => new(
        (string)customer.Name,
        customer.Identity is { } identity
            ? new CustomerIdentitySnapshot((string)identity.Country, (string)identity.Type, (string)identity.Id,
                (string)identity.Name, (string)identity.Source)
            : null);
}

public sealed record CustomerIdentitySnapshot(string Country, string Type, string Id, string Name, string Source);
