import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import {
  attachCustomerContact,
  createContact,
  deleteContact,
  detachCustomerContact,
  updateCustomerContact,
} from "./contacts";
import { ApiValidationError, NotFoundError } from "./customers";

const jsonResponse = (status: number, body: unknown) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

describe("contacts api client", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("POSTs new contacts to /api/v1/contacts", async () => {
    const contact = { id: 1001, firstName: "Anders", lastName: "Refsdal" };
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(201, contact));
    stubFetch(fetchMock);

    const result = await createContact({ firstName: "Anders", lastName: "Refsdal" });

    expect(result).toEqual(contact);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/contacts", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ firstName: "Anders", lastName: "Refsdal" }),
    });
  });

  it("maps 400 validation problems to ApiValidationError", async () => {
    stubFetch(
      vi.fn().mockResolvedValue(
        jsonResponse(400, {
          title: "Invalid contact",
          errors: { firstName: ["A name cannot be null or empty"] },
        }),
      ),
    );

    const error = await createContact({ firstName: "", lastName: "Refsdal" }).catch((e: unknown) => e);

    expect(error).toBeInstanceOf(ApiValidationError);
    expect((error as ApiValidationError).fieldErrors).toEqual({
      firstName: "A name cannot be null or empty",
    });
  });

  it("surfaces 409 conflicts with the problem detail", async () => {
    stubFetch(
      vi.fn().mockResolvedValue(
        jsonResponse(409, {
          title: "Contact already associated",
          detail: "Contact 1001 is already associated with customer 2002.",
        }),
      ),
    );

    await expect(attachCustomerContact(2002, { contactId: 1001, role: "CEO" })).rejects.toThrow(
      "Contact 1001 is already associated with customer 2002.",
    );
  });

  it("maps 404 to NotFoundError", async () => {
    stubFetch(vi.fn().mockResolvedValue(new Response(null, { status: 404 })));

    const error = await updateCustomerContact(1, 2, { role: "CEO" }).catch((e: unknown) => e);

    expect(error).toBeInstanceOf(NotFoundError);
  });

  it("uses the expected methods and urls for deletion and detachment", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }));
    stubFetch(fetchMock);

    await deleteContact(1001);
    await detachCustomerContact(2002, 1001);

    expect(fetchMock).toHaveBeenNthCalledWith(1, "/api/v1/contacts/1001", { method: "DELETE" });
    expect(fetchMock).toHaveBeenNthCalledWith(2, "/api/v1/customers/2002/contacts/1001", {
      method: "DELETE",
    });
  });
});
