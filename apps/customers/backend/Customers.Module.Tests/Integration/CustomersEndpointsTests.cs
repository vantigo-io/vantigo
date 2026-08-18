using System.Net;
using System.Net.Http.Json;

namespace Vantigo.Customers.Module.Tests.Integration;

[Collection(CustomersApiCollection.Name)]
public sealed class CustomersEndpointsTests
{
    private readonly HttpClient _client;

    public CustomersEndpointsTests(CustomersApiFactory factory)
    {
        _client = factory.CreateAuthenticatedClient();
    }

    [Fact]
    public async Task CreateCustomer_WithNameOnly_ReturnsCreatedWithLocation()
    {
        var response = await _client.PostAsJsonAsync("/api/v1/customers", new
        {
            name = "Wayne Enterprises",
        });

        Assert.Equal(HttpStatusCode.Created, response.StatusCode);

        var created = await response.Content.ReadFromJsonAsync<CreatedCustomer>();
        Assert.True(created.Id > 0);
        Assert.Equal($"/api/v1/customers/{created.Id}", response.Headers.Location?.AbsolutePath);
    }

    [Fact]
    public async Task CreateCustomer_WithLegalIdentity_PersistsIdentity()
    {
        var response = await _client.PostAsJsonAsync("/api/v1/customers", new
        {
            name = "Acme",
            identity = new
            {
                country = "NO",
                type = "Business",
                id = "923609016",
                name = "Acme AS",
                source = "brreg",
            },
        });

        Assert.Equal(HttpStatusCode.Created, response.StatusCode);
        var created = await response.Content.ReadFromJsonAsync<CreatedCustomer>();

        var customer = await _client.GetFromJsonAsync<Customer>($"/api/v1/customers/{created.Id}");

        Assert.Equal("Acme", customer.Name);
        // The caller has the legal-identity view permission, so the safe projection carries
        // an identity summary (country, type and legal id only).
        Assert.NotNull(customer.Identity);
        Assert.Equal("no", customer.Identity.Value.Country);
        Assert.Equal("business", customer.Identity.Value.Type);
        Assert.Equal("923609016", customer.Identity.Value.Id);
        var identity = await _client.GetFromJsonAsync<LegalIdentity>(
            $"/api/v1/customers/{created.Id}/legal-identity");
        Assert.Equal("no", identity.Country);
        Assert.Equal("business", identity.Type);
        Assert.Equal("923609016", identity.Id);
        Assert.Equal("Acme AS", identity.Name);
        Assert.Equal("brreg", identity.Source);
    }

    [Theory]
    [InlineData("")]
    [InlineData("   ")]
    public async Task CreateCustomer_WithInvalidLegalId_ReturnsBadRequestWithFieldError(string legalId)
    {
        var response = await _client.PostAsJsonAsync("/api/v1/customers", new
        {
            name = "Acme",
            identity = new
            {
                country = "no",
                type = "business",
                id = legalId,
                name = "Acme AS",
                source = "manual",
            },
        });

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);

