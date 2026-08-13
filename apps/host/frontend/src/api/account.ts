import { clearCsrfToken, ensureCsrfToken, request } from "./request";
import { creationOptions, credentialJson, requestOptions, webAuthnAvailable } from "./webauthn";

export type Language = "auto" | "en";
export interface PersonalProfile {
  displayName: string;
  email: string | null;
  preferredLanguage: Language;
  avatarUrl: string | null;
}
interface AccountResponse {
  displayName: string;
  email: string | null;
  preferredLanguage: string | null;
  avatarUrl: string | null;
}
export interface PersonalMfaStatus {
  twoFactorEnabled: boolean;
  mfaEnrollmentRequired: boolean;
}
export const profileQueryKey = (userId: string) => ["account", "profile", userId] as const;
export interface MfaSetup {
  sharedKey: string | null;
  authenticatorUri: string | null;
  initialized: boolean;
}
export interface Passkey {
  credentialId: string;
  name: string;
  createdAt: string;
  transports: string[];
  isUserVerified: boolean;
  isBackupEligible: boolean;
  isBackedUp: boolean;
}
export interface PasskeyCeremony {
  ceremonyId: string;
  options: unknown;
}
export interface PasskeyLoginSession {
  user: { id: string; displayName: string; email: string; roles: string[] };
  twoFactorEnabled?: boolean;
  mfaEnrollmentRequired?: boolean;
  mfaAuthenticated?: boolean;
}

const json = (method: string, body: unknown): RequestInit => ({
  method,
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify(body),
});
const toProfile = (value: AccountResponse): PersonalProfile => ({
  ...value,
  preferredLanguage: value.preferredLanguage?.toLowerCase() === "en" ? "en" : "auto",
});
const account = "/api/v1/identity/account";
export const getProfile = async () => toProfile(await request<AccountResponse>(account));
export const updateProfile = async (body: Pick<PersonalProfile, "displayName" | "preferredLanguage">) =>
  toProfile(
    await request<AccountResponse>(
      `${account}/profile`,
      json("PATCH", {
        displayName: body.displayName,
        preferredLanguage: body.preferredLanguage === "auto" ? "automatic" : body.preferredLanguage,
      }),
    ),
  );
export const uploadProfilePhoto = (file: File) => {
  const body = new FormData();
  body.append("avatar", file);
  return request<{ uploaded: boolean; url: string }>(`${account}/avatar`, { method: "POST", body });
};
export const removeProfilePhoto = () => request<void>(`${account}/avatar`, { method: "DELETE" });
export const changePassword = (body: { currentPassword: string; newPassword: string }) =>
  request<{ success: boolean }>(`${account}/password`, json("POST", body));
export const getMfaStatus = () => request<PersonalMfaStatus>("/api/v1/identity/owner/mfa");
export const initializeMfa = (password: string) =>
  request<MfaSetup>("/api/v1/identity/owner/mfa/setup", json("POST", { password }));
export const enableMfa = (body: { code: string; password: string }) =>
  request<{ twoFactorEnabled: boolean; recoveryCodes: string[] }>(
    "/api/v1/identity/owner/mfa/enable",
    json("POST", body),
  );
export const disableMfa = (password: string) =>
  request<PersonalMfaStatus>("/api/v1/identity/owner/mfa/disable", json("POST", { password }));
export const regenerateRecoveryCodes = (body: { code: string; password: string }) =>
  request<{ recoveryCodes: string[] }>("/api/v1/identity/owner/mfa/recovery-codes", json("POST", body));
export const listPasskeys = () => request<Passkey[]>(`${account}/passkeys`);
export const removePasskey = (credentialId: string, currentPassword: string) =>
  request<void>(`${account}/passkeys/${encodeURIComponent(credentialId)}`, json("DELETE", { currentPassword }));
export const beginPasskeyEnrollment = (body: { name: string; currentPassword: string }) =>
  request<PasskeyCeremony>(`${account}/passkeys/begin`, json("POST", body));
export const completePasskeyEnrollment = (body: {
  ceremonyId: string;
  credentialJson: string;
  currentPassword: string;
}) => request<void>(`${account}/passkeys/complete`, json("POST", body));

export const beginPasskeyLogin = (email: string) =>
  request<PasskeyCeremony>("/api/v1/identity/passkeys/login/begin", json("POST", { email }));
export const completePasskeyLogin = async (ceremonyId: string, credential: string) => {
  const session = await request<PasskeyLoginSession>(
    "/api/v1/identity/passkeys/login/complete",
    json("POST", { ceremonyId, credentialJson: credential }),
  );
  clearCsrfToken();
  await ensureCsrfToken();
  return session;
};

const requireWebAuthn = () => {
  if (!webAuthnAvailable()) throw new Error("Passkeys are not supported on this device or browser.");
};
const cancelled = (error: unknown) => error instanceof DOMException && error.name === "NotAllowedError";

/** Runs the browser ceremony and submits the required JSON string to the API. */
export const enrollPasskey = async (body: { name: string; currentPassword: string }) => {
  requireWebAuthn();
  const ceremony = await beginPasskeyEnrollment(body);
  try {
    const credential = await navigator.credentials.create({ publicKey: creationOptions(ceremony.options) });
    if (!credential) throw new Error("Passkey enrollment was cancelled.");
    await completePasskeyEnrollment({
      ...body,
      ceremonyId: ceremony.ceremonyId,
      credentialJson: JSON.stringify(credentialJson(credential)),
    });
  } catch (error) {
    if (cancelled(error)) throw new Error("Passkey enrollment was cancelled.", { cause: error });
    throw error;
  }
};

export const loginWithPasskey = async (email: string) => {
  requireWebAuthn();
  const ceremony = await beginPasskeyLogin(email);
  try {
    const credential = await navigator.credentials.get({ publicKey: requestOptions(ceremony.options) });
    if (!credential) throw new Error("Passkey sign-in was cancelled.");
    return completePasskeyLogin(ceremony.ceremonyId, JSON.stringify(credentialJson(credential)));
  } catch (error) {
    if (cancelled(error)) throw new Error("Passkey sign-in was cancelled.", { cause: error });
    throw error;
  }
};
