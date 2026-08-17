import { describe, expect, it, vi } from "vitest";
import type { Session } from "../api/auth";
import { activeTenantForSession, legacyTenantPath, synchronizeTenant, tenantForSlug } from "./-tenant-routing";

const session = (activeTenantId: string | null = "tenant-a"): Session => ({
  user: { id: "user-1", displayName: "Test User", email: "test@example.com", roles: [] },
  isSystemAdmin: false,
  activeTenantId,
  tenants: [
    { id: "tenant-a", name: "Alpha", slug: "alpha" },
    { id: "tenant-b", name: "Beta", slug: "beta" },
  ],
});

describe("tenant route guards", () => {
  it("rejects a slug for a tenant the session does not contain", () => {
    expect(tenantForSlug(session(), "unknown")).toBeUndefined();
  });

  it("switches the session when the URL tenant differs from the active tenant", async () => {
    const switcher = vi.fn(async () => session("tenant-b"));
    const result = await synchronizeTenant(session(), "beta", switcher);

    expect(switcher).toHaveBeenCalledWith("tenant-b");
    expect(result.tenant?.slug).toBe("beta");
    expect(result.session.activeTenantId).toBe("tenant-b");
  });

  it("redirects legacy paths to the active tenant while preserving search and hash", () => {
    expect(legacyTenantPath("/customers/42", "?tab=energy", "details", activeTenantForSession(session())?.slug)).toBe(
      "/alpha/customers/42?tab=energy#details",
    );
  });
});
