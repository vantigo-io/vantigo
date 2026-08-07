using System.Reflection;

using Vantigo.Communications.Api.Endpoints;

namespace Vantigo.Communications.Api.Tests.Endpoints;

public sealed class MailboxDtoExposureTests
{
    [Fact]
    public void Public_mailbox_response_dtos_do_not_expose_secrets()
    {
        var dtoTypes = new[]
        {
            typeof(MailboxResponse),
            typeof(MailboxSettingsSummary),
            typeof(MailboxSummaryResponse),
        };
        var secretNames = new[] { "Password", "ApiKey", "SecretCiphertext" };

        Assert.All(dtoTypes, dtoType =>
            Assert.DoesNotContain(dtoType.GetProperties(BindingFlags.Public | BindingFlags.Instance), property =>
                secretNames.Contains(property.Name, StringComparer.OrdinalIgnoreCase)));
    }
}