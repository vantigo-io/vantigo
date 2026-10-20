import { describe, expect, it } from "vitest";
import { staffNavItems, visibleStaffNavItems } from "./navigation";
import {
  ABSENCE_ROUTE_PATH,
  GROUPS_ROUTE_PATH,
  MY_PAGE_ROUTE_PATH,
  PEOPLE_ROUTE_PATH,
  STAFF_SETTINGS_ROUTE_PATH,
} from "./routes";

const offered = (permissions: string[] | undefined) => visibleStaffNavItems(permissions).map((item) => item.key);

describe("the Staff sidebar", () => {
  it("offers My page to everyone in the app, People to the directory, Absence to who reads or decides it, Groups and Settings to HR", () => {
    expect(offered(["staff:access"])).toEqual(["myPage"]);
    expect(offered(["staff:access", "staff:view"])).toEqual(["myPage", "people", "absence"]);
    // HR without the directory lists people too (the server answers it), so the sidebar offers it.
    expect(offered(["staff:access", "staff:manage"])).toEqual(["myPage", "people", "absence", "groups", "settings"]);
    expect(offered(["staff:access", "staff:view", "staff:manage"])).toEqual([
      "myPage",
      "people",
      "absence",
      "groups",
      "settings",
    ]);
    // The Approver decides everyone's requests; staff:personal-data alone decides the HR-only types (plan reading 33).
    expect(offered(["staff:access", "staff:approve"])).toEqual(["myPage", "absence"]);
    expect(offered(["staff:access", "staff:personal-data"])).toEqual(["myPage", "absence"]);
    expect(offered(["*"])).toEqual(["myPage", "people", "absence", "groups", "settings"]);
    expect(offered(undefined)).toEqual([]);
  });

  it("guards Absence's URL by staff:access alone, so a manager by the terms opens the dashboard's link", () => {
    const absence = staffNavItems.find((item) => item.key === "absence");
    expect(absence?.requiredPermissions).toEqual([
      "staff:view",
      "staff:approve",
      "staff:manage",
      "staff:personal-data",
    ]);
    expect(absence?.guardPermissions).toEqual(["staff:access"]);
  });

  it("guards People's URL by staff:access alone, so HR without the directory still lists", () => {
    const people = staffNavItems.find((item) => item.key === "people");
    expect(people?.requiredPermissions).toEqual(["staff:view", "staff:manage"]);
    expect(people?.guardPermissions).toEqual(["staff:access"]);
  });

  it("points at the paths the package's own links use", () => {
    expect(staffNavItems.map((item) => item.to)).toEqual([
      MY_PAGE_ROUTE_PATH,
      PEOPLE_ROUTE_PATH,
      ABSENCE_ROUTE_PATH,
      GROUPS_ROUTE_PATH,
      STAFF_SETTINGS_ROUTE_PATH,
    ]);
  });
});
