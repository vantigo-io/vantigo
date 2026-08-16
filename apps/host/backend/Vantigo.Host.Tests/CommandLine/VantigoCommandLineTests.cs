namespace Vantigo.Host.Tests.CommandLine;

public sealed class VantigoCommandLineTests
{
    [Fact]
    public void Parse_WithNoArguments_ReturnsNoArguments()
    {
        var result = VantigoCommandLine.Parse([]);

        Assert.Equal(VantigoCommand.NoArguments, result.Command);
    }

    [Fact]
    public void Parse_WithApiCommand_ReturnsApiAndRemainingArguments()
    {
        var result = VantigoCommandLine.Parse(["api", "--urls", "http://localhost"]);

        Assert.Equal(VantigoCommand.Api, result.Command);
        Assert.Equal(["--urls", "http://localhost"], result.RemainingArguments);
    }

    [Theory]
    [InlineData("migrate", VantigoCommand.Migrate)]
    [InlineData("seed", VantigoCommand.Seed)]
    [InlineData("reset-communications", VantigoCommand.ResetCommunications)]
    public void Parse_WithKnownCommand_ReturnsCommand(string argument, VantigoCommand expected)
    {
        var result = VantigoCommandLine.Parse([argument]);

        Assert.Equal(expected, result.Command);
    }

    [Fact]
    public void Parse_WithUnknownCommand_ReturnsInvalid()
    {
        var result = VantigoCommandLine.Parse(["unknown"]);

        Assert.Equal(VantigoCommand.Invalid, result.Command);
        Assert.Equal("unknown", result.InvalidCommand);
    }

    [Fact]
    public void Parse_WithHostBootstrapArguments_ReturnsNoArgumentsForImplicitTestMode()
    {
        var result = VantigoCommandLine.Parse([
            "--environment=Development",
            "--contentRoot=/tmp/customers",
            "--applicationName=Vantigo.Customers",
        ]);

        Assert.Equal(VantigoCommand.NoArguments, result.Command);
    }

    [Fact]
    public void Parse_WithDashDashUnknownFlag_ReturnsInvalid()
    {
        var result = VantigoCommandLine.Parse(["--unknown"]);

        Assert.Equal(VantigoCommand.Invalid, result.Command);
    }
}