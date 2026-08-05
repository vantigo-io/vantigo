using Vantigo.Communications.Api;

namespace Vantigo.Communications.Api.Tests.Endpoints;

public sealed class CommunicationsCommandLineTests
{
    [Fact]
    public void No_arguments_are_a_web_application_factory_candidate()
    {
        Assert.Equal(CommunicationsCommandMode.NoArguments, CommunicationsCommandLine.Parse([]).Mode);
        Assert.Equal(
            CommunicationsCommandMode.NoArguments,
            CommunicationsCommandLine.Parse([
                "--environment=Development",
                "--contentRoot=/tmp/communications",
                "--applicationName=Vantigo.Communications.Api",
            ]).Mode);
    }

    [Fact]
    public void Unknown_commands_are_rejected()
    {
        var result = CommunicationsCommandLine.Parse(["unknown"]);

        Assert.Equal(CommunicationsCommandMode.Invalid, result.Mode);
        Assert.Equal("unknown", result.InvalidCommand);
    }

    [Fact]
    public void Valid_command_strips_mode_before_forwarding_remaining_arguments()
    {
        var result = CommunicationsCommandLine.Parse(["api", "--urls", "http://localhost"]);

        Assert.Equal(CommunicationsCommandMode.Api, result.Mode);
        Assert.Equal(["--urls", "http://localhost"], result.RemainingArguments);
    }
}