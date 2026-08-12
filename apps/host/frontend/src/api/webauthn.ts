const bytesToBase64Url = (value: ArrayBuffer | Uint8Array) => {
  const bytes = value instanceof Uint8Array ? value : new Uint8Array(value);
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
};

const base64UrlToBytes = (value: string) => {
  const normalized = value
    .replace(/-/g, "+")
    .replace(/_/g, "/")
    .padEnd(Math.ceil(value.length / 4) * 4, "=");
  const binary = atob(normalized);
  return Uint8Array.from(binary, (character) => character.charCodeAt(0));
};

const decodeBinary = (value: unknown) => (typeof value === "string" ? base64UrlToBytes(value) : value);

export const webAuthnAvailable = () =>
  typeof window !== "undefined" && "PublicKeyCredential" in window && !!navigator.credentials;

const fallbackCreationOptions = (input: unknown): PublicKeyCredentialCreationOptions => {
  const options = { ...(input as Record<string, unknown>) };
  if (typeof options.challenge === "string") options.challenge = decodeBinary(options.challenge);
  if (options.user && typeof options.user === "object")
    options.user = {
      ...(options.user as Record<string, unknown>),
      id: decodeBinary((options.user as Record<string, unknown>).id),
    };
  if (Array.isArray(options.excludeCredentials))
    options.excludeCredentials = options.excludeCredentials.map((entry) => ({
      ...(entry as Record<string, unknown>),
      id: decodeBinary((entry as Record<string, unknown>).id),
    }));
  return options as unknown as PublicKeyCredentialCreationOptions;
};

const fallbackRequestOptions = (input: unknown): PublicKeyCredentialRequestOptions => {
  const options = { ...(input as Record<string, unknown>) };
  if (typeof options.challenge === "string") options.challenge = decodeBinary(options.challenge);
  if (Array.isArray(options.allowCredentials))
    options.allowCredentials = options.allowCredentials.map((entry) => ({
      ...(entry as Record<string, unknown>),
      id: decodeBinary((entry as Record<string, unknown>).id),
    }));
  return options as unknown as PublicKeyCredentialRequestOptions;
};

export const creationOptions = (options: unknown) => {
  const credential = window.PublicKeyCredential as typeof PublicKeyCredential & {
    parseCreationOptionsFromJSON?: (value: unknown) => PublicKeyCredentialCreationOptions;
  };
  return credential.parseCreationOptionsFromJSON
    ? credential.parseCreationOptionsFromJSON(options)
    : fallbackCreationOptions(options);
};

export const requestOptions = (options: unknown) => {
  const credential = window.PublicKeyCredential as typeof PublicKeyCredential & {
    parseRequestOptionsFromJSON?: (value: unknown) => PublicKeyCredentialRequestOptions;
  };
  return credential.parseRequestOptionsFromJSON
    ? credential.parseRequestOptionsFromJSON(options)
    : fallbackRequestOptions(options);
};

const responseJson = (response: AuthenticatorAttestationResponse | AuthenticatorAssertionResponse) => {
  const result: Record<string, unknown> = {
    clientDataJSON: bytesToBase64Url(response.clientDataJSON),
  };
  if ("attestationObject" in response) result.attestationObject = bytesToBase64Url(response.attestationObject);
  if ("authenticatorData" in response) result.authenticatorData = bytesToBase64Url(response.authenticatorData);
  if ("signature" in response) result.signature = bytesToBase64Url(response.signature);
  if ("userHandle" in response) result.userHandle = response.userHandle ? bytesToBase64Url(response.userHandle) : null;
  return result;
};

const jsonSafeValue = (value: unknown): unknown => {
  if (value instanceof ArrayBuffer || value instanceof Uint8Array) return bytesToBase64Url(value);
  if (Array.isArray(value)) return value.map(jsonSafeValue);
  if (value && typeof value === "object")
    return Object.fromEntries(Object.entries(value).map(([key, entry]) => [key, jsonSafeValue(entry)]));
  return value;
};

const jsonSafeExtensions = (value: Record<string, unknown>) => jsonSafeValue(value);

export const credentialJson = (credential: Credential) => {
  const publicCredential = credential as PublicKeyCredential;
  return {
    id: publicCredential.id,
    rawId: bytesToBase64Url(publicCredential.rawId),
    type: publicCredential.type,
    response: responseJson(
      publicCredential.response as AuthenticatorAttestationResponse | AuthenticatorAssertionResponse,
    ),
    clientExtensionResults: jsonSafeExtensions(publicCredential.getClientExtensionResults() as Record<string, unknown>),
    authenticatorAttachment: publicCredential.authenticatorAttachment,
  };
};
