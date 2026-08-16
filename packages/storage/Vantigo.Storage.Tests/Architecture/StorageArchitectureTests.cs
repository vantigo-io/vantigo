using Vantigo.Storage;
using Vantigo.Storage.Abstractions;

namespace Vantigo.Storage.Tests.Architecture;

public sealed class StorageArchitectureTests
{
    [Fact]
    public void Abstractions_have_only_framework_assembly_references()
    {
        var externalReferences = typeof(IObjectStore).Assembly
            .GetReferencedAssemblies()
            .Where(reference => !reference.Name!.StartsWith("System.", StringComparison.Ordinal) &&
                                reference.Name is not "System" &&
                                reference.Name is not "mscorlib")
            .Select(reference => reference.Name)
            .ToArray();

        Assert.Empty(externalReferences);
    }

    [Fact]
    public void Provider_and_backend_types_are_not_public()
    {
        var implementation = typeof(ObjectStorageServiceCollectionExtensions).Assembly;

        Assert.DoesNotContain(implementation.GetExportedTypes(), type =>
            type.Namespace?.StartsWith("Vantigo.Storage.Providers", StringComparison.Ordinal) == true ||
            type.Namespace?.StartsWith("Vantigo.Storage.Initialization", StringComparison.Ordinal) == true ||
            type.Namespace?.StartsWith("Vantigo.Storage.Scoping", StringComparison.Ordinal) == true);
        Assert.DoesNotContain(implementation.GetExportedTypes(), type =>
            typeof(IObjectStore).IsAssignableFrom(type));
    }
}