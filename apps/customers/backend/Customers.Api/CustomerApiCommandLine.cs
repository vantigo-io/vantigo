namespace Vantigo.Customers.Api;

internal enum CustomerApiCommand
{
    NoArguments,
    Api,
    Migrate,
    Seed,
    Invalid,
}

internal readonly record struct CustomerApiCommandLineResult(
    CustomerApiCommand Command,
    string[] RemainingArguments,
    string? InvalidCommand = null);

internal static class CustomerApiCommandLine
{
    internal const string Usage = "Usage: Customers.Api <api|migrate|seed> [arguments]";

    internal static CustomerApiCommandLineResult Parse(IReadOnlyList<string> arguments)
    {
        if (arguments.Count == 0)
        {
            return new(CustomerApiCommand.NoArguments, []);
        }

        // WebApplicationFactory supplies these host bootstrap switches to the entry
        // point before its test DI registrations are applied. Treat that exact shape
        // as an implicit test candidate; the DI marker is still required after build.
        if (arguments.Any(argument => argument.StartsWith("--environment=", StringComparison.Ordinal)) &&
            arguments.Any(argument => argument.StartsWith("--applicationName=", StringComparison.Ordinal)))
        {
            return new(CustomerApiCommand.NoArguments, arguments.ToArray());
        }

        var command = arguments[0].ToLowerInvariant() switch
        {
            "api" => CustomerApiCommand.Api,
            "migrate" => CustomerApiCommand.Migrate,
            "seed" => CustomerApiCommand.Seed,
            _ => CustomerApiCommand.Invalid,
        };

        return new(command, arguments.Skip(1).ToArray(), command == CustomerApiCommand.Invalid ? arguments[0] : null);
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