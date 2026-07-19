import { twoFactorClient } from "better-auth/client/plugins";
import { createAuthClient } from "better-auth/react";

// Same-origin: better-auth defaults to the current origin,
// so no server config is needed (and lib/config must not leak into client bundles).
export const authClient = createAuthClient({
  plugins: [
    twoFactorClient({
      onTwoFactorRedirect() {
        // Sign-in was intercepted because the user has 2FA enabled.
        window.location.href = "/two-factor";
      },
    }),
  ],
});
