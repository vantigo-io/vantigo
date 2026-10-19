import { useQuery } from "@tanstack/react-query";
import { setLanguagePreference, setTimeZones } from "@vantigo/frontend-shell";
import { useLayoutEffect, useRef } from "react";
import { getProfile, profileQueryKey } from "../api/account";
import { fetchSession, sessionQueryKey } from "../api/auth";
import { appPathname, sessionlessPaths } from "../lib/public-paths";

export const AccountLanguagePreference = () => {
  // The pay page, on the public host, asks for no session: the request would
  // leave the host's allowlist (D14). A session the root fetched still arrives
  // from the cache; on /pay there is none.
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
  const previousUserId = useRef<string | undefined>(undefined);
  const hadProfile = useRef(false);

  // The session's time zones (time zones design D4, D8) reach the shell the
  // same way the language does: every formatter shows instants in the
  // effective zone, and module screens judge "today" in the business zone.
  // No session (signed out, /pay) puts both back on the browser's zone.
  const timeZone = session.data?.timeZone;
  const timeZoneSource = session.data?.timeZoneSource;
  const businessTimeZone = session.data?.businessTimeZone;
  const hasSession = !!session.data;
  useLayoutEffect(() => {
    setTimeZones(hasSession ? { timeZone, timeZoneSource, businessTimeZone } : null);
  }, [hasSession, timeZone, timeZoneSource, businessTimeZone]);

  useLayoutEffect(() => {
    if (!userId) {
      // A pending initial session must not clear the initial automatic preference, but a
      // known session disappearing must clear the previous account's preference now.
      if (previousUserId.current || !session.isPending) setLanguagePreference("auto");
      previousUserId.current = undefined;
      hadProfile.current = false;
      return;
    }

    if (previousUserId.current !== userId) {
      previousUserId.current = userId;
      hadProfile.current = false;
      if (!profile.data) setLanguagePreference("auto");
    }

    if (profile.data) {
      hadProfile.current = true;
      setLanguagePreference(profile.data.preferredLanguage);
    } else if (hadProfile.current && !profile.isPending) {
      // A settled profile error/removal must not leave an old account preference active.
      hadProfile.current = false;
      setLanguagePreference("auto");
    }
  }, [profile.data, profile.isPending, session.isPending, userId]);

  return null;
};
