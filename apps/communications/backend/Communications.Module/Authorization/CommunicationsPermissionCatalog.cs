using Vantigo.Contracts.Authorization;

namespace Vantigo.Communications.Authorization;

internal static class CommunicationsPermissions
{
    public const string MessagesView = "communications:messages-view";
    public const string MessagesSend = "communications:messages-send";
    public const string MessagesManage = "communications:messages-manage";
    public const string MailboxesView = "communications:mailboxes-view";
    public const string MailboxesManage = "communications:mailboxes-manage";
    public const string SuppressionsView = "communications:suppressions-view";
    public const string SuppressionsManage = "communications:suppressions-manage";
}

internal sealed class CommunicationsPermissionCatalogContributor : IPermissionCatalogContributor
{
    public void Contribute(PermissionCatalogBuilder catalog)
    {
        catalog.Add(new PermissionDescriptor(
            CommunicationsPermissions.MessagesView,
            "View communications messages",
            "View emails, message subjects, bodies, recipients, delivery status, and message events.",
            "communications", "Communications", Sensitive: true));
        catalog.Add(new PermissionDescriptor(
            CommunicationsPermissions.MessagesSend,
            "Send communications messages",
            "Queue email messages for delivery.",
            "communications", "Communications"));
        catalog.Add(new PermissionDescriptor(
            CommunicationsPermissions.MessagesManage,
            "Manage communications messages",
            "Resend, archive, and unarchive email messages.",
            "communications", "Communications"));
        catalog.Add(new PermissionDescriptor(
            CommunicationsPermissions.MailboxesView,
            "View communications mailboxes",
            "View configured shared mailboxes and their delivery settings.",
            "communications", "Communications"));
        catalog.Add(new PermissionDescriptor(
            CommunicationsPermissions.MailboxesManage,
            "Manage communications mailboxes",
            "Create, update, and verify shared mailboxes.",
            "communications", "Communications"));
        catalog.Add(new PermissionDescriptor(
            CommunicationsPermissions.SuppressionsView,
            "View communications suppressions",
            "View suppressed email addresses and suppression reasons.",
            "communications", "Communications"));
        catalog.Add(new PermissionDescriptor(
            CommunicationsPermissions.SuppressionsManage,
            "Manage communications suppressions",
            "Create and remove suppressed email addresses.",
            "communications", "Communications"));
    }
}