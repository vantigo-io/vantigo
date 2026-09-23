import { afterEach, describe, expect, it, vi } from "vitest";
import { stubFetch } from "../test/fetch";
import {
  attachCustomerContact,
  createContact,
  customerContactsQueryOptions,
  deleteContact,
  detachCustomerContact,
  normalizeContactRoles,
  normalizeCustomerContact,
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

  it("POSTs new contacts to /api/v1/customers/contacts", async () => {
    const contact = { id: 1001, firstName: "Anders", lastName: "Refsdal" };
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse(201, contact));
    stubFetch(fetchMock);

    const result = await createContact({ firstName: "Anders", lastName: "Refsdal" });

    expect(result).toEqual(contact);
    expect(fetchMock).toHaveBeenCalledWith("/api/v1/customers/contacts", {
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

    await expect(attachCustomerContact(2002, { contactId: 1001, title: "CEO" })).rejects.toThrow(
      "Contact 1001 is already associated with customer 2002.",
    );
  });

  it("maps 404 to NotFoundError", async () => {
    stubFetch(vi.fn().mockResolvedValue(new Response(null, { status: 404 })));

    const error = await updateCustomerContact(1, 2, { title: "CEO" }).catch((e: unknown) => e);

    expect(error).toBeInstanceOf(NotFoundError);
  });

  it("uses the expected methods and urls for deletion and detachment", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }));
    stubFetch(fetchMock);

    await deleteContact(1001);
    await detachCustomerContact(2002, 1001);

    expect(fetchMock).toHaveBeenNthCalledWith(1, "/api/v1/customers/contacts/1001", { method: "DELETE" });
    expect(fetchMock).toHaveBeenNthCalledWith(2, "/api/v1/customers/2002/contacts/1001", {
      method: "DELETE",
    });
  });

  it("reads the title and the roles off a list response", async () => {
    // The list path's own normalisation: `title` comes straight off the wire
    // (follow-ups design D5 left it the only name the free text has) and an
    // absent `roles` becomes an empty array, which is what keeps the contacts
    // card's title line and its badges working on every response.
    stubFetch(() =>
      Promise.resolve(
        new Response(
          JSON.stringify({
            data: [
              {
                contact: {
                  id: 1001,
                  firstName: "A",
                  lastName: "B",
                  middleName: null,
                  prefix: null,
                  suffix: null,
                  phone: null,
                  email: null,
                },
                title: "CTO",
                phone: null,
                email: null,
              },
            ],
          }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        ),
      ),
    );

    const answered = (await customerContactsQueryOptions(2002).queryFn?.({ signal: undefined } as never)) as {
      data: { title: string | null; roles: unknown[] }[];
    };
    expect(answered.data[0].title).toBe("CTO");
    expect(answered.data[0].roles).toEqual([]);
  });

  it("reads the title straight off the wire and turns an absent one into null", () => {
    // The `role` alias is gone (follow-ups design D5), so there is no fallback
    // left to get wrong: a wire body with no title means the association has
    // none, and an extra `role` key a stale client still sends is ignored.
    expect(
      normalizeCustomerContact({
        contact: { id: 1, firstName: "Kari", lastName: "Nordmann" },
        title: null,
        roles: [],
        phone: null,
        email: null,
      } as never),
    ).toMatchObject({ title: null, roles: [] });
    expect(
      normalizeCustomerContact({
        contact: { id: 1, firstName: "Kari", lastName: "Nordmann" },
        title: "CEO",
        role: "IGNORED",
        phone: null,
        email: null,
      } as never),
    ).toMatchObject({ title: "CEO", roles: [] });
  });

  it("normalises roles: absent is empty, primary defaults to false, and the fixed order is restored", () => {
    // The server sorts, but a cached response from an older version, or any
    // future widening, must not decide what the UI's order is.
    expect(normalizeContactRoles(undefined)).toEqual([]);
    expect(normalizeContactRoles(null)).toEqual([]);
    expect(
      normalizeContactRoles([
        { role: "decision_maker", primary: true },
        { role: "billing" },
        { role: "executive_sponsor", primary: true },
        { role: "project", primary: false },
      ]),
    ).toEqual([
      { role: "billing", primary: false },
      { role: "project", primary: false },
      { role: "decision_maker", primary: true },
      { role: "executive_sponsor", primary: true },
    ]);
  });

  it("sends title and the complete roles on an attach, and reads title back as null when absent", async () => {
    const fetchMock = stubFetch(() =>
      Promise.resolve(
        new Response(
          JSON.stringify({
            contact: {
              id: 1001,
              firstName: "A",
              lastName: "B",
              middleName: null,
              prefix: null,
              suffix: null,
              phone: null,
              email: null,
            },
            roles: [{ role: "billing", primary: true }],
          }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        ),
      ),
    );

    const answered = await attachCustomerContact(2002, {
      contactId: 1001,
      roles: [{ role: "billing", primary: true }],
    });

    // `title` is absent — the server omits what is unset — so the title is
    // genuinely none rather than "".
    expect(answered.title).toBeNull();
    expect(answered.roles).toEqual([{ role: "billing", primary: true }]);
    // `actualCalls`, not `.mock.calls`: `stubFetch` (src/test/fetch.ts) returns
    // the business mock with `calls` (everything, session bootstrap included)
    // and `actualCalls` (everything but the bootstrap) as plain arrays on it —
    // it is not a vi.fn wrapper with a `.mock` of its own. Filtering by method
    // and URL rather than taking the last call, as the Global Constraints require.
    const attach = fetchMock.actualCalls.find(
      ([url, init]) => String(url) === "/api/v1/customers/2002/contacts" && init?.method === "POST",
    );
    expect(attach).toBeDefined();
    if (!attach) throw new Error("Attach request is required");
    expect(JSON.parse((attach[1] as RequestInit).body as string)).toEqual({
      contactId: 1001,
      roles: [{ role: "billing", primary: true }],
    });
  });
});
