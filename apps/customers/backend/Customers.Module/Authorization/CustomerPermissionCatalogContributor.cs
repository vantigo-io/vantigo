using Vantigo.Contracts.Authorization;

namespace Vantigo.Customers.Authorization;

internal sealed class CustomerPermissionCatalogContributor : IPermissionCatalogContributor
{
    public void Contribute(PermissionCatalogBuilder catalog)
    {
        catalog
            .Add(new("customers:view", "View customers",
                "View customer names, identifiers, and a sanitized activity summary.",
                "customers", "Customers"))
            .Add(new("customers:create", "Create customers",
                "Create customers without legal identity data.",
                "customers", "Customers"))
            .Add(new("customers:update", "Update customers",
                "Update customer names and basic non-sensitive details.",
                "customers", "Customers"))
            .Add(new("customers:delete", "Delete customers",
                "Delete customers and their customer-owned records.",
                "customers", "Customers", Sensitive: true))
            .Add(new("customers:legal-identity-view", "View legal identities",
                "View customer legal identity and registry attribution.",
                "customers", "Legal identity", Sensitive: true))
            .Add(new("customers:legal-identity-manage", "Manage legal identities",
                "Add, replace, or remove customer legal identity data.",
                "customers", "Legal identity", Sensitive: true))
            .Add(new("customers:contacts-view", "View contacts",
                "View contact names and contact details.",
                "customers", "Contacts", Sensitive: true))
            .Add(new("customers:contacts-manage", "Manage contacts",
                "Create, update, and delete contacts.",
                "customers", "Contacts", Sensitive: true))
            .Add(new("customers:associations-view", "View customer associations",
                "View links between customers and contacts.",
                "customers", "Associations", Sensitive: true))
            .Add(new("customers:associations-manage", "Manage customer associations",
                "Create, update, and remove customer-contact links.",
                "customers", "Associations", Sensitive: true))
            .Add(new("customers:timeline-view", "View customer timeline",
                "View customer timeline entries, notes, provenance, and revisions.",
                "customers", "Timeline", Sensitive: true))
            .Add(new("customers:timeline-manage", "Manage customer timeline",
                "Create, update, and delete customer timeline entries.",
                "customers", "Timeline", Sensitive: true))
            .Add(new("customers:lookup-view", "Use registry lookup",
                "Search the external business registry for legal identities.",
                "customers", "Lookup", Sensitive: true));
    }
}