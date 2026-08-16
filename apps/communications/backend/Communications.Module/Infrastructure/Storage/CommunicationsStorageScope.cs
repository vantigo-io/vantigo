using Vantigo.Storage.Abstractions;

namespace Vantigo.Communications.Infrastructure.Storage;

public sealed class CommunicationsStorageScope : IStorageScope
{
    public static string Name => "communications";
}