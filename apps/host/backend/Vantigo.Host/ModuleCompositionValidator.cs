using Vantigo.Contracts;

namespace Vantigo.Host;

/// <summary>
/// Fails startup when the configured module combination cannot satisfy the
/// cross-module contracts the enabled modules consume.
/// </summary>
/// <remarks>
/// Communications and Energy read customer and contact data through
/// <see cref="ICustomerDirectory"/>, which only the Customers module implements.
/// Hosting either of them without Customers used to surface much later as an
/// unresolvable dependency in the middle of a request or, in Development, as a
/// raw container validation failure. The combination is rejected here instead,
/// while the message can still name the two flags that disagree.
/// </remarks>
public static class ModuleCompositionValidator
{
    private static readonly ModuleContractRequirement[] Requirements =
    [
        new(ModuleActivation.CommunicationsModule, typeof(ICustomerDirectory), ModuleActivation.CustomersModule),
        new(ModuleActivation.EnergyModule, typeof(ICustomerDirectory), ModuleActivation.CustomersModule),
    ];

    /// <summary>
    /// Asserts that every contract the enabled modules require has a registered
    /// implementation. Runs before the container is built so the reported error
    /// names the invalid module combination rather than a missing service.
    /// </summary>
    public static void Validate(ModuleActivation activation, IServiceCollection services)
    {
        ArgumentNullException.ThrowIfNull(activation);
        ArgumentNullException.ThrowIfNull(services);

        List<string> failures = [];
        foreach (ModuleContractRequirement requirement in Requirements)
        {
            if (!activation.IsEnabled(requirement.Module)) continue;
            if (services.Any(descriptor => descriptor.ServiceType == requirement.Contract)) continue;
            failures.Add(
                $"the {requirement.Module} module requires {requirement.Contract.Name}, which the " +
                $"{requirement.Provider} module implements, but Modules:{requirement.Provider}:Enabled is false " +
                $"(set Modules:{requirement.Provider}:Enabled=true or Modules:{requirement.Module}:Enabled=false)");
        }

        if (failures.Count == 0) return;

        throw new InvalidOperationException(
            "The configured module combination cannot be hosted: " +
            string.Join("; ", failures) +
            ". Enabled modules: " +
            (activation.EnabledModuleNames.Count == 0 ? "none" : string.Join(", ", activation.EnabledModuleNames)) +
            ".");
    }

    private sealed record ModuleContractRequirement(string Module, Type Contract, string Provider);
}