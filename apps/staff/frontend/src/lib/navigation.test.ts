import { describe, expect, it } from "vitest";
import { staffNavItems, visibleStaffNavItems } from "./navigation";
import { MY_PAGE_ROUTE_PATH, PEOPLE_ROUTE_PATH, STAFF_SETTINGS_ROUTE_PATH } from "./routes";

const offered = (permissions: string[] | undefined) => visibleStaffNavItems(permissions).map((item) => item.key);

describe("the Staff sidebar", () => {
  it("offers My page to everyone in the app, People to the directory, Settings to HR", () => {
    expect(offered(["staff:access"])).toEqual(["myPage"]);
    expect(offered(["staff:access", "staff:view"])).toEqual(["myPage", "people"]);
    expect(offered(["staff:access", "staff:view", "staff:manage"])).toEqual(["myPage", "people", "settings"]);
    expect(offered(["*"])).toEqual(["myPage", "people", "settings"]);
    expect(offered(undefined)).toEqual([]);
  });

  it("guards People's URL by staff:access alone, so HR without the directory still lists", () => {
    const people = staffNavItems.find((item) => item.key === "people");
    expect(people?.requiredPermissions).toEqual(["staff:view"]);
    expect(people?.guardPermissions).toEqual(["staff:access"]);
  });

  it("points at the paths the package's own links use", () => {
    expect(staffNavItems.map((item) => item.to)).toEqual([
      MY_PAGE_ROUTE_PATH,
      PEOPLE_ROUTE_PATH,
      STAFF_SETTINGS_ROUTE_PATH,
    ]);
  });
});
