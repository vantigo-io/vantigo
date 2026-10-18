import { describe, expect, it } from "vitest";
import { hostCatalog } from "../i18n";

/**
 * The host's catalog speaks English and Norwegian key for key. The `nb` maps
 * are typed after `en`, but a test reads what ships, so a key added in one
 * language alone — or left empty — fails here even where a cast hides it.
 */
describe("the host catalog's languages", () => {
  it("has the same keys in English and Norwegian, none of them empty", () => {
    const en = hostCatalog.en as Record<string, string>;
    const nb = hostCatalog.nb as Record<string, string>;
    expect(Object.keys(nb).sort()).toEqual(Object.keys(en).sort());
    for (const [key, value] of [...Object.entries(en), ...Object.entries(nb)]) {
      expect(value.trim(), key).not.toBe("");
    }
  });

  it("puts the Norwegian in its own words for the delivery's account sentences", () => {
    const en = hostCatalog.en as Record<string, string>;
    const nb = hostCatalog.nb as Record<string, string>;
    const keys = Object.keys(en).filter(
      (key) =>
        /^dashboard\.staff(Account|Invitation|EmployeeRole|HrOnlySelfApproved)/.test(key) ||
        /^admin\.(provisioningRole|revokedReason|refusal|adopt|stopManag|managedByStaff|disabledBy|userPending|employeeNumber)/.test(
          key,
        ) ||
        /^auth\.(preparedAccount|activateAccount)$/.test(key),
    );
    expect(keys.length).toBeGreaterThan(60);
    for (const key of keys) expect(nb[key], key).not.toBe(en[key]);
  });
});
