const publicRoutes = new Set([
  "/sign-in",
  "/accept-invitation",
  "/forgot-password",
  "/reset-password",
  "/invitations/accept",
  "/password-reset",
  "/setup",
]);

export const isPublicRoute = (pathname: string) => publicRoutes.has(pathname);
