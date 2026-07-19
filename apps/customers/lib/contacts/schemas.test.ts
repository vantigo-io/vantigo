import { describe, expect, it } from "vitest";
import {
  contactCreateSchema,
  contactListQuerySchema,
  customerContactCreateSchema,
  customerContactUpdateSchema,
} from "./schemas";

describe("contactCreateSchema", () => {
  it("accepts a minimal contact", () => {
    const result = contactCreateSchema.safeParse({ name: "Kari Nordmann" });
    expect(result.success).toBe(true);
  });

  it("requires a non-empty name", () => {
    expect(contactCreateSchema.safeParse({ name: "  " }).success).toBe(false);
    expect(contactCreateSchema.safeParse({}).success).toBe(false);
  });

  it("turns empty strings into null for optional fields", () => {
    const result = contactCreateSchema.parse({ name: "Kari", email: "", phone: "" });
    expect(result.email).toBeNull();
    expect(result.phone).toBeNull();
  });

  it("rejects invalid emails", () => {
    expect(contactCreateSchema.safeParse({ name: "Kari", email: "not-an-email" }).success).toBe(
      false,
    );
  });
});

describe("customerContactCreateSchema", () => {
  it("accepts linking an existing contact", () => {
    const result = customerContactCreateSchema.safeParse({
      contactId: 7,
      role: "CEO",
      isPrimary: true,
    });
    expect(result.success).toBe(true);
  });

  it("accepts creating and linking a new contact", () => {
    const result = customerContactCreateSchema.safeParse({
      contact: { name: "Kari" },
      role: "",
    });
    expect(result.success).toBe(true);
    if (result.success && "contact" in result.data) {
      expect(result.data.role).toBeNull();
      expect(result.data.isPrimary).toBe(false);
    }
  });

  it("rejects a payload with neither contactId nor contact", () => {
    expect(customerContactCreateSchema.safeParse({ role: "CEO" }).success).toBe(false);
  });
});

describe("customerContactUpdateSchema", () => {
  it("accepts partial updates", () => {
    expect(customerContactUpdateSchema.safeParse({ isPrimary: true }).success).toBe(true);
    expect(customerContactUpdateSchema.safeParse({ role: null }).success).toBe(true);
    expect(customerContactUpdateSchema.safeParse({}).success).toBe(true);
  });
});

describe("contactListQuerySchema", () => {
  it("applies defaults", () => {
    const result = contactListQuerySchema.parse({});
    expect(result).toMatchObject({ page: 1, pageSize: 25, sortBy: "name", sortDir: "asc" });
  });

  it("coerces string query params", () => {
    const result = contactListQuerySchema.parse({ page: "3", pageSize: "50" });
    expect(result.page).toBe(3);
    expect(result.pageSize).toBe(50);
  });

  it("rejects unknown sort fields", () => {
    expect(contactListQuerySchema.safeParse({ sortBy: "phone" }).success).toBe(false);
  });
});
