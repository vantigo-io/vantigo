using Vantigo.Communications.Authorization;
using Vantigo.Contracts.Authorization;

namespace Vantigo.Communications.Module.Tests.Authorization;

public sealed class CommunicationsPermissionCatalogTests
{
    [Fact]
    public void ContributorRegistersTheCommunicationsPermissionInventory()
    {
        var catalog = PermissionCatalog.Create([new CommunicationsPermissionCatalogContributor()]);

        Assert.Equal(
            [
                "communications:mailboxes-manage",
                "communications:mailboxes-view",
                "communications:messages-manage",
                "communications:messages-send",
                "communications:messages-view",
                "communications:suppressions-manage",
                "communications:suppressions-view",
            ],
            catalog.Permissions.Select(permission => permission.Key).ToArray());
        Assert.True(catalog.GetRequired(CommunicationsPermissions.MessagesView).Sensitive);
        Assert.Contains("emails", catalog.GetRequired(CommunicationsPermissions.MessagesView).Description, StringComparison.OrdinalIgnoreCase);
        Assert.Contains("bodies", catalog.GetRequired(CommunicationsPermissions.MessagesView).Description, StringComparison.OrdinalIgnoreCase);
        Assert.Contains("recipients", catalog.GetRequired(CommunicationsPermissions.MessagesView).Description, StringComparison.OrdinalIgnoreCase);
    }
}