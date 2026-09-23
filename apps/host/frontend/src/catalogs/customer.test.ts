import { describe, expect, it } from "vitest";
import { customerCatalog } from "./customer";

describe("the customer catalog's Customer 360 strings", () => {
  it("has English and Norwegian text for every key", () => {
    const keys = Object.keys(customerCatalog.en).filter((key) => key.startsWith("customer.overview360."));
    expect(keys).toHaveLength(19);
    for (const key of keys) {
      expect(customerCatalog.en[key as keyof typeof customerCatalog.en], `${key} (en)`).toBeTruthy();
      expect(customerCatalog.nb[key as keyof typeof customerCatalog.nb], `${key} (nb)`).toBeTruthy();
    }
  });
});