        var problem = await response.Content.ReadFromJsonAsync<ValidationProblem>();
        Assert.Contains("identity.id", problem.Errors.Keys);
    }

    [Fact]
    public async Task CreateCustomer_WithEmptyName_ReturnsBadRequestWithFieldError()
    {
        var response = await _client.PostAsJsonAsync("/api/v1/customers", new
        {
            name = "",
        });

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);

        var problem = await response.Content.ReadFromJsonAsync<ValidationProblem>();
        var error = Assert.Single(problem.Errors);
        Assert.Equal("name", error.Key);
    }

    [Fact]
    public async Task CreateCustomer_WithMultipleInvalidFields_ReportsAllErrorsAtOnce()
    {
        var response = await _client.PostAsJsonAsync("/api/v1/customers", new
        {
            name = "",
            identity = new
            {
                country = "",
                type = "business",
                id = "  ",
                name = "Acme AS",
                source = "manual",
            },
        });

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);

        var problem = await response.Content.ReadFromJsonAsync<ValidationProblem>();
        Assert.Equal(
            new[] { "identity.country", "identity.id", "name" },
            problem.Errors.Keys.Order());
    }

    [Fact]
    public async Task CreateCustomer_WithTooLongLegalName_ReturnsBadRequest()
    {
        var response = await _client.PostAsJsonAsync("/api/v1/customers", new
        {
            name = "Acme",
            identity = new
            {
                country = "no",
                type = "business",
                id = "923609016",
                name = new string('a', 256),
                source = "manual",
            },
        });

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
    }

    [Theory]
    [InlineData("")]
    [InlineData("   ")]
    [InlineData("bogus")]
    [InlineData("BRREG!")]
    public async Task CreateCustomer_WithInvalidLegalSource_ReturnsBadRequestWithFieldError(string source)
    {
        var response = await _client.PostAsJsonAsync("/api/v1/customers", new
        {
            name = "Acme",
            identity = new
            {
                country = "no",
                type = "business",
                id = "923609016",
                name = "Acme AS",
                source,
            },
        });

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);

        var problem = await response.Content.ReadFromJsonAsync<ValidationProblem>();
        var error = Assert.Single(problem.Errors);
        Assert.Equal("identity.source", error.Key);
    }

    [Theory]
    [InlineData("brreg")]
    [InlineData("Manual")]
    public async Task CreateCustomer_WithValidLegalSource_PersistsNormalizedSource(string source)
    {
        var createResponse = await _client.PostAsJsonAsync("/api/v1/customers", new
        {
            name = "Sourced",
            identity = new
            {
                country = "no",
                type = "business",
                id = "923609016",
                name = "Sourced AS",
                source,
            },
        });
        var created = await createResponse.Content.ReadFromJsonAsync<CreatedCustomer>();

        var identity = await _client.GetFromJsonAsync<LegalIdentity>(
            $"/api/v1/customers/{created.Id}/legal-identity");

        Assert.Equal(source.ToLower(), identity.Source);
    }

    [Fact]
    public async Task GetCustomer_WhenCustomerDoesNotExist_ReturnsNotFound()
    {
        var response = await _client.GetAsync("/api/v1/customers/999999");

        Assert.Equal(HttpStatusCode.NotFound, response.StatusCode);
    }

    [Fact]
    public async Task UpdateCustomer_WithValidName_UpdatesName()
    {
        var createResponse = await _client.PostAsJsonAsync("/api/v1/customers", new
        {
            name = "Initech",
        });
        var created = await createResponse.Content.ReadFromJsonAsync<CreatedCustomer>();

        var response = await _client.PutAsJsonAsync($"/api/v1/customers/{created.Id}", new
        {
            name = "Initrode",
        });

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);

        var updated = await response.Content.ReadFromJsonAsync<Customer>();
        Assert.Equal(created.Id, updated.Id);
        Assert.Equal("Initrode", updated.Name);

        var fetched = await _client.GetFromJsonAsync<Customer>($"/api/v1/customers/{created.Id}");
        Assert.Equal(updated, fetched);
    }

    [Fact]
    public async Task UpdateCustomer_WithIdentity_ReplacesIdentity()
    {
        var createResponse = await _client.PostAsJsonAsync("/api/v1/customers", new
        {
            name = "Replaceable",
        });
        var created = await createResponse.Content.ReadFromJsonAsync<CreatedCustomer>();

        var response = await _client.PutAsJsonAsync($"/api/v1/customers/{created.Id}", new
        {
            name = "Replaceable",
            identity = new
            {
                country = "NO",
                type = "Business",
                id = "923609016",
                name = "Replaceable AS",
                source = "brreg",
            },
        });

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        var identity = await _client.GetFromJsonAsync<LegalIdentity>(
            $"/api/v1/customers/{created.Id}/legal-identity");
        Assert.Equal("no", identity.Country);
        Assert.Equal("business", identity.Type);
        Assert.Equal("923609016", identity.Id);
        Assert.Equal("Replaceable AS", identity.Name);
        Assert.Equal("brreg", identity.Source);
    }

    [Fact]
    public async Task UpdateCustomer_WithoutIdentity_RemovesExistingIdentity()
    {
        var createResponse = await _client.PostAsJsonAsync("/api/v1/customers", new
        {
            name = "Removable",
            identity = new
            {
                country = "no",
                type = "business",
                id = "912345670",
                name = "Removable AS",
                source = "manual",
            },
        });
        var created = await createResponse.Content.ReadFromJsonAsync<CreatedCustomer>();

        var response = await _client.DeleteAsync($"/api/v1/customers/{created.Id}/legal-identity");

        Assert.Equal(HttpStatusCode.NoContent, response.StatusCode);

        var updated = await _client.GetFromJsonAsync<Customer>($"/api/v1/customers/{created.Id}");
        Assert.Null(updated.Identity);

        var fetched = await _client.GetFromJsonAsync<Customer>($"/api/v1/customers/{created.Id}");
        Assert.Null(fetched.Identity);
        Assert.Equal(HttpStatusCode.NoContent,
            (await _client.GetAsync($"/api/v1/customers/{created.Id}/legal-identity")).StatusCode);
    }

    [Fact]
    public async Task UpdateCustomer_WithInvalidIdentityFields_ReportsAllErrorsAtOnce()
    {
        var createResponse = await _client.PostAsJsonAsync("/api/v1/customers", new
        {
            name = "Identity Validation Co",
        });
        var created = await createResponse.Content.ReadFromJsonAsync<CreatedCustomer>();

        var response = await _client.PutAsJsonAsync($"/api/v1/customers/{created.Id}/legal-identity", new
        {
            name = "",
            identity = new
            {
                country = "",
                type = "business",
                id = " ",
                name = "Acme AS",
                source = "manual",
            },
        });

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);

        Assert.Contains("BadHttpRequestException", await response.Content.ReadAsStringAsync());
    }

    [Theory]
    [InlineData("")]
    [InlineData("   ")]
    public async Task UpdateCustomer_WithInvalidName_ReturnsBadRequestWithFieldError(string name)
    {
        var createResponse = await _client.PostAsJsonAsync("/api/v1/customers", new
        {
            name = "Update Validation Co",
        });
        var created = await createResponse.Content.ReadFromJsonAsync<CreatedCustomer>();

        var response = await _client.PutAsJsonAsync($"/api/v1/customers/{created.Id}", new
        {
            name,
        });

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);

        var problem = await response.Content.ReadFromJsonAsync<ValidationProblem>();
        var error = Assert.Single(problem.Errors);
        Assert.Equal("name", error.Key);
    }

    [Fact]
    public async Task UpdateCustomer_WhenCustomerDoesNotExist_ReturnsNotFound()
    {
        var response = await _client.PutAsJsonAsync("/api/v1/customers/999999", new
        {
            name = "Ghost Corp",
        });

        Assert.Equal(HttpStatusCode.NotFound, response.StatusCode);
    }


    [Fact]
    public async Task GetCustomers_ReturnsCreatedCustomers()
    {
        var createResponse = await _client.PostAsJsonAsync("/api/v1/customers", new
        {
            name = "Stark Industries",
        });
        var created = await createResponse.Content.ReadFromJsonAsync<CreatedCustomer>();

        var response = await _client.GetFromJsonAsync<CustomerList>("/api/v1/customers?search=Stark%20Industries");

        var customer = Assert.Single(response.Data, c => c.Id == created.Id);
        Assert.Equal("Stark Industries", customer.Name);
        Assert.Null(customer.Identity);
    }

    [Fact]
    public async Task GetCustomers_RepresentsCustomersIdenticallyToGetCustomer()
    {
        var createResponse = await _client.PostAsJsonAsync("/api/v1/customers", new
        {
            name = "Globex",
            identity = new
            {
                country = "no",
                type = "business",
                id = "912345678",
                name = "Globex AS",
                source = "manual",
            },
        });
        var created = await createResponse.Content.ReadFromJsonAsync<CreatedCustomer>();

        var single = await _client.GetFromJsonAsync<Customer>($"/api/v1/customers/{created.Id}");
        var list = await _client.GetFromJsonAsync<CustomerList>("/api/v1/customers?search=Globex");

        var listed = Assert.Single(list.Data, c => c.Id == created.Id);
        Assert.Equal(single.Id, listed.Id);
        Assert.Equal(single.Name, listed.Name);
        Assert.Equal(single.Identity, listed.Identity);
    }

    [Fact]
    public async Task GetCustomers_WithoutParameters_AppliesDefaultPagination()
    {
        var created = await _client.PostAsJsonAsync("/api/v1/customers", new { name = "Default Paging Co" });
        Assert.Equal(HttpStatusCode.Created, created.StatusCode);

        var response = await _client.GetFromJsonAsync<CustomerList>("/api/v1/customers");

        Assert.Equal(1, response.Pagination.Page);
        Assert.Equal(25, response.Pagination.PageSize);
        Assert.True(response.Pagination.TotalCount > 0);
        Assert.False(response.Pagination.HasPreviousPage);
        Assert.True(response.Data.Count <= 25);
    }

    [Fact]
    public async Task GetCustomers_Pagination_ReturnsCorrectSlicesAndMetadata()
    {
        for (var i = 1; i <= 5; i++)
        {
            await _client.PostAsJsonAsync("/api/v1/customers", new { name = $"Paged Corp {i}" });
        }

        var firstPage = await _client.GetFromJsonAsync<CustomerList>(
            "/api/v1/customers?search=Paged%20Corp&pageSize=2&sortBy=name");
        var secondPage = await _client.GetFromJsonAsync<CustomerList>(
            "/api/v1/customers?search=Paged%20Corp&pageSize=2&page=2&sortBy=name");
        var lastPage = await _client.GetFromJsonAsync<CustomerList>(
            "/api/v1/customers?search=Paged%20Corp&pageSize=2&page=3&sortBy=name");

        Assert.Equal(5, firstPage.Pagination.TotalCount);
        Assert.Equal(3, firstPage.Pagination.TotalPages);

        Assert.Equal(new[] { "Paged Corp 1", "Paged Corp 2" }, firstPage.Data.Select(c => c.Name));
        Assert.True(firstPage.Pagination.HasNextPage);
        Assert.False(firstPage.Pagination.HasPreviousPage);

        Assert.Equal(new[] { "Paged Corp 3", "Paged Corp 4" }, secondPage.Data.Select(c => c.Name));
        Assert.True(secondPage.Pagination.HasNextPage);
        Assert.True(secondPage.Pagination.HasPreviousPage);

        Assert.Equal(new[] { "Paged Corp 5" }, lastPage.Data.Select(c => c.Name));
        Assert.False(lastPage.Pagination.HasNextPage);
        Assert.True(lastPage.Pagination.HasPreviousPage);
    }

    [Fact]
    public async Task GetCustomers_SortByNameDescending_ReturnsCustomersInDescendingOrder()
    {
        await _client.PostAsJsonAsync("/api/v1/customers", new { name = "Sorted Alpha" });
        await _client.PostAsJsonAsync("/api/v1/customers", new { name = "Sorted Beta" });
        await _client.PostAsJsonAsync("/api/v1/customers", new { name = "Sorted Gamma" });

        var response = await _client.GetFromJsonAsync<CustomerList>(
            "/api/v1/customers?search=Sorted&sortBy=name&sortDirection=desc");

        Assert.Equal(
            new[] { "Sorted Gamma", "Sorted Beta", "Sorted Alpha" },
            response.Data.Select(c => c.Name));
    }

    [Fact]
    public async Task GetCustomers_Search_MatchesLegalNameAndLegalIdCaseInsensitively()
    {
        await _client.PostAsJsonAsync("/api/v1/customers", new
        {
            name = "Searchable",
            identity = new
            {
                country = "no",
                type = "business",
                id = "998877665",
                name = "Umbrella Norge AS",
                source = "manual",
            },
        });

        var byLegalName = await _client.GetFromJsonAsync<CustomerList>("/api/v1/customers?search=umbrella%20norge");
        var byLegalId = await _client.GetFromJsonAsync<CustomerList>("/api/v1/customers?search=998877665");
        var noMatch = await _client.GetFromJsonAsync<CustomerList>("/api/v1/customers?search=no-such-customer");

        Assert.Empty(byLegalName.Data);
        Assert.Empty(byLegalId.Data);
        Assert.Empty(noMatch.Data);
        Assert.Equal(0, noMatch.Pagination.TotalCount);
    }

    [Theory]
    [InlineData("page=0")]
    [InlineData("page=-1")]
    [InlineData("pageSize=0")]
    [InlineData("pageSize=101")]
    [InlineData("sortBy=bogus")]
    [InlineData("sortDirection=bogus")]
    public async Task GetCustomers_WithInvalidQueryParameters_ReturnsBadRequest(string queryString)
    {
        var response = await _client.GetAsync($"/api/v1/customers?{queryString}");

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
    }

    [Theory]
    [InlineData("/api/v2/customers")]
    [InlineData("/api/v9/customers")]
    public async Task GetCustomers_WithUnknownApiVersion_DoesNotResolve(string url)
    {
        var response = await _client.GetAsync(url);

        Assert.Equal(HttpStatusCode.NotFound, response.StatusCode);
    }

    [Fact]
    public async Task OpenApiDocument_ForV1_ContainsVersionedCustomerPaths()
    {
        var response = await _client.GetAsync("/openapi/v1.json");

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);

        var document = await response.Content.ReadAsStringAsync();
        Assert.Contains("/api/v1/customers", document);
    }

    private readonly record struct CreatedCustomer(int Id, long CustomerNumber);

    private readonly record struct ValidationProblem(Dictionary<string, string[]> Errors);

    private readonly record struct Customer(int Id, long CustomerNumber, string Name, LegalIdentity? Identity);

    private readonly record struct LegalIdentity(string Country, string Type, string Id, string Name, string Source);

    private readonly record struct CustomerList(List<Customer> Data, Pagination Pagination);

    private readonly record struct Pagination(
        int Page,
        int PageSize,
        int TotalCount,
        int TotalPages,
        bool HasNextPage,
        bool HasPreviousPage);
}