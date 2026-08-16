namespace Vantigo.Tenancy.Abstractions;

/// <summary>
/// The strongly-typed identifier of a tenant. Never empty when resolved.
/// </summary>
public readonly record struct TenantId(Guid Value)
{
    public static TenantId New() => new(Guid.NewGuid());

    public bool IsEmpty => Value == Guid.Empty;

    public override string ToString() => Value.ToString();
}