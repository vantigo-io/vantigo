import { type ComponentType, createContext, type ReactNode, useContext } from "react";

/**
 * A client-side link: whatever router the host uses, given `to` and
 * children. The shell never depends on a router; the host hands its link
 * component to `AppShellLayout`, and shell components that render links
 * (breadcrumbs) pick it up from here.
 */
export type ShellLinkComponent = ComponentType<{ to: string; children?: ReactNode }>;

const ShellLinkContext = createContext<ShellLinkComponent | undefined>(undefined);

export const ShellLinkProvider = ({ link, children }: { link: ShellLinkComponent; children: ReactNode }) => (
  <ShellLinkContext.Provider value={link}>{children}</ShellLinkContext.Provider>
);

/** The host's link component, or undefined when none was provided (plain anchors then). */
export const useShellLink = () => useContext(ShellLinkContext);
