using System.Net;
using System.Net.Http.Json;

namespace Vantigo.Customers.Api.Tests.Integration;

[Collection(CustomersApiCollection.Name)]
public sealed class ContactsEndpointsTests
{
    private readonly HttpClient _client;

    public ContactsEndpointsTests(CustomersApiFactory factory)
    {
        _client = factory.CreateAuthenticatedClient();
    }

    private async Task<Contact> CreateContact(object request)
    {
        var response = await _client.PostAsJsonAsync("/api/v1/contacts", request);
        Assert.Equal(HttpStatusCode.Created, response.StatusCode);
        return await response.Content.ReadFromJsonAsync<Contact>();
    }

    private async Task<int> CreateCustomer(string name)
    {
        var response = await _client.PostAsJsonAsync("/api/v1/customers", new { name });
        var created = await response.Content.ReadFromJsonAsync<CreatedCustomer>();
        return created.Id;
    }

    [Fact]
    public async Task CreateContact_WithAllFields_ReturnsCreatedContactAndLocation()
    {
        var response = await _client.PostAsJsonAsync("/api/v1/contacts", new
        {
            firstName = "Anders",
            lastName = "Refsdal",
            middleName = "Bernhard",
            prefix = "Dr.",
            suffix = "PhD",
            phone = "+47 934 89 731",
            email = "Anders@Refsdal.NO",
        });

        Assert.Equal(HttpStatusCode.Created, response.StatusCode);

        var contact = await response.Content.ReadFromJsonAsync<Contact>();
        Assert.True(contact.Id > 0);
        Assert.Equal("Anders", contact.FirstName);
        Assert.Equal("Refsdal", contact.LastName);
        Assert.Equal("Bernhard", contact.MiddleName);
        Assert.Equal("Dr.", contact.Prefix);
        Assert.Equal("PhD", contact.Suffix);
        Assert.Equal("+47 934 89 731", contact.Phone);
        Assert.Equal("anders@refsdal.no", contact.Email);
        Assert.Equal($"/api/v1/contacts/{contact.Id}", response.Headers.Location?.AbsolutePath);
    }

    [Fact]
    public async Task CreateContact_WithNamesOnly_LeavesOptionalFieldsNull()
    {
        var contact = await CreateContact(new { firstName = "Kari", lastName = "Nordmann" });

        var fetched = await _client.GetFromJsonAsync<Contact>($"/api/v1/contacts/{contact.Id}");

        Assert.Equal("Kari", fetched.FirstName);
        Assert.Null(fetched.MiddleName);
        Assert.Null(fetched.Phone);
        Assert.Null(fetched.Email);
    }

    [Fact]
    public async Task CreateContact_WithMultipleInvalidFields_ReportsAllErrorsAtOnce()
    {
        var response = await _client.PostAsJsonAsync("/api/v1/contacts", new
        {
            firstName = "",
            lastName = "  ",
            phone = "not a number",
            email = "not-an-email",
        });

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);

