using Vantigo.Customers.Domain.Contacts.Common;

namespace Vantigo.Customers.Domain.Contacts;

/// <summary>
/// A contact is a person that can be associated with one or several customers, for
/// instance the CEO of a business customer. Contacts exist independently of their
/// associations: a contact can be created before any customer relation exists and
/// survives when associations are removed.
/// </summary>
public sealed class Contact
{
    /// <summary>
    /// The id is the primary method of identifying a contact. It is an auto incrementable
    /// value that is uniquely identifiable within the system. This value is set by the
    /// database once a contact has been persisted.
    /// </summary>
    public int Id { get; set; }

    /// <summary>
    /// The given name of the contact.
    /// </summary>
    public PersonName FirstName { get; set; }

    /// <summary>
    /// The family name of the contact.
    /// </summary>
    public PersonName LastName { get; set; }

    /// <summary>
    /// The middle name of the contact, when known.
    /// </summary>
    public PersonName? MiddleName { get; set; }

    /// <summary>
    /// An honorific prefix such as "Dr.", when known.
    /// </summary>
    public NamePart? Prefix { get; set; }

    /// <summary>
    /// An honorific suffix such as "Jr." or "PhD", when known.
    /// </summary>
    public NamePart? Suffix { get; set; }

    /// <summary>
    /// The phone number the contact can be reached on, when known. Associations to
    /// customers can carry their own connection-specific phone number.
    /// </summary>
    public PhoneNumber? Phone { get; set; }

    /// <summary>
    /// The email address the contact can be reached on, when known. Associations to
    /// customers can carry their own connection-specific email address.
    /// </summary>
    public EmailAddress? Email { get; set; }
}