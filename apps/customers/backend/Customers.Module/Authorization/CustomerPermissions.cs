namespace Vantigo.Customers.Authorization;

internal static class CustomerPermissions
{
    internal const string View = "customers:view";
    internal const string Create = "customers:create";
    internal const string Update = "customers:update";
    internal const string Delete = "customers:delete";
    internal const string LegalIdentityView = "customers:legal-identity-view";
    internal const string LegalIdentityManage = "customers:legal-identity-manage";
    internal const string ContactsView = "customers:contacts-view";
    internal const string ContactsManage = "customers:contacts-manage";
    internal const string AssociationsView = "customers:associations-view";
    internal const string AssociationsManage = "customers:associations-manage";
    internal const string TimelineView = "customers:timeline-view";
    internal const string TimelineManage = "customers:timeline-manage";
    internal const string LookupView = "customers:lookup-view";
}