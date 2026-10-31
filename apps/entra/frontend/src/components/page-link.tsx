import { Anchor } from "@mantine/core";
import { appUrl, useShellLink } from "@vantigo/frontend-shell";
import type { ReactNode } from "react";

/**
 * A link to another page of the installation: the host's client-side link
 * where it provides one, a plain anchor under the base path otherwise.
 */
export const PageLink = ({ to, children }: { to: string; children: ReactNode }) => {
  const Link = useShellLink();
  return Link ? (
    <Anchor size="sm" renderRoot={(props) => <Link to={to} {...props} />}>
      {children}
    </Anchor>
  ) : (
    <Anchor size="sm" href={appUrl(to)}>
      {children}
    </Anchor>
  );
};
