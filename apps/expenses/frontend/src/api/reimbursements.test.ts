import { afterEach, describe, expect, it, vi } from "vitest";
import { downloadReimbursementsCsv, saveCsv } from "./reimbursements";
import { setAuthStateClearer, setUnauthorizedHandler } from "./request";

const tick = () => new Promise((resolve) => setTimeout(resolve, 0));

afterEach(() => {
  setUnauthorizedHandler(undefined);
  setAuthStateClearer(undefined);
});

describe("saveCsv", () => {
  it("lets go of the object URL only after the browser has had the click", async () => {
    // A download is dispatched asynchronously, so revoking in the same turn as
    // the click is a race Chromium happens to win and Firefox and Safari lose.
    const revoke = vi.fn();
    vi.stubGlobal("URL", { ...URL, createObjectURL: () => "blob:payroll", revokeObjectURL: revoke });
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});

    saveCsv({ blob: new Blob(["a;b"]), fileName: "payroll.csv" });

    expect(click).toHaveBeenCalled();
    expect(revoke).not.toHaveBeenCalled();
    await tick();
    expect(revoke).toHaveBeenCalledWith("blob:payroll");
    expect(document.querySelector("a[download]")).toBeNull();
  });
});

describe("downloadReimbursementsCsv", () => {
  const answer = (response: Response) =>
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => response),
    );

  it("signs the person out when the session has expired, as every other request does", async () => {
    const cleared = vi.fn();
    const unauthorized = vi.fn();
    setAuthStateClearer(cleared);
    setUnauthorizedHandler(unauthorized);
    answer(new Response(null, { status: 401 }));

    await expect(downloadReimbursementsCsv({ state: "waiting" })).rejects.toThrow();
    expect(cleared).toHaveBeenCalled();
    expect(unauthorized).toHaveBeenCalled();
  });

  it("leaves the session alone on a refusal that is about the export", async () => {
    const unauthorized = vi.fn();
    setUnauthorizedHandler(unauthorized);
    answer(
      new Response(JSON.stringify({ title: "Too many rows to export", detail: "Narrow the filter." }), {
        status: 400,
        headers: { "Content-Type": "application/problem+json" },
      }),
    );

    await expect(downloadReimbursementsCsv({ state: "waiting" })).rejects.toThrow("Narrow the filter.");
    expect(unauthorized).not.toHaveBeenCalled();
  });

  it("takes the file name off Content-Disposition, RFC 5987 first", async () => {
    answer(
      new Response("a;b", {
        status: 200,
        headers: { "Content-Disposition": "attachment; filename*=UTF-8''l%C3%B8nn.csv" },
      }),
    );

    expect((await downloadReimbursementsCsv({ state: "waiting" })).fileName).toBe("lønn.csv");
  });
});
