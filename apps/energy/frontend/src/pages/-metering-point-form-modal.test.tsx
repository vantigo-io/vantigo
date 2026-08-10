import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { MeteringPointFormModal } from "./-metering-point-form-modal";

describe("MeteringPointFormModal", () => {
  it("rejects a GSRN that is not exactly 18 digits", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("[]", { status: 200 })));
    render(
      <MantineProvider>
        <QueryClientProvider client={new QueryClient()}>
          <MeteringPointFormModal state={{ mode: "create" }} onClose={vi.fn()} />
        </QueryClientProvider>
      </MantineProvider>,
    );
    fireEvent.change(screen.getByRole("textbox", { name: /GSRN/ }), { target: { value: "123" } });
    fireEvent.click(screen.getByRole("button", { name: "Create metering point" }));
    expect(await screen.findByText("GSRN must contain exactly 18 digits")).toBeInTheDocument();
  });
});
