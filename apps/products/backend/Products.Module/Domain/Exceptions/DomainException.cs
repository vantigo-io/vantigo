namespace Vantigo.Products.Domain.Exceptions;

/// <summary>
/// Thrown when a domain invariant is violated, for instance when a value object
/// is constructed with an invalid value. The message is safe to surface to API
/// consumers as a validation error.
/// </summary>
public class DomainException : Exception
{
    public DomainException() : base() { }
    public DomainException(string message) : base(message) { }
}