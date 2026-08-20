namespace Vantigo.Host;

public enum VantigoCommand
{
    NoArguments,
    Api,
    Migrate,
    Seed,
    ResetCommunications,
    HealthCheck,
    Invalid,
}

public readonly record struct VantigoCommandLineResult(
    VantigoCommand Command,
    string[] RemainingArguments,
    string? InvalidCommand = null);

public static class VantigoCommandLine
{
    public const string Usage = "Usage: Vantigo.Host <api|migrate|seed|reset-communications|healthcheck> [arguments]";

    public static VantigoCommandLineResult Parse(IReadOnlyList<string> arguments)
    {
        if (arguments.Count == 0)
        {
            return new(VantigoCommand.NoArguments, []);
        }

        // WebApplicationFactory supplies host bootstrap switches before the test DI
        // registrations are applied. The startup marker is checked by the host after
        // building, so this remains a safe implicit test mode.
        if (arguments.Any(argument => argument.StartsWith("--environment=", StringComparison.Ordinal)) &&
            arguments.Any(argument => argument.StartsWith("--applicationName=", StringComparison.Ordinal)))
        {
            return new(VantigoCommand.NoArguments, arguments.ToArray());
        }

        var command = arguments[0].ToLowerInvariant() switch
        {
            "api" => VantigoCommand.Api,
            "migrate" => VantigoCommand.Migrate,
            "seed" => VantigoCommand.Seed,
            "reset-communications" => VantigoCommand.ResetCommunications,
            "healthcheck" => VantigoCommand.HealthCheck,
            _ => VantigoCommand.Invalid,
        };

        return new(command, arguments.Skip(1).ToArray(), command == VantigoCommand.Invalid ? arguments[0] : null);
    }

    public static void WriteUsageError(string? errorMessage = null)
    {
        if (errorMessage is not null)
        {
            Console.Error.WriteLine(errorMessage);
        }

        Console.Error.WriteLine(Usage);
        Environment.ExitCode = 2;
    }
}