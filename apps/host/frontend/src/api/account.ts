import { clearCsrfToken, ensureCsrfToken, request } from "./request";
import { creationOptions, credentialJson, requestOptions, webAuthnAvailable } from "./webauthn";

export type Language = "auto" | "en" | "nb";
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
export type PasskeyOperation = "enrollment" | "sign-in";
export type PasskeyClientErrorCode = "unsupported" | "cancelled" | "failed";

/** A stable error from the local WebAuthn ceremony, before any API submission. */
export class PasskeyClientError extends Error {
  readonly code: PasskeyClientErrorCode;
  readonly operation: PasskeyOperation;

  constructor(code: PasskeyClientErrorCode, operation: PasskeyOperation, cause?: unknown) {
    super(code, cause === undefined ? undefined : { cause });
    this.name = "PasskeyClientError";
    this.code = code;
    this.operation = operation;
  }
}

export const isPasskeyClientError = (error: unknown): error is PasskeyClientError =>
  error instanceof PasskeyClientError;

const json = (method: string, body: unknown): RequestInit => ({
  method,
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify(body),
});
const toProfile = (value: AccountResponse): PersonalProfile => ({
  ...value,
  preferredLanguage:
    value.preferredLanguage?.trim().toLowerCase() === "en"
      ? "en"
      : value.preferredLanguage?.trim().toLowerCase() === "nb"
        ? "nb"
        : "auto",
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

const requireWebAuthn = (operation: PasskeyOperation) => {
  if (!webAuthnAvailable()) throw new PasskeyClientError("unsupported", operation);
};
const cancelled = (error: unknown) => error instanceof DOMException && error.name === "NotAllowedError";

const localCredentialJson = async (
  operation: PasskeyOperation,
  getCredential: () => Promise<Credential | null>,
): Promise<string> => {
  try {
    const credential = await getCredential();
    if (!credential) throw new PasskeyClientError("cancelled", operation);
    return JSON.stringify(credentialJson(credential));
  } catch (error) {
    if (error instanceof PasskeyClientError) throw error;
    throw new PasskeyClientError(cancelled(error) ? "cancelled" : "failed", operation, error);
  }
};

/** Runs the browser ceremony and submits the required JSON string to the API. */
export const enrollPasskey = async (body: { name: string; currentPassword: string }) => {
  requireWebAuthn("enrollment");
  const ceremony = await beginPasskeyEnrollment(body);
  const credential = await localCredentialJson("enrollment", () =>
    navigator.credentials.create({ publicKey: creationOptions(ceremony.options) }),
  );
  await completePasskeyEnrollment({ ...body, ceremonyId: ceremony.ceremonyId, credentialJson: credential });
};

export const loginWithPasskey = async (email: string) => {
  requireWebAuthn("sign-in");
  const ceremony = await beginPasskeyLogin(email);
  const credential = await localCredentialJson("sign-in", () =>
    navigator.credentials.get({ publicKey: requestOptions(ceremony.options) }),
  );
  return completePasskeyLogin(ceremony.ceremonyId, credential);
};
