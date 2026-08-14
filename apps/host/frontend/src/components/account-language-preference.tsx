import { useQuery } from "@tanstack/react-query";
import { setLanguagePreference } from "@vantigo/frontend-shell";
import { useLayoutEffect, useRef } from "react";
import { getProfile, profileQueryKey } from "../api/account";
import { fetchSession, sessionQueryKey } from "../api/auth";

export const AccountLanguagePreference = () => {
  const session = useQuery({ queryKey: sessionQueryKey, queryFn: fetchSession });
  const userId = session.data?.user.id;
  const profile = useQuery({
    queryKey: profileQueryKey(userId ?? "unknown"),
    queryFn: getProfile,
    enabled: !!userId,
  });
  const previousUserId = useRef<string | undefined>(undefined);
  const hadProfile = useRef(false);

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