        var problem = await response.Content.ReadFromJsonAsync<ValidationProblem>();
        Assert.Equal(
            new[] { "email", "firstName", "lastName", "phone" },
            problem.Errors.Keys.Order());
    }

    [Fact]
    public async Task UpdateContact_ReplacesAllFieldsAndClearsBlankOptionals()
    {
        var contact = await CreateContact(new
        {
            firstName = "Ola",
            lastName = "Nordmann",
            phone = "+47 22 86 44 00",
        });

        var response = await _client.PutAsJsonAsync($"/api/v1/contacts/{contact.Id}", new
        {
            firstName = "Ola",
            lastName = "Nordmann-Hansen",
            email = "ola@nordmann.no",
        });

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);

        var updated = await response.Content.ReadFromJsonAsync<Contact>();
        Assert.Equal("Nordmann-Hansen", updated.LastName);
        Assert.Equal("ola@nordmann.no", updated.Email);
        Assert.Null(updated.Phone);
    }

    [Fact]
    public async Task UpdateContact_WhenContactDoesNotExist_ReturnsNotFound()
    {
        var response = await _client.PutAsJsonAsync("/api/v1/contacts/999999", new
        {
            firstName = "Ghost",
            lastName = "Contact",
        });

        Assert.Equal(HttpStatusCode.NotFound, response.StatusCode);
    }

    [Fact]
    public async Task DeleteContact_RemovesContactAndItsAssociations()
    {
        var contact = await CreateContact(new { firstName = "Delete", lastName = "Me" });
        var customerId = await CreateCustomer("Delete Contact Co");
        await AttachContact(customerId, contact.Id, "CEO");

        var response = await _client.DeleteAsync($"/api/v1/contacts/{contact.Id}");

        Assert.Equal(HttpStatusCode.NoContent, response.StatusCode);
        Assert.Equal(HttpStatusCode.NotFound, (await _client.GetAsync($"/api/v1/contacts/{contact.Id}")).StatusCode);

        var customerContacts = await _client.GetFromJsonAsync<CustomerContactList>($"/api/v1/customers/{customerId}/contacts");
        Assert.Empty(customerContacts.Data);
    }

    [Fact]
    public async Task DeleteContact_WhenContactDoesNotExist_ReturnsNotFound()
    {
        var response = await _client.DeleteAsync("/api/v1/contacts/999999");

        Assert.Equal(HttpStatusCode.NotFound, response.StatusCode);
    }

    [Fact]
    public async Task GetContacts_Search_MatchesNamePartsPhoneAndEmail()
    {
        await CreateContact(new
        {
            firstName = "Searchable",
            lastName = "Contactsen",
            middleName = "Findme",
            phone = "+47 99 88 77 66",
            email = "searchable@contactsen.no",
        });

        var byFirstName = await _client.GetFromJsonAsync<ContactList>("/api/v1/contacts?search=searchable");
        var byMiddleName = await _client.GetFromJsonAsync<ContactList>("/api/v1/contacts?search=findme");
        var byPhone = await _client.GetFromJsonAsync<ContactList>("/api/v1/contacts?search=99%2088%2077");
        var byEmail = await _client.GetFromJsonAsync<ContactList>("/api/v1/contacts?search=searchable%40contactsen");
        var noMatch = await _client.GetFromJsonAsync<ContactList>("/api/v1/contacts?search=no-such-contact");

        Assert.Single(byFirstName.Data, c => c.Contact.FirstName == "Searchable");
        Assert.Single(byMiddleName.Data, c => c.Contact.FirstName == "Searchable");
        Assert.Single(byPhone.Data, c => c.Contact.FirstName == "Searchable");
        Assert.Single(byEmail.Data, c => c.Contact.FirstName == "Searchable");
        Assert.Empty(noMatch.Data);
    }

    [Fact]
    public async Task GetContacts_Search_MatchesFullNamesAcrossNameParts()
    {
        await CreateContact(new
        {
            firstName = "Fullname",
            lastName = "Matchsen",
            middleName = "Bernhard",
        });

        var byFullName = await _client.GetFromJsonAsync<ContactList>("/api/v1/contacts?search=fullname%20matchsen");
        var byReversedOrder = await _client.GetFromJsonAsync<ContactList>("/api/v1/contacts?search=matchsen%20fullname");
        var byFirstAndMiddle = await _client.GetFromJsonAsync<ContactList>("/api/v1/contacts?search=fullname%20bernhard");
        var byPartialTerms = await _client.GetFromJsonAsync<ContactList>("/api/v1/contacts?search=full%20match");
        var withWrongTerm = await _client.GetFromJsonAsync<ContactList>("/api/v1/contacts?search=fullname%20nomatch");

        Assert.Single(byFullName.Data, c => c.Contact.FirstName == "Fullname");
        Assert.Single(byReversedOrder.Data, c => c.Contact.FirstName == "Fullname");
        Assert.Single(byFirstAndMiddle.Data, c => c.Contact.FirstName == "Fullname");
        Assert.Single(byPartialTerms.Data, c => c.Contact.FirstName == "Fullname");
        Assert.Empty(withWrongTerm.Data);
    }

    [Fact]
    public async Task GetContacts_DefaultSort_OrdersByFirstNameThenLastName()
    {
        await CreateContact(new { firstName = "Sorttest Bravo", lastName = "Alpha" });
        await CreateContact(new { firstName = "Sorttest Alpha", lastName = "Bravo" });
        await CreateContact(new { firstName = "Sorttest Alpha", lastName = "Alpha" });

        var response = await _client.GetFromJsonAsync<ContactList>("/api/v1/contacts?search=Sorttest");

        Assert.Equal(
            new[] { ("Sorttest Alpha", "Alpha"), ("Sorttest Alpha", "Bravo"), ("Sorttest Bravo", "Alpha") },
            response.Data.Select(c => ((string)c.Contact.FirstName, (string)c.Contact.LastName)));
    }

    [Fact]
    public async Task GetContacts_ReturnsCustomerCountAndSingleCustomerName()
    {
        var contactAlone = await CreateContact(new { firstName = "Countless", lastName = "Solo" });
        var contactSingle = await CreateContact(new { firstName = "Countless", lastName = "Single" });
        var contactDouble = await CreateContact(new { firstName = "Countless", lastName = "Double" });

        var firstCustomer = await CreateCustomer("Countless First AS");
        var secondCustomer = await CreateCustomer("Countless Second AS");

        await AttachContact(firstCustomer, contactSingle.Id, "CEO");
        await AttachContact(firstCustomer, contactDouble.Id, "CTO");
        await AttachContact(secondCustomer, contactDouble.Id, "Custodian");

        var response = await _client.GetFromJsonAsync<ContactList>("/api/v1/contacts?search=Countless");

        var alone = Assert.Single(response.Data, c => c.Contact.Id == contactAlone.Id);
        Assert.Equal(0, alone.CustomerCount);
        Assert.Null(alone.Customer);

        var single = Assert.Single(response.Data, c => c.Contact.Id == contactSingle.Id);
        Assert.Equal(1, single.CustomerCount);
        Assert.Equal("Countless First AS", single.Customer?.Name);
        Assert.Equal(firstCustomer, single.Customer?.Id);

        var double_ = Assert.Single(response.Data, c => c.Contact.Id == contactDouble.Id);
        Assert.Equal(2, double_.CustomerCount);
        Assert.Null(double_.Customer);
    }

    [Fact]
    public async Task AttachContact_ReturnsAssociationWithContact()
    {
        var contact = await CreateContact(new { firstName = "Attach", lastName = "Mensen", email = "attach@mensen.no" });
        var customerId = await CreateCustomer("Attach Co");

        var response = await _client.PostAsJsonAsync($"/api/v1/customers/{customerId}/contacts", new
        {
            contactId = contact.Id,
            role = "CEO",
            email = "attach@attachco.no",
        });

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);

        var association = await response.Content.ReadFromJsonAsync<CustomerContact>();
        Assert.Equal(contact.Id, association.Contact.Id);
        Assert.Equal("CEO", association.Role);
        Assert.Equal("attach@attachco.no", association.Email);
        Assert.Null(association.Phone);
    }

    [Fact]
    public async Task AttachContact_Twice_ReturnsConflict()
    {
        var contact = await CreateContact(new { firstName = "Conflict", lastName = "Hansen" });
        var customerId = await CreateCustomer("Conflict Co");

        await AttachContact(customerId, contact.Id, "CEO");
        var response = await _client.PostAsJsonAsync($"/api/v1/customers/{customerId}/contacts", new
        {
            contactId = contact.Id,
            role = "CTO",
        });

        Assert.Equal(HttpStatusCode.Conflict, response.StatusCode);
    }

    [Theory]
    [InlineData(999999, null)]
    [InlineData(null, 999999)]
    public async Task AttachContact_WithUnknownCustomerOrContact_ReturnsNotFound(int? customerId, int? contactId)
    {
        var contact = await CreateContact(new { firstName = "Known", lastName = "Contactsen" });
        var knownCustomerId = await CreateCustomer("Known Co");

        var response = await _client.PostAsJsonAsync($"/api/v1/customers/{customerId ?? knownCustomerId}/contacts", new
        {
            contactId = contactId ?? contact.Id,
            role = "CEO",
        });

        Assert.Equal(HttpStatusCode.NotFound, response.StatusCode);
    }

    [Fact]
    public async Task AttachContact_WithInvalidConnection_ReportsFieldErrors()
    {
        var contact = await CreateContact(new { firstName = "Invalid", lastName = "Connection" });
        var customerId = await CreateCustomer("Invalid Connection Co");

        var response = await _client.PostAsJsonAsync($"/api/v1/customers/{customerId}/contacts", new
        {
            contactId = contact.Id,
            role = "",
            email = "not-an-email",
        });

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);

        var problem = await response.Content.ReadFromJsonAsync<ValidationProblem>();
        Assert.Equal(new[] { "email", "role" }, problem.Errors.Keys.Order());
    }

    [Fact]
    public async Task GetCustomerContacts_ReturnsAssociationsSortedByContactName()
    {
        var customerId = await CreateCustomer("Listing Co");
        var second = await CreateContact(new { firstName = "Bravo", lastName = "Listing" });
        var first = await CreateContact(new { firstName = "Alpha", lastName = "Listing", phone = "+47 11 22 33 44" });

        await AttachContact(customerId, second.Id, "CTO");
        await AttachContact(customerId, first.Id, "CEO");

        var response = await _client.GetFromJsonAsync<CustomerContactList>($"/api/v1/customers/{customerId}/contacts");

        Assert.Equal(2, response.Data.Count);
        Assert.Equal(new[] { "Alpha", "Bravo" }, response.Data.Select(a => a.Contact.FirstName));
        Assert.Equal("CEO", response.Data[0].Role);
        Assert.Equal("+47 11 22 33 44", response.Data[0].Contact.Phone);
    }

    [Fact]
    public async Task GetCustomerContacts_WhenCustomerDoesNotExist_ReturnsNotFound()
    {
        var response = await _client.GetAsync("/api/v1/customers/999999/contacts");

        Assert.Equal(HttpStatusCode.NotFound, response.StatusCode);
    }

    [Fact]
    public async Task UpdateCustomerContact_ReplacesConnectionFields()
    {
        var contact = await CreateContact(new { firstName = "Update", lastName = "Connectionsen" });
        var customerId = await CreateCustomer("Update Connection Co");
        await AttachContact(customerId, contact.Id, "CEO");

        var response = await _client.PutAsJsonAsync($"/api/v1/customers/{customerId}/contacts/{contact.Id}", new
        {
            role = "Chairman",
            phone = "+47 55 66 77 88",
        });

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);

        var updated = await response.Content.ReadFromJsonAsync<CustomerContact>();
        Assert.Equal("Chairman", updated.Role);
        Assert.Equal("+47 55 66 77 88", updated.Phone);
        Assert.Null(updated.Email);
    }

    [Fact]
    public async Task UpdateCustomerContact_WhenAssociationDoesNotExist_ReturnsNotFound()
    {
        var customerId = await CreateCustomer("No Association Co");

        var response = await _client.PutAsJsonAsync($"/api/v1/customers/{customerId}/contacts/999999", new
        {
            role = "CEO",
        });

        Assert.Equal(HttpStatusCode.NotFound, response.StatusCode);
    }

    [Fact]
    public async Task DetachContact_RemovesAssociationButKeepsContact()
    {
        var contact = await CreateContact(new { firstName = "Detach", lastName = "Mensen" });
        var customerId = await CreateCustomer("Detach Co");
        await AttachContact(customerId, contact.Id, "CEO");

        var response = await _client.DeleteAsync($"/api/v1/customers/{customerId}/contacts/{contact.Id}");

        Assert.Equal(HttpStatusCode.NoContent, response.StatusCode);

        var customerContacts = await _client.GetFromJsonAsync<CustomerContactList>($"/api/v1/customers/{customerId}/contacts");
        Assert.Empty(customerContacts.Data);

        var stillThere = await _client.GetAsync($"/api/v1/contacts/{contact.Id}");
        Assert.Equal(HttpStatusCode.OK, stillThere.StatusCode);
    }

    [Fact]
    public async Task DetachContact_WhenAssociationDoesNotExist_ReturnsNotFound()
    {
        var customerId = await CreateCustomer("Nothing To Detach Co");

        var response = await _client.DeleteAsync($"/api/v1/customers/{customerId}/contacts/999999");

        Assert.Equal(HttpStatusCode.NotFound, response.StatusCode);
    }

    [Fact]
    public async Task GetContactCustomers_ReturnsAssociationsSortedByCustomerName()
    {
        var contact = await CreateContact(new { firstName = "Manysided", lastName = "Kontaktsen" });
        var bravoCustomer = await CreateCustomer("Manysided Bravo AS");
        var alphaCustomer = await CreateCustomer("Manysided Alpha AS");

        await AttachContact(bravoCustomer, contact.Id, "CTO");
        var attachResponse = await _client.PostAsJsonAsync($"/api/v1/customers/{alphaCustomer}/contacts", new
        {
            contactId = contact.Id,
            role = "CEO",
            phone = "+47 99 00 11 22",
        });
        Assert.Equal(HttpStatusCode.OK, attachResponse.StatusCode);

        var response = await _client.GetFromJsonAsync<ContactCustomerList>($"/api/v1/contacts/{contact.Id}/customers");

        Assert.Equal(2, response.Data.Count);
        Assert.Equal(new[] { "Manysided Alpha AS", "Manysided Bravo AS" }, response.Data.Select(a => a.Customer.Name));
        Assert.Equal("CEO", response.Data[0].Role);
        Assert.Equal("+47 99 00 11 22", response.Data[0].Phone);
        Assert.Null(response.Data[0].Email);
        Assert.Equal(alphaCustomer, response.Data[0].Customer.Id);
    }

    [Fact]
    public async Task GetContactCustomers_WithoutAssociations_ReturnsEmptyList()
    {
        var contact = await CreateContact(new { firstName = "Lonely", lastName = "Kontaktsen" });

        var response = await _client.GetFromJsonAsync<ContactCustomerList>($"/api/v1/contacts/{contact.Id}/customers");

        Assert.Empty(response.Data);
    }

    [Fact]
    public async Task GetContactCustomers_WhenContactDoesNotExist_ReturnsNotFound()
    {
        var response = await _client.GetAsync("/api/v1/contacts/999999/customers");

        Assert.Equal(HttpStatusCode.NotFound, response.StatusCode);
    }

    private async Task AttachContact(int customerId, int contactId, string role)
    {
        var response = await _client.PostAsJsonAsync($"/api/v1/customers/{customerId}/contacts", new
        {
            contactId,
            role,
        });

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
    }

    private readonly record struct CreatedCustomer(int Id);

    private readonly record struct ValidationProblem(Dictionary<string, string[]> Errors);

    private readonly record struct Contact(
        int Id,
        string FirstName,
        string LastName,
        string? MiddleName,
        string? Prefix,
        string? Suffix,
        string? Phone,
        string? Email);

    private readonly record struct ContactListItem(Contact Contact, int CustomerCount, CustomerReference? Customer);

    private readonly record struct CustomerReference(int Id, string Name);

    private readonly record struct ContactList(List<ContactListItem> Data);

    private readonly record struct CustomerContact(Contact Contact, string Role, string? Phone, string? Email);

    private readonly record struct CustomerContactList(List<CustomerContact> Data);

    private readonly record struct ContactCustomer(CustomerReference Customer, string Role, string? Phone, string? Email);

    private readonly record struct ContactCustomerList(List<ContactCustomer> Data);
}