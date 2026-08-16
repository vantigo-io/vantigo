using System.Net;
using System.Net.Http.Json;

using Microsoft.AspNetCore.Identity;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.DependencyInjection;

using Vantigo.Customers.Database.Customers;
using Vantigo.Identity.Database.Accounts;

namespace Vantigo.Customers.Module.Tests.Integration;

[Collection(CustomersApiCollection.Name)]
public sealed class CustomersPermissionIntegrationTests(CustomersApiFactory factory)
{
    [Fact]
    public async Task OwnerHasCustomerAccessAndNoPermissionUserIsDenied()
    {
        using var owner = factory.CreateAuthenticatedClient();
        var ownerResponse = await owner.GetAsync("/api/v1/customers");
        Assert.Equal(HttpStatusCode.OK, ownerResponse.StatusCode);

        using var user = await factory.CreateUserClientAsync("User");
        var denied = await user.GetAsync("/api/v1/customers");
        Assert.Equal(HttpStatusCode.Forbidden, denied.StatusCode);
    }

    [Fact]
    public async Task ViewerGetsSafeCustomerProjectionButSensitiveDataIsDenied()
    {
        using var owner = factory.CreateAuthenticatedClient();
        var created = await owner.PostAsJsonAsync("/api/v1/customers", new { name = "Safe Customer" });
        var id = (await created.Content.ReadFromJsonAsync<CreatedCustomer>())!.Id;
        await owner.PutAsJsonAsync($"/api/v1/customers/{id}/legal-identity", new
        {
            country = "no",
            type = "business",
            id = "923609016",
            name = "Safe AS",
            source = "manual",
        });

        using var viewer = await factory.CreateUserClientAsync("Viewer", ["customers:view"]);
        var response = await viewer.GetFromJsonAsync<SafeCustomer>($"/api/v1/customers/{id}");
        Assert.Equal("Safe Customer", response!.Name);
        var safeJson = await (await viewer.GetAsync($"/api/v1/customers/{id}")).Content.ReadAsStringAsync();
        Assert.DoesNotContain("identity", safeJson, StringComparison.OrdinalIgnoreCase);

        Assert.Equal(HttpStatusCode.Forbidden,
            (await viewer.GetAsync($"/api/v1/customers/{id}/legal-identity")).StatusCode);
        Assert.Equal(HttpStatusCode.Forbidden,
            (await viewer.GetAsync($"/api/v1/customers/{id}/timeline")).StatusCode);
    }

    [Fact]
    public async Task CustomerPermissionsAuthorizeCreateUpdateDeleteAndSensitiveFamilies()
    {
        using var user = await factory.CreateUserClientAsync("Admin", new[]
        {
            "customers:view", "customers:create", "customers:update", "customers:delete",
            "customers:legal-identity-view", "customers:legal-identity-manage",
            "customers:contacts-view", "customers:contacts-manage",
            "customers:associations-view", "customers:associations-manage",
            "customers:timeline-view", "customers:timeline-manage", "customers:lookup-view",
        });

        var create = await user.PostAsJsonAsync("/api/v1/customers", new { name = "Permission Customer" });
        Assert.Equal(HttpStatusCode.Created, create.StatusCode);
        var customerId = (await create.Content.ReadFromJsonAsync<CreatedCustomer>())!.Id;

        var update = await user.PutAsJsonAsync($"/api/v1/customers/{customerId}", new { name = "Updated Customer" });
        Assert.Equal(HttpStatusCode.OK, update.StatusCode);

        var identity = await user.PutAsJsonAsync($"/api/v1/customers/{customerId}/legal-identity", new
        {
            country = "no",
            type = "business",
            id = "923609016",
            name = "Permission AS",
            source = "manual",
        });
        Assert.Equal(HttpStatusCode.OK, identity.StatusCode);
        Assert.Equal(HttpStatusCode.OK,
            (await user.GetAsync($"/api/v1/customers/{customerId}/legal-identity")).StatusCode);

        var contact = await user.PostAsJsonAsync("/api/v1/customers/contacts", new
        {
            firstName = "Permission",
            lastName = "Contact",
            email = "permission@example.test",
        });
        Assert.Equal(HttpStatusCode.Created, contact.StatusCode);
        var contactId = (await contact.Content.ReadFromJsonAsync<ContactResponse>())!.Id;
        Assert.Equal(HttpStatusCode.OK,
            (await user.PostAsJsonAsync($"/api/v1/customers/{customerId}/contacts", new
            {
                contactId,
                role = "CEO",
            })).StatusCode);
        Assert.Equal(HttpStatusCode.OK,
            (await user.GetAsync($"/api/v1/customers/{customerId}/contacts")).StatusCode);
        Assert.Equal(HttpStatusCode.OK,
            (await user.GetAsync($"/api/v1/customers/contacts/{contactId}/customers")).StatusCode);

        var timeline = await user.PostAsJsonAsync($"/api/v1/customers/{customerId}/timeline", new
        {
            eventType = "note",
            occurredOn = "2026-08-12",
            note = "permission note",
        });
        Assert.Equal(HttpStatusCode.Created, timeline.StatusCode);
        Assert.Equal(HttpStatusCode.OK,
            (await user.GetAsync($"/api/v1/customers/{customerId}/timeline")).StatusCode);
        Assert.Equal(HttpStatusCode.OK,
            (await user.GetAsync("/api/v1/customers/lookup/brreg?search=equinor")).StatusCode);

        Assert.Equal(HttpStatusCode.NoContent,
            (await user.DeleteAsync($"/api/v1/customers/{customerId}")).StatusCode);
    }

