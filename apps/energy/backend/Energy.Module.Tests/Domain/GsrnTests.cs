using Vantigo.Energy.Domain.MeteringPoints;

namespace Vantigo.Energy.Module.Tests.Domain;

public sealed class GsrnTests
{
    [Theory]
    [InlineData("707057500000000001")]
    [InlineData("123456789012345678")]
    public void Accepts_eighteen_digits(string value) => Assert.True(Gsrn.IsValid(value));

    [Theory]
    [InlineData("")]
    [InlineData("123")]
    [InlineData("70705750000000000A")]
    public void Rejects_non_eighteen_digit_values(string value) => Assert.False(Gsrn.IsValid(value));
}