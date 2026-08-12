using Microsoft.AspNetCore.Identity;

namespace Vantigo.Identity.Database.Accounts;

/// <summary>
/// The local account used by the application cookie. Account data deliberately lives
/// in a separate EF context/schema from the customer domain.
/// </summary>
public sealed class ApplicationUser : IdentityUser<Guid>
{
    public ApplicationUser()
    {
        Id = Guid.NewGuid();
        LockoutEnabled = true;
    }

    public required string DisplayName { get; set; }

    /// <summary>
    /// Persistent administrator-controlled disable state. This is deliberately
    /// separate from <see cref="IdentityUser{TKey}.LockoutEnd"/>, which is the
    /// transient failed-sign-in lockout state.
    /// </summary>
    public bool IsDisabled { get; set; }

    /// <summary>
    /// The user's preferred UI language. Null means Automatic; this slice only
    /// supports English explicitly.
    /// </summary>
    public string? PreferredLanguage { get; set; }

}