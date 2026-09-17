import { publicPaths } from "./public-paths";

/** Where a session that must enrol is held: the tab with the authenticator form. */
export const mfaEnrolmentDestination = "/settings/security";

/** The tree the gate leaves open: the whole personal settings area, where enrolment happens. */
const settingsTree = "/settings";

const inSettings = (pathname: string) => pathname === settingsTree || pathname.startsWith(`${settingsTree}/`);

/**
 * The MFA enrolment gate. The backend sets `mfaEnrollmentRequired` on the
 * session of an administrator (Owner or SystemAdmin) who has no
 * authenticator while the installation requires MFA for administrators, and
 * refuses every module and control-plane call from that session with 403
 * until one is enabled. Rather than let the user find that out one
 * forbidden page at a time, every signed-in destination outside /settings
 * redirects to the security tab, which explains the situation and holds the
 * form. Public paths are left alone so sign-out and sign-in keep working;
 * a missing session is the sign-in redirect's business, not this one's.
 *
 * Returns the redirect target, or undefined when the path may load.
 */
export const mfaEnrolmentRedirect = (
  session: { mfaEnrollmentRequired?: boolean } | null | undefined,
  pathname: string,
): typeof mfaEnrolmentDestination | undefined => {
  if (session?.mfaEnrollmentRequired !== true) return undefined;
  if (publicPaths.has(pathname) || inSettings(pathname)) return undefined;
  return mfaEnrolmentDestination;
};
