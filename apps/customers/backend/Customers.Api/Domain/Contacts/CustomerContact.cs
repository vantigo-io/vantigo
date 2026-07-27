using Vantigo.Customers.Api.Domain.Contacts.Common;
using Vantigo.Customers.Api.Domain.Customers;

namespace Vantigo.Customers.Api.Domain.Contacts;

/// <summary>
/// The association between a customer and a contact, describing the role the contact
/// holds for that particular customer (e.g. "CEO") together with optional
/// connection-specific contact details, such as the work email used at that company.
/// A contact can be associated with a customer at most once.
/// </summary>
public sealed class CustomerContact
{
    /// <summary>
    /// The id of the customer the contact is associated with.
    /// </summary>
    public int CustomerId { get; set; }

    /// <summary>
    /// The id of the associated contact.
    /// </summary>
    public int ContactId { get; set; }

    /// <summary>
    /// The role the contact holds for the customer, such as "CEO" or "Custodian".
    /// </summary>
    public ContactRole Role { get; set; }

    /// <summary>
    /// A connection-specific phone number, when it differs from the contact's own.
    /// </summary>
    public PhoneNumber? Phone { get; set; }

    /// <summary>
    /// A connection-specific email address, when it differs from the contact's own.
    /// </summary>
    public EmailAddress? Email { get; set; }

    public Customer Customer { get; set; } = null!;
    public Contact Contact { get; set; } = null!;
}