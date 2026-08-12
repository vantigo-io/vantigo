using Vantigo.Contracts.Authorization;

namespace Vantigo.Energy.Authorization;

internal sealed class EnergyPermissionCatalogContributor : IPermissionCatalogContributor
{
    public void Contribute(PermissionCatalogBuilder catalog)
    {
        catalog
            .Add(new PermissionDescriptor(
                "energy:metering-points-view",
                "View energy metering points",
                "View energy metering point details and listings.",
                "energy",
                "Energy",
                Delegable: true))
            .Add(new PermissionDescriptor(
                "energy:metering-points-manage",
                "Manage energy metering points",
                "Create and update energy metering points.",
                "energy",
                "Energy",
                Delegable: true))
            .Add(new PermissionDescriptor(
                "energy:meters-view",
                "View energy meters",
                "View energy meter history for metering points.",
                "energy",
                "Energy",
                Delegable: true))
            .Add(new PermissionDescriptor(
                "energy:meters-manage",
                "Manage energy meters",
                "Replace meters installed at energy metering points.",
                "energy",
                "Energy",
                Delegable: true))
            .Add(new PermissionDescriptor(
                "energy:consumption-view",
                "View energy consumption",
                "View energy consumption intervals and aggregates.",
                "energy",
                "Energy",
                Delegable: true))
            .Add(new PermissionDescriptor(
                "energy:consumption-manage",
                "Manage energy consumption",
                "Add and replace manual energy consumption intervals.",
                "energy",
                "Energy",
                Delegable: true))
            .Add(new PermissionDescriptor(
                "energy:supply-periods-view",
                "View energy supply periods",
                "View energy supply periods for metering points.",
                "energy",
                "Energy",
                Delegable: true))
            .Add(new PermissionDescriptor(
                "energy:supply-periods-manage",
                "Manage energy supply periods",
                "Create, switch, end, and cancel energy supply periods.",
                "energy",
                "Energy",
                Delegable: true));
    }
}