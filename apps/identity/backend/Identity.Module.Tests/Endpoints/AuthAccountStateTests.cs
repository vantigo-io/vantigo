using Vantigo.Identity.Database.Accounts;
using Vantigo.Identity.Endpoints.Auth;

namespace Vantigo.Identity.Tests.Endpoints;

public sealed class AuthAccountStateTests
{
    private static readonly DateTimeOffset Now = new(2026, 8, 12, 12, 0, 0, TimeSpan.Zero);

    [Fact]
    public void DisabledAndTransientLockoutAreIndependent()
    {
        var disabled = new ApplicationUser { DisplayName = "Disabled", IsDisabled = true };
        var locked = new ApplicationUser { DisplayName = "Locked", LockoutEnd = Now.AddMinutes(15) };
        var available = new ApplicationUser { DisplayName = "Available" };

        Assert.False(AuthAccountState.IsActive(disabled, Now));
        Assert.False(AuthAccountState.IsLockedOut(disabled.LockoutEnd, Now));
        Assert.False(AuthAccountState.IsActive(locked, Now));
        Assert.True(AuthAccountState.IsLockedOut(locked.LockoutEnd, Now));
        Assert.True(AuthAccountState.IsActive(available, Now));
    }

    [Fact]
    public void LockoutExpiresWithoutChangingPersistentDisableState()
    {
        var user = new ApplicationUser
        {
            DisplayName = "Disabled and locked",
            IsDisabled = true,
            LockoutEnd = Now.AddMinutes(-1),
        };

        Assert.False(AuthAccountState.IsLockedOut(user.LockoutEnd, Now));
        Assert.False(AuthAccountState.IsActive(user, Now));
        Assert.True(user.IsDisabled);
    }

    [Fact]
    public void RolesAreOrderedOwnerThenUserThenOtherRoles()
    {
        var roles = AuthRoleOrdering.Ordered(["User", "Billing", "Owner", "User"]);

        Assert.Equal(["Owner", "User", "Billing"], roles);
        Assert.Equal("Owner", AuthRoleOrdering.ManagedRole(["User", "Owner"]));
    }
}