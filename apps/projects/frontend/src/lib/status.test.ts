import { describe, expect, it } from "vitest";
import { projectsCatalog } from "../i18n";
import { projectStatusColor, projectStatuses, projectStatusLabelKey } from "./status";

describe("projectStatusColor", () => {
  it("gives every status the badge colour the design asks for", () => {
    expect(Object.fromEntries(projectStatuses.map((status) => [status, projectStatusColor(status)]))).toEqual({
      planned: "gray",
      active: "green",
      "on-hold": "yellow",
      completed: "blue",
      cancelled: "red",
    });
  });
});

describe("projectStatusLabelKey", () => {
  it("names a key the catalog carries in both languages", () => {
    for (const status of projectStatuses) {
      const key = projectStatusLabelKey(status);
      expect(projectsCatalog.en).toHaveProperty(key);
      expect(projectsCatalog.nb).toHaveProperty(key);
    }
  });
});
