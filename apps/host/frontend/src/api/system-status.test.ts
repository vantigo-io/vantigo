import { afterEach, describe, expect, it, vi } from "vitest";
import { request } from "./request";
import { fetchSystemStatus, setMaintenance } from "./system-status";

vi.mock("./request", () => ({ request: vi.fn() }));

describe("system status API", () => {
  afterEach(() => vi.clearAllMocks());

  it("fetches the anonymous maintenance status endpoint", async () => {
    vi.mocked(request).mockResolvedValue({ maintenance: false, message: null });

    await fetchSystemStatus();

    expect(request).toHaveBeenCalledWith("/api/v1/identity/system/status", { handleUnauthorized: false });
  });

  it("updates maintenance mode with the expected JSON body", async () => {
    vi.mocked(request).mockResolvedValue({ maintenance: true, message: "Deploying" });

    await setMaintenance({ enabled: true, message: "Deploying" });

    expect(request).toHaveBeenCalledWith("/api/v1/identity/system/maintenance", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ enabled: true, message: "Deploying" }),
    });
  });
});