    [Fact]
    public async Task CustomerUpdateRequiresViewPermissionForItsSafeResponse()
    {
        using var owner = factory.CreateAuthenticatedClient();
        var customerResponse = await owner.PostAsJsonAsync("/api/v1/customers", new { name = "Update Permission Customer" });
        var customerId = (await customerResponse.Content.ReadFromJsonAsync<CreatedCustomer>())!.Id;

        using var updateOnly = await factory.CreateUserClientAsync("UpdateOnly", ["customers:update"]);
        var denied = await updateOnly.PutAsJsonAsync($"/api/v1/customers/{customerId}", new { name = "Should Be Denied" });
        Assert.Equal(HttpStatusCode.Forbidden, denied.StatusCode);

        using var updateAndView = await factory.CreateUserClientAsync("UpdateAndView", ["customers:update", "customers:view"]);
        var allowed = await updateAndView.PutAsJsonAsync($"/api/v1/customers/{customerId}", new { name = "Updated Safely" });
        Assert.Equal(HttpStatusCode.OK, allowed.StatusCode);
        var safe = await allowed.Content.ReadFromJsonAsync<SafeCustomer>();
        Assert.Equal("Updated Safely", safe!.Name);

        var ownerAllowed = await owner.PutAsJsonAsync($"/api/v1/customers/{customerId}", new { name = "Owner Updated" });
        Assert.Equal(HttpStatusCode.OK, ownerAllowed.StatusCode);
    }

    [Fact]
    public async Task AssociationManagerWithoutContactsViewCannotReceiveContactPii()
    {
        using var owner = factory.CreateAuthenticatedClient();
        var customerResponse = await owner.PostAsJsonAsync("/api/v1/customers", new { name = "Association Customer" });
        var customerId = (await customerResponse.Content.ReadFromJsonAsync<CreatedCustomer>())!.Id;
        var contactResponse = await owner.PostAsJsonAsync("/api/v1/customers/contacts", new
        {
            firstName = "Private",
            lastName = "Contact",
            email = "private.contact@example.test",
            phone = "+47 900 00 001",
        });
        var contactId = (await contactResponse.Content.ReadFromJsonAsync<ContactResponse>())!.Id;

        var attach = await owner.PostAsJsonAsync($"/api/v1/customers/{customerId}/contacts", new
        {
            contactId,
            role = "CEO",
            email = "association@example.test",
        });
        Assert.Equal(HttpStatusCode.OK, attach.StatusCode);

        using var associationManager = await factory.CreateUserClientAsync(
            "AssociationManager", ["customers:associations-manage"]);

        var attachDenied = await associationManager.PostAsJsonAsync(
            $"/api/v1/customers/{customerId}/contacts", new { contactId, role = "CTO" });
        Assert.Equal(HttpStatusCode.Forbidden, attachDenied.StatusCode);

        var updateDenied = await associationManager.PutAsJsonAsync(
            $"/api/v1/customers/{customerId}/contacts/{contactId}", new { role = "Chairman" });
        Assert.Equal(HttpStatusCode.Forbidden, updateDenied.StatusCode);

        var associationViewDenied = await associationManager.GetAsync(
            $"/api/v1/customers/{customerId}/contacts");
        Assert.Equal(HttpStatusCode.Forbidden, associationViewDenied.StatusCode);
    }

    [Fact]
    public async Task AssociationManagerWithContactsViewCanAttachAndReceiveContactProjection()
    {
        using var owner = factory.CreateAuthenticatedClient();
        var customerResponse = await owner.PostAsJsonAsync("/api/v1/customers", new { name = "Association Viewer Customer" });
        var customerId = (await customerResponse.Content.ReadFromJsonAsync<CreatedCustomer>())!.Id;
        var contactResponse = await owner.PostAsJsonAsync("/api/v1/customers/contacts", new
        {
            firstName = "Visible",
            lastName = "Contact",
            email = "visible.contact@example.test",
        });
        var contactId = (await contactResponse.Content.ReadFromJsonAsync<ContactResponse>())!.Id;

        using var associationManager = await factory.CreateUserClientAsync(
            "AssociationViewer", ["customers:associations-manage", "customers:contacts-view"]);

        var attach = await associationManager.PostAsJsonAsync(
            $"/api/v1/customers/{customerId}/contacts", new { contactId, role = "CEO" });
        Assert.Equal(HttpStatusCode.OK, attach.StatusCode);
        var projection = await attach.Content.ReadFromJsonAsync<CustomerContactResponse>();
        Assert.Equal("Visible", projection!.Contact.FirstName);
        Assert.Equal("Contact", projection.Contact.LastName);

        var update = await associationManager.PutAsJsonAsync(
            $"/api/v1/customers/{customerId}/contacts/{contactId}", new { role = "Chairman" });
        Assert.Equal(HttpStatusCode.OK, update.StatusCode);
        var updatedProjection = await update.Content.ReadFromJsonAsync<CustomerContactResponse>();
        Assert.Equal("Chairman", updatedProjection!.Role);
    }

