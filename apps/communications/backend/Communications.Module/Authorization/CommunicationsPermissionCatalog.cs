using Vantigo.Contracts.Authorization;

namespace Vantigo.Communications.Authorization;

internal static class CommunicationsPermissions
{
    public const string ConversationsView = "communications:conversations-view";
    public const string ConversationsReply = "communications:conversations-reply";
    public const string ConversationsManage = "communications:conversations-manage";
    public const string ChannelsManage = "communications:channels-manage";
    public const string SuppressionsManage = "communications:suppressions-manage";
}

internal sealed class CommunicationsPermissionCatalogContributor : IPermissionCatalogContributor
{
    public void Contribute(PermissionCatalogBuilder catalog)
    {
        catalog.Add(new PermissionDescriptor(CommunicationsPermissions.ConversationsView, "View communications conversations", "View conversations, messages, participants, bodies, attachments, and tags.", "communications", "Communications", Sensitive: true));
        catalog.Add(new PermissionDescriptor(CommunicationsPermissions.ConversationsReply, "Reply to communications conversations", "Create outbound conversation messages and queue them for delivery.", "communications", "Communications"));
        catalog.Add(new PermissionDescriptor(CommunicationsPermissions.ConversationsManage, "Manage communications conversations", "Assign, close, tag, and add internal notes to conversations.", "communications", "Communications"));
        catalog.Add(new PermissionDescriptor(CommunicationsPermissions.ChannelsManage, "Manage communications channels", "Create, update, and verify configured communication channels.", "communications", "Communications"));
        catalog.Add(new PermissionDescriptor(CommunicationsPermissions.SuppressionsManage, "Manage communications suppressions", "View, create, and remove suppressed email addresses.", "communications", "Communications"));
    }
}