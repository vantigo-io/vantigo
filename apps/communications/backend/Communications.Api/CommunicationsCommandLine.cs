namespace Vantigo.Communications.Api;

internal enum CommunicationsCommandMode
{
    NoArguments,
    Api,
    Migrate,
    Seed,
    Invalid,
}

internal readonly record struct CommunicationsCommandLineResult(
    CommunicationsCommandMode Mode,
    IReadOnlyList<string> RemainingArguments,
    string? InvalidCommand = null);

internal static class CommunicationsCommandLine
{
    internal const string Usage = "Usage: Communications.Api <api|migrate|seed> [arguments]";

    internal static CommunicationsCommandLineResult Parse(IReadOnlyList<string> arguments)
    {
        if (arguments.Count == 0)
        {
            return new(CommunicationsCommandMode.NoArguments, []);
        }

        // WebApplicationFactory supplies these host bootstrap switches to the entry
        // point before its test DI registrations are applied. Treat that exact shape
        // as an implicit test candidate; the DI marker is still required after build.
        if (arguments.Any(argument => argument.StartsWith("--environment=", StringComparison.Ordinal)) &&
            arguments.Any(argument => argument.StartsWith("--applicationName=", StringComparison.Ordinal)))
        {
            return new(CommunicationsCommandMode.NoArguments, arguments.ToArray());
        }

        var mode = arguments[0].ToLowerInvariant() switch
        {
            "api" => CommunicationsCommandMode.Api,
            "migrate" => CommunicationsCommandMode.Migrate,
            "seed" => CommunicationsCommandMode.Seed,
            _ => CommunicationsCommandMode.Invalid,
        };

        return new(
            mode,
            arguments.Skip(1).ToArray(),
            mode == CommunicationsCommandMode.Invalid ? arguments[0] : null);
    }

    internal static void WriteUsageError(string? errorMessage = null)
    {
        if (errorMessage is not null)
        {
            Console.Error.WriteLine(errorMessage);
        }

        Console.Error.WriteLine(Usage);
        Environment.ExitCode = 2;
    }
}