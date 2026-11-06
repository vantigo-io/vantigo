import { MantineProvider } from "@mantine/core";
import { ModalsProvider } from "@mantine/modals";
import { Notifications } from "@mantine/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render } from "@testing-library/react";
import type { ReactNode } from "react";

/**
 * Mounts one of the package's components under the providers the host gives
 * it: Mantine in `env="test"` so menus and modals render without transitions,
 * the notifications and modals managers the mutations use, and a fresh query
 * client per test so nothing is cached between them.
 */
export const renderWithProviders = (ui: ReactNode) => {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const result = render(
    <MantineProvider env="test">
      <Notifications />
      <ModalsProvider>
        <QueryClientProvider client={queryClient}>{ui}</QueryClientProvider>
      </ModalsProvider>
    </MantineProvider>,
  );
  return { ...result, queryClient };
};
