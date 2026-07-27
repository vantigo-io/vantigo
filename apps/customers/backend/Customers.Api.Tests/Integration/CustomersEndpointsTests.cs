using System.Net;
using System.Net.Http.Json;

namespace Vantigo.Customers.Api.Tests.Integration;

[Collection(CustomersApiCollection.Name)]
public sealed class CustomersEndpointsTests
{
    private readonly HttpClient _client;

    public CustomersEndpointsTests(CustomersApiFactory factory)
    {
        _client = factory.CreateClient();
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
            },
        });

        Assert.Equal(HttpStatusCode.Created, response.StatusCode);
        var created = await response.Content.ReadFromJsonAsync<CreatedCustomer>();

        var customer = await _client.GetFromJsonAsync<Customer>($"/api/v1/customers/{created.Id}");

        Assert.Equal("Acme", customer.Name);
        Assert.NotNull(customer.Identity);
        Assert.Equal("no", customer.Identity.Value.Country);
        Assert.Equal("business", customer.Identity.Value.Type);
        Assert.Equal("923609016", customer.Identity.Value.Id);
        Assert.Equal("Acme AS", customer.Identity.Value.Name);
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
            },
        });

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);
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
            },
        });

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);

        var updated = await response.Content.ReadFromJsonAsync<Customer>();
        Assert.NotNull(updated.Identity);
        Assert.Equal("no", updated.Identity.Value.Country);
        Assert.Equal("business", updated.Identity.Value.Type);
        Assert.Equal("923609016", updated.Identity.Value.Id);
        Assert.Equal("Replaceable AS", updated.Identity.Value.Name);
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
            },
        });
        var created = await createResponse.Content.ReadFromJsonAsync<CreatedCustomer>();

        var response = await _client.PutAsJsonAsync($"/api/v1/customers/{created.Id}", new
        {
            name = "Removable",
        });

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);

        var updated = await response.Content.ReadFromJsonAsync<Customer>();
        Assert.Null(updated.Identity);

        var fetched = await _client.GetFromJsonAsync<Customer>($"/api/v1/customers/{created.Id}");
        Assert.Null(fetched.Identity);
    }

    [Fact]
    public async Task UpdateCustomer_WithInvalidIdentityFields_ReportsAllErrorsAtOnce()
    {
        var createResponse = await _client.PostAsJsonAsync("/api/v1/customers", new
        {
            name = "Identity Validation Co",
        });
        var created = await createResponse.Content.ReadFromJsonAsync<CreatedCustomer>();

        var response = await _client.PutAsJsonAsync($"/api/v1/customers/{created.Id}", new
        {
            name = "",
            identity = new
            {
                country = "",
                type = "business",
                id = " ",
                name = "Acme AS",
            },
        });

        Assert.Equal(HttpStatusCode.BadRequest, response.StatusCode);

        var problem = await response.Content.ReadFromJsonAsync<ValidationProblem>();
        Assert.Equal(
            new[] { "identity.country", "identity.id", "name" },
            problem.Errors.Keys.Order());
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
            },
        });
        var created = await createResponse.Content.ReadFromJsonAsync<CreatedCustomer>();

        var single = await _client.GetFromJsonAsync<Customer>($"/api/v1/customers/{created.Id}");
        var list = await _client.GetFromJsonAsync<CustomerList>("/api/v1/customers?search=Globex");

        var listed = Assert.Single(list.Data, c => c.Id == created.Id);
        Assert.Equal(single, listed);
    }

    [Fact]
    public async Task GetCustomers_WithoutParameters_AppliesDefaultPagination()
    {
        await _client.PostAsJsonAsync("/api/v1/customers", new { name = "Default Paging Co" });

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
            },
        });

        var byLegalName = await _client.GetFromJsonAsync<CustomerList>("/api/v1/customers?search=umbrella%20norge");
        var byLegalId = await _client.GetFromJsonAsync<CustomerList>("/api/v1/customers?search=998877665");
        var noMatch = await _client.GetFromJsonAsync<CustomerList>("/api/v1/customers?search=no-such-customer");

        Assert.Single(byLegalName.Data, c => c.Name == "Searchable");
        Assert.Single(byLegalId.Data, c => c.Name == "Searchable");
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

    private readonly record struct CreatedCustomer(int Id);

    private readonly record struct ValidationProblem(Dictionary<string, string[]> Errors);

    private readonly record struct Customer(int Id, string Name, LegalIdentity? Identity);

    private readonly record struct LegalIdentity(string Country, string Type, string Id, string Name);

    private readonly record struct CustomerList(List<Customer> Data, Pagination Pagination);

    private readonly record struct Pagination(
        int Page,
        int PageSize,
        int TotalCount,
        int TotalPages,
        bool HasNextPage,
        bool HasPreviousPage);
}
