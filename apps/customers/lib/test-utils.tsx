import { MantineProvider } from "@mantine/core";
import { render as testingLibraryRender } from "@testing-library/react";

/** Renders UI wrapped in the providers required by Mantine components. */
export function render(ui: React.ReactNode) {
  return testingLibraryRender(<MantineProvider>{ui}</MantineProvider>);
}