    [Fact]
    public async Task LegalIdentityManageOnlyCannotReceiveLegalIdentityMutationResponse()
    {
        using var owner = factory.CreateAuthenticatedClient();
        var customerResponse = await owner.PostAsJsonAsync("/api/v1/customers", new { name = "Legal Mutation Customer" });
        var customerId = (await customerResponse.Content.ReadFromJsonAsync<CreatedCustomer>())!.Id;

        using var manager = await factory.CreateUserClientAsync("LegalManager", ["customers:legal-identity-manage"]);
        var response = await manager.PutAsJsonAsync($"/api/v1/customers/{customerId}/legal-identity", new
        {
            country = "no",
            type = "business",
            id = "923609016",
            name = "Legal Mutation AS",
            source = "manual",
        });

        Assert.Equal(HttpStatusCode.Forbidden, response.StatusCode);
    }

    [Fact]
    public async Task TimelineManageOnlyCannotReceiveTimelineMutationResponse()
    {
        using var owner = factory.CreateAuthenticatedClient();
        var customerResponse = await owner.PostAsJsonAsync("/api/v1/customers", new { name = "Timeline Mutation Customer" });
        var customerId = (await customerResponse.Content.ReadFromJsonAsync<CreatedCustomer>())!.Id;

        using var manager = await factory.CreateUserClientAsync("TimelineManager", ["customers:timeline-manage"]);
        var create = await manager.PostAsJsonAsync($"/api/v1/customers/{customerId}/timeline", new
        {
            eventType = "note",
            occurredOn = "2026-08-12",
            note = "should not return sensitive timeline data",
        });

        Assert.Equal(HttpStatusCode.Forbidden, create.StatusCode);
    }

    [Fact]
    public async Task ContactManageOnlyCannotDeleteContactThatCascadesAssociations()
    {
        using var owner = factory.CreateAuthenticatedClient();
        var customerResponse = await owner.PostAsJsonAsync("/api/v1/customers", new { name = "Cascade Customer" });
        var customerId = (await customerResponse.Content.ReadFromJsonAsync<CreatedCustomer>())!.Id;
        var contactResponse = await owner.PostAsJsonAsync("/api/v1/customers/contacts", new
        {
            firstName = "Cascade",
            lastName = "Contact",
            email = "cascade@example.test",
        });
        var contactId = (await contactResponse.Content.ReadFromJsonAsync<ContactResponse>())!.Id;
        Assert.Equal(HttpStatusCode.OK, (await owner.PostAsJsonAsync($"/api/v1/customers/{customerId}/contacts", new
        {
            contactId,
            role = "CEO",
        })).StatusCode);

        using var manager = await factory.CreateUserClientAsync("ContactManager", ["customers:contacts-manage"]);
        var denied = await manager.DeleteAsync($"/api/v1/customers/contacts/{contactId}");
        Assert.Equal(HttpStatusCode.Forbidden, denied.StatusCode);

        using var fullManager = await factory.CreateUserClientAsync(
            "ContactAssociationManager", ["customers:contacts-manage", "customers:associations-manage"]);
        var deleted = await fullManager.DeleteAsync($"/api/v1/customers/contacts/{contactId}");
        Assert.Equal(HttpStatusCode.NoContent, deleted.StatusCode);
    }

    [Fact]
    public async Task DisabledUserIsDeniedByPermissionHandlerImmediately()
    {
        using var user = await factory.CreateUserClientAsync("Disabled", ["customers:view"]);
        await using (var scope = factory.Services.CreateAsyncScope())
        {
            var users = scope.ServiceProvider.GetRequiredService<UserManager<ApplicationUser>>();
            var account = await users.Users.SingleAsync(item => item.DisplayName == "Disabled");
            Assert.NotNull(account);
            account!.IsDisabled = true;
            Assert.True((await users.UpdateAsync(account)).Succeeded);
        }

        Assert.Equal(HttpStatusCode.Unauthorized,
            (await user.GetAsync("/api/v1/customers")).StatusCode);
    }

    private readonly record struct CreatedCustomer(int Id, long CustomerNumber);
    private readonly record struct ContactResponse(int Id, string? FirstName = null, string? LastName = null);
    private readonly record struct CustomerContactResponse(ContactProjection Contact, string Role, string? Phone, string? Email);
    private readonly record struct ContactProjection(int Id, string FirstName, string LastName, string? MiddleName, string? Prefix, string? Suffix, string? Phone, string? Email);
    private readonly record struct SafeCustomer(int Id, string Name, SafeTimelineSummary TimelineSummary);
    private readonly record struct SafeTimelineSummary(int EntryCount, DateOnly? LatestOccurredOn);
}