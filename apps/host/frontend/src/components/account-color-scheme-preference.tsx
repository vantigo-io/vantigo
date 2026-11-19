import { useMantineColorScheme } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { useLayoutEffect } from "react";
import { getProfile, profileQueryKey } from "../api/account";
import { fetchSession, sessionQueryKey } from "../api/auth";
import { appPathname, sessionlessPaths } from "../lib/public-paths";

/**
 * Applies the signed-in person's colour scheme. The server's value is the truth;
 * local storage, which the MantineProvider keeps, only mirrors it so the first
 * paint is right before the profile has loaded. Without a session (the public
 * pages, signed out) nothing is applied, so those pages keep following the
 * mirror, or the computer. It asks for the same session and profile as
 * AccountLanguagePreference, so the cache answers it.
 */
export const AccountColorSchemePreference = () => {
  // The pay page, on the public host, asks for no session: the request would
  // leave the host's allowlist (D14). /rsvp and /station are the same.
  const session = useQuery({
    queryKey: sessionQueryKey,
    queryFn: fetchSession,
    enabled: !sessionlessPaths.has(appPathname(window.location.pathname)),
  });
  const userId = session.data?.user.id;
  const profile = useQuery({
    queryKey: profileQueryKey(userId ?? "unknown"),
    queryFn: getProfile,
    enabled: !!userId,
  });
  const { setColorScheme } = useMantineColorScheme();
  const colorScheme = profile.data?.colorScheme;

  useLayoutEffect(() => {
    if (colorScheme) setColorScheme(colorScheme);
  }, [colorScheme, setColorScheme]);

  return null;
};
