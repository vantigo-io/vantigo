namespace Vantigo.Products.Domain.Products;

/// <summary>
/// The kind of tax treatment applied by a tax category.
/// </summary>
public enum TaxCategoryKind
{
    /// <summary>The ordinary tax rate for the category.</summary>
    Standard,

    /// <summary>A reduced tax rate for qualifying goods or services.</summary>
    Reduced,

    /// <summary>The transaction is taxable at a zero rate.</summary>
    Zero,

    /// <summary>The transaction is exempt from the tax.</summary>
    Exempt,
}