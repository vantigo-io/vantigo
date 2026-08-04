using Microsoft.AspNetCore.Identity;

namespace Vantigo.Communications.Api.Database.Accounts;

public sealed class ApplicationUser : IdentityUser<Guid>
{
    public ApplicationUser()
    {
        Id = Guid.NewGuid();
        LockoutEnabled = true;
    }

    public string DisplayName { get; set; } = string.Empty;
}