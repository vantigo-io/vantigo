import { describe, expect, it } from "vitest";
import { customerCreateSchema, customerListQuerySchema, customerUpdateSchema } from "./schemas";

const validCustomer = {
  legalName: "Testbedrift AS",
  legalType: "business" as const,
  legalCountry: "NO",
  legalId: "923609016",
  email: "post@testbedrift.no",
  phone: "+4722334455",
};

describe("customerCreateSchema", () => {
  it("accepts a valid Norwegian business customer", () => {
    const result = customerCreateSchema.safeParse(validCustomer);
    expect(result.success).toBe(true);
  });

  it("accepts a valid Norwegian private customer", () => {
    const result = customerCreateSchema.safeParse({
      ...validCustomer,
      legalType: "private",
      legalId: "01010150074",
    });
    expect(result.success).toBe(true);
  });

  it("rejects an orgnr for a private customer", () => {
    const result = customerCreateSchema.safeParse({
      ...validCustomer,
      legalType: "private",
    });
    expect(result.success).toBe(false);
    if (!result.success) {
      expect(result.error.issues[0].path).toEqual(["legalId"]);
    }
  });

  it("uppercases the country code", () => {
    const result = customerCreateSchema.safeParse({ ...validCustomer, legalCountry: "no" });
    expect(result.success).toBe(true);
    if (result.success) {
      expect(result.data.legalCountry).toBe("NO");
    }
  });

  it("skips strict legal-id rules for non-Norwegian customers", () => {
    const result = customerCreateSchema.safeParse({
      ...validCustomer,
      legalCountry: "SE",
      legalId: "5560360793",
    });
    expect(result.success).toBe(true);
  });

  it("rejects invalid email", () => {
    expect(customerCreateSchema.safeParse({ ...validCustomer, email: "nope" }).success).toBe(false);
  });

  it("treats email and phone as optional", () => {
    const { email, phone, ...withoutContact } = validCustomer;
    const result = customerCreateSchema.safeParse(withoutContact);
    expect(result.success).toBe(true);
  });

  it("normalizes empty contact strings to null", () => {
    const result = customerCreateSchema.safeParse({ ...validCustomer, email: "", phone: "  " });
    expect(result.success).toBe(true);
    if (result.success) {
      expect(result.data.email).toBeNull();
      expect(result.data.phone).toBeNull();
    }
  });
});

describe("customerUpdateSchema", () => {
  it("accepts a partial update without legal fields", () => {
    expect(customerUpdateSchema.safeParse({ email: "new@testbedrift.no" }).success).toBe(true);
  });

  it("requires legal fields to change together", () => {
    expect(customerUpdateSchema.safeParse({ legalId: "923609016" }).success).toBe(false);
    expect(
      customerUpdateSchema.safeParse({
        legalId: "923609016",
        legalType: "business",
        legalCountry: "NO",
      }).success,
    ).toBe(true);
  });

  it("accepts status changes", () => {
    expect(customerUpdateSchema.safeParse({ status: "archived" }).success).toBe(true);
    expect(customerUpdateSchema.safeParse({ status: "deleted" }).success).toBe(false);
  });
});

describe("customerListQuerySchema", () => {
  it("applies defaults", () => {
    const result = customerListQuerySchema.parse({});
    expect(result).toMatchObject({ page: 1, pageSize: 25, sortBy: "id", sortDir: "asc" });
  });

  it("coerces string query params", () => {
    const result = customerListQuerySchema.parse({ page: "2", pageSize: "50" });
    expect(result.page).toBe(2);
    expect(result.pageSize).toBe(50);
  });

  it("caps pageSize at 100", () => {
    expect(customerListQuerySchema.safeParse({ pageSize: "500" }).success).toBe(false);
  });

  it("rejects unknown sort fields", () => {
    expect(customerListQuerySchema.safeParse({ sortBy: "legalId" }).success).toBe(false);
  });
});
