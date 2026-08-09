namespace Vantigo.Products.Api;

internal enum ProductApiCommand
{
    NoArguments,
    Api,
    Migrate,
    Seed,
    Invalid,
}

internal readonly record struct ProductApiCommandLineResult(
    ProductApiCommand Command,
    string[] RemainingArguments,
    string? InvalidCommand = null);

internal static class ProductApiCommandLine
{
    internal const string Usage = "Usage: Products.Api <api|migrate|seed> [arguments]";

    internal static ProductApiCommandLineResult Parse(IReadOnlyList<string> arguments)
    {
        if (arguments.Count == 0)
        {
            return new(ProductApiCommand.NoArguments, []);
        }

        // WebApplicationFactory supplies these host bootstrap switches to the entry
        // point before its test DI registrations are applied. Treat that exact shape
        // as an implicit test candidate; the DI marker is still required after build.
        if (arguments.Any(argument => argument.StartsWith("--environment=", StringComparison.Ordinal)) &&
            arguments.Any(argument => argument.StartsWith("--applicationName=", StringComparison.Ordinal)))
        {
            return new(ProductApiCommand.NoArguments, arguments.ToArray());
        }

        var command = arguments[0].ToLowerInvariant() switch
        {
            "api" => ProductApiCommand.Api,
            "migrate" => ProductApiCommand.Migrate,
            "seed" => ProductApiCommand.Seed,
            _ => ProductApiCommand.Invalid,
        };

        return new(command, arguments.Skip(1).ToArray(), command == ProductApiCommand.Invalid ? arguments[0] : null);
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