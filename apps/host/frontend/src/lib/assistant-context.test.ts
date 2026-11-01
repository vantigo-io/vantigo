import { QueryClient } from "@tanstack/react-query";
import { assistantContext } from "@vantigo/assistant-ui";
import { customerQueryOptions } from "@vantigo/customers-ui/api/customers";
import { meteringPointQueryOptions } from "@vantigo/energy-ui/api/energy";
import { setLanguagePreference } from "@vantigo/frontend-shell";
import { invoiceQueryOptions } from "@vantigo/invoices-ui/api/invoices";
import { projectQueryOptions } from "@vantigo/projects-ui/api/projects";
import { afterEach, beforeAll, describe, expect, it } from "vitest";
import "../i18n";
import { assistantChipFor, registerAssistantContexts, routeInfoFrom, screenNameKey } from "./assistant-context";

beforeAll(() => registerAssistantContexts());
afterEach(() => setLanguagePreference("en"));

const route = (routeId: string, params: Record<string, string>) => ({ routeId, pathname: routeId, params });

describe("the assistant's context for the detail pages", () => {
  it("names a customer from the cache the page has already filled", () => {
    const client = new QueryClient();
    client.setQueryData(customerQueryOptions(42).queryKey, { id: 42, name: "Ola Nordmann AS" } as never);
    expect(assistantContext.resolve(route("/customers/$customerId/", { customerId: "42" }), client)?.label).toBe(
      "Customer Ola Nordmann AS",
    );
    // A tab of the customer is still the customer.
    expect(
      assistantContext.resolve(route("/customers/$customerId/invoices", { customerId: "42" }), client)?.label,
    ).toBe("Customer Ola Nordmann AS");
  });

  it("names a metering point by its GSRN, an invoice by its number and a project by its code and name", () => {
    const client = new QueryClient();
    client.setQueryData(meteringPointQueryOptions(7).queryKey, { id: 7, gsrn: "707057500000000012" } as never);
    client.setQueryData(invoiceQueryOptions(9).queryKey, { id: 9, number: 20260031 } as never);
    client.setQueryData(invoiceQueryOptions(10).queryKey, { id: 10 } as never);
    client.setQueryData(projectQueryOptions(3).queryKey, {
      id: 3,
      code: "ACME1000",
      name: "Roof replacement",
    } as never);

    const label = (routeId: string, params: Record<string, string>) =>
      assistantContext.resolve(route(routeId, params), client)?.label;
    expect(label("/energy/metering-points/$meteringPointId", { meteringPointId: "7" })).toBe(
      "Metering point 707057500000000012",
    );
    expect(label("/invoices/$invoiceId", { invoiceId: "9" })).toBe("Invoice 20260031");
    expect(label("/invoices/$invoiceId", { invoiceId: "10" })).toBe("Draft invoice");
    expect(label("/projects/$projectId", { projectId: "3" })).toBe("Project ACME1000 Roof replacement");
  });

  it("says the record in Norwegian when the person's language is", () => {
    setLanguagePreference("nb");
    const client = new QueryClient();
    client.setQueryData(customerQueryOptions(1).queryKey, { id: 1, name: "Kari" } as never);
    expect(assistantContext.resolve(route("/customers/$customerId", { customerId: "1" }), client)?.label).toBe(
      "Kunde Kari",
    );
  });

  it("says nothing while the record is not loaded, or on a route with no resolver", () => {
    const client = new QueryClient();
    expect(assistantContext.resolve(route("/customers/$customerId", { customerId: "42" }), client)).toBeUndefined();
    expect(assistantContext.resolve(route("/products", {}), client)).toBeUndefined();
    expect(assistantContext.resolve(route("/invoices/$invoiceId", { invoiceId: "abc" }), client)).toBeUndefined();
  });
});

describe("the chip", () => {
  it("is the record's label when there is one, else the screen's name, with the route and its ids", () => {
    const info = routeInfoFrom([
      { routeId: "/customers", pathname: "/customers/42", params: {} },
      { routeId: "/customers/$customerId/invoices", pathname: "/customers/42/invoices", params: { customerId: 42 } },
    ]);
    expect(info).toEqual({
      routeId: "/customers/$customerId/invoices",
      pathname: "/customers/42/invoices",
      params: { customerId: "42" },
    });
    expect(assistantChipFor(info, "customers", "Customer Ola", "Customers")).toEqual({
      app: "customers",
      route: "/customers/$customerId/invoices",
      label: "Customer Ola",
      params: { customerId: "42" },
    });
    expect(assistantChipFor(info, "customers", undefined, "Customers")?.label).toBe("Customers");
    expect(assistantChipFor(info, "customers", undefined, undefined)).toBeUndefined();
    expect(assistantChipFor(undefined, "customers", "x", "y")).toBeUndefined();
  });

  it("falls back to the sidebar entry the path is under, then the app's own name", () => {
    expect(screenNameKey("customers", "/customers/contacts/5")).toBe("navigation.contacts");
    expect(screenNameKey("customers", "/customers/42")).toBe("navigation.customers");
    expect(screenNameKey("home", "/dashboard")).toBe("navigation.dashboard");
    expect(screenNameKey("energy", "/energy/import/1")).toBe("navigation.energy");
    expect(screenNameKey(undefined, "/somewhere")).toBeUndefined();
  });
});
