import type { Session, Tenant } from "../api/auth";

export const tenantForSlug = (session: Session | null, slug: string): Tenant | undefined =>
  session?.tenants?.find((tenant) => tenant.slug === slug);

export const activeTenantForSession = (session: Session | null): Tenant | undefined => {
  const tenants = session?.tenants ?? [];
  return (
    tenants.find((tenant) => tenant.id === session?.activeTenantId) ?? (tenants.length === 1 ? tenants[0] : undefined)
  );
};

export const synchronizeTenant = async (
  session: Session,
  slug: string,
  switcher: (tenantId: string) => Promise<Session>,
) => {
  const tenant = tenantForSlug(session, slug);
  if (!tenant) return { session, tenant: undefined };
  if (tenant.id === session.activeTenantId) return { session, tenant };
  return { session: await switcher(tenant.id), tenant };
};

export const legacyTenantPath = (pathname: string, searchStr: string, hash: string, slug?: string) => {
  if (!slug) return undefined;
  const prefixes = ["/customers", "/contacts", "/inbox", "/communications", "/products", "/energy"];
  if (!prefixes.some((prefix) => pathname === prefix || pathname.startsWith(`${prefix}/`))) return undefined;
  return `/${encodeURIComponent(slug)}${pathname}${searchStr}${hash ? `#${hash}` : ""}`;
};
