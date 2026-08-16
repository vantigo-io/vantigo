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
                "communications:channels-manage",
                "communications:conversations-manage",
                "communications:conversations-reply",
                "communications:conversations-view",
                "communications:suppressions-manage",
            ],
            catalog.Permissions.Select(permission => permission.Key).ToArray());
        Assert.True(catalog.GetRequired(CommunicationsPermissions.ConversationsView).Sensitive);
        Assert.Contains("bodies", catalog.GetRequired(CommunicationsPermissions.ConversationsView).Description, StringComparison.OrdinalIgnoreCase);
        Assert.Contains("participants", catalog.GetRequired(CommunicationsPermissions.ConversationsView).Description, StringComparison.OrdinalIgnoreCase);
    }
}