import { request } from "./request";

/**
 * Cross-module writes. Staff never imports the host or identity's frontend;
 * the person's own picture is their Vantigo avatar (photo sync design S2), so
 * My page's Change photo and Remove photo call identity's account API — the
 * signed-in user's own — the way Time calls the projects HTTP API. While HR's
 * photo stands identity refuses both with a 409 `avatar_managed`, its
 * sentence the error's message.
 */

/** Sets the caller's own avatar: one part named `avatar`, identity's name, the `Content-Type` left to the browser. */
export const uploadMyAvatar = (file: File) => {
  const form = new FormData();
  form.append("avatar", file, file.name);
  return request<{ uploaded: boolean; url: string }>("/api/v1/identity/account/avatar", { method: "POST", body: form });
};

/** Removes the caller's own avatar. */
export const removeMyAvatar = () => request<void>("/api/v1/identity/account/avatar", { method: "DELETE" });
