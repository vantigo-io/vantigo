using Vantigo.Customers.Domain.Customers.Common;
using Vantigo.Customers.Domain.Customers.ValueObjects;

namespace Vantigo.Customers.Domain.Customers;

/// <summary>
/// A customer is an aggregate of all the related customer data for a particular customer within
/// the Vantigo Customers system. A customer will always have an id that it can be identified
/// with and belonging data in the form of entities and values.
/// </summary>
public sealed class Customer
{
    /// <summary>
    /// The id is the primary method of identifying a customer. It is an auto incrementable value
    /// that is uniquely identifiable within the system. This value is set by the database once
    /// a customer has been persisted.
    /// </summary>
    public int Id { get; set; }

    /// <summary>
    /// The name is a field that is used to define a "friendly name" of the customer. It is not
    /// necessarily connected to the legal name of the customer, but more of a name that can be
    /// used to identify the customer either before the legal data is known or as an alias.
    /// </summary>
    public FriendlyName Name { get; set; }

    /// <summary>
    /// The Legal identity of the customer is used to correctly identify the customer in the public
    /// registry where the customer is located. For instance, in Norway every business needs to be
    /// present in the "Register of Business Enterprises" which contains the absolute fact about
    /// the business. Same is true for persons.
    /// </summary>
    public LegalIdentity? Identity { get; set; }
}