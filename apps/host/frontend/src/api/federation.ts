import { appUrl } from "@vantigo/frontend-shell";
import { request } from "./request";

export type FederationProviderType = "Entra" | "Google" | "Generic";
export type JitCreationMode = "Disabled" | "CreateUser";

export interface FederationConnection {
  id: string;
  providerType: FederationProviderType;
  displayName: string;
  authority: string;
  clientId: string;
  allowedDomains: string[];
  isEnabled: boolean;
  isDefault: boolean;
  jitCreationMode: JitCreationMode;
  configurationVersion: number;
  concurrencyStamp: string;
  clientSecretReference: string | null;
  validationState: string;
  validationErrorCode: string | null;
  validationCompletedAt: string | null;
  validatedConfigurationVersion: number | null;
  validatedIssuer: string | null;
  validatedDiscoveryEndpoint: string | null;
  validatedAuthorizationEndpoint: string | null;
  validatedTokenEndpoint: string | null;
  validatedJwksUri: string | null;
}

export interface FederationConnectionInput {
  providerType: FederationProviderType;
  displayName: string;
  authority: string;
  clientId: string;
  clientSecretReference?: string;
  allowedDomains: string[];
  isEnabled: boolean;
  isDefault: boolean;
  jitCreationMode: JitCreationMode;
  concurrencyStamp?: string;
  clearClientSecretReference?: boolean;
}

const base = "/api/v1/identity/access/federation-connections";
const mutation = (method: string, body: unknown) =>
  request<FederationConnection>(base, {
    method,
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });

export const listFederationConnections = () => request<FederationConnection[]>(base);
export const createFederationConnection = (input: FederationConnectionInput) => mutation("POST", input);
export const updateFederationConnection = (id: string, input: FederationConnectionInput) =>
  request<FederationConnection>(`${base}/${id}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
export const validateFederationConnection = (id: string, concurrencyStamp: string) =>
  request<{ connection: FederationConnection; succeeded: boolean; code: string; message: string }>(
    `${base}/${id}/validate`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ concurrencyStamp }),
    },
  );
export const setFederationConnectionEnabled = (
  id: string,
  concurrencyStamp: string,
  enabled: boolean,
  isDefault = false,
) =>
  request<FederationConnection>(`${base}/${id}/${enabled ? "enable" : "disable"}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ concurrencyStamp, isDefault }),
  });
export const deleteFederationConnection = (id: string, concurrencyStamp: string) =>
  request<void>(`${base}/${id}`, {
    method: "DELETE",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ concurrencyStamp }),
  });

export interface FederationProviderDiscovery {
  id: string;
  displayName: string;
  providerKind: FederationProviderType;
}
export const fetchPublicSignInProviders = () =>
  request<FederationProviderDiscovery[]>("/api/v1/identity/federation/providers", { handleUnauthorized: false });
export const federationChallengeUrl = (id: string, returnPath: "/" | "/sign-in" = "/sign-in") =>
  `${appUrl(`/api/v1/identity/federation/${encodeURIComponent(id)}/challenge`)}?returnPath=${encodeURIComponent(returnPath)}`;
