using Vantigo.Customers.Api.Domain.Contacts.Common;
using Vantigo.Customers.Api.Domain.Exceptions;

namespace Vantigo.Customers.Api.Tests.Domain.Contacts.Common;

public sealed class PersonNameTests
{
    [Fact]
    public void Construction_TrimsTheValue()
    {
        Assert.Equal("Anders", (string)new PersonName("  Anders  "));
    }

    [Theory]
    [InlineData(null)]
    [InlineData("")]
    [InlineData("   ")]
    public void TryCreate_WithBlankValue_Fails(string? value)
    {
        Assert.False(PersonName.TryCreate(value, out _, out var error));
        Assert.NotNull(error);
    }

    [Fact]
    public void TryCreate_WithTooLongValue_Fails()
    {
        Assert.False(PersonName.TryCreate(new string('a', PersonName.MaxLength + 1), out _, out _));
    }

    [Fact]
    public void Construction_WithInvalidValue_ThrowsDomainException()
    {
        Assert.Throws<DomainException>(() => new PersonName(""));
    }
}

public sealed class PhoneNumberTests
{
    [Theory]
    [InlineData("+47 934 89 731")]
    [InlineData("(555) 123-4567")]
    [InlineData("22.86.44.00")]
    public void TryCreate_WithCommonFormats_Succeeds(string value)
    {
        Assert.True(PhoneNumber.TryCreate(value, out var phone, out _));
        Assert.Equal(value, (string)phone);
    }

    [Theory]
    [InlineData("not a number")]
    [InlineData("+47 934 89 731 ext#2")]
    [InlineData("+-() .")]
    public void TryCreate_WithInvalidCharactersOrNoDigits_Fails(string value)
    {
        Assert.False(PhoneNumber.TryCreate(value, out _, out var error));
        Assert.NotNull(error);
    }
}

public sealed class EmailAddressTests
{
    [Theory]
    [InlineData("anders@vantigo.io", "anders@vantigo.io")]
    [InlineData("  Anders@Vantigo.IO  ", "anders@vantigo.io")]
    public void TryCreate_WithValidValue_NormalizesToLowercase(string value, string expected)
    {
        Assert.True(EmailAddress.TryCreate(value, out var email, out _));
        Assert.Equal(expected, (string)email);
    }

    [Theory]
    [InlineData("no-at-sign")]
    [InlineData("@vantigo.io")]
    [InlineData("anders@")]
    [InlineData("anders@vantigo")]
    [InlineData("anders@vantigo.")]
    [InlineData("an ders@vantigo.io")]
    [InlineData("anders@@vantigo.io")]
    public void TryCreate_WithInvalidShape_Fails(string value)
    {
        Assert.False(EmailAddress.TryCreate(value, out _, out var error));
        Assert.NotNull(error);
    }
}

public sealed class NamePartTests
{
    [Fact]
    public void TryCreate_WithTooLongValue_Fails()
    {
        Assert.False(NamePart.TryCreate(new string('a', NamePart.MaxLength + 1), out _, out _));
    }

    [Fact]
    public void TryCreate_WithValidValue_Succeeds()
    {
        Assert.True(NamePart.TryCreate("Dr.", out var part, out _));
        Assert.Equal("Dr.", (string)part);
    }
}

public sealed class ContactRoleTests
{
    [Fact]
    public void TryCreate_WithValidValue_TrimsAndSucceeds()
    {
        Assert.True(ContactRole.TryCreate("  CEO  ", out var role, out _));
        Assert.Equal("CEO", (string)role);
    }

    [Theory]
    [InlineData("")]
    [InlineData("   ")]
    public void TryCreate_WithBlankValue_Fails(string value)
    {
        Assert.False(ContactRole.TryCreate(value, out _, out var error));
        Assert.NotNull(error);
    }
}