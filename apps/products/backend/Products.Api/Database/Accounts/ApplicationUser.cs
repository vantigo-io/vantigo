using Microsoft.AspNetCore.Identity;

namespace Vantigo.Products.Api.Database.Accounts;

/// <summary>
/// The local account used by the application cookie. Account data deliberately lives
/// in a separate EF context/schema from the product domain.
/// </summary>
public sealed class ApplicationUser : IdentityUser<Guid>
{
    public ApplicationUser()
    {
        Id = Guid.NewGuid();
        LockoutEnabled = true;
    }

    public required string DisplayName { get; set; }
}