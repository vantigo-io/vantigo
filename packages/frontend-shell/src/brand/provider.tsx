import { MantineProvider, type MantineProviderProps, useComputedColorScheme } from "@mantine/core";
import { createContext, type ReactNode, useCallback, useContext, useEffect, useMemo, useState } from "react";
import { appConfig, appUrl } from "../app-config";
import { vantigoLogo, vantigoLogoDark } from "../logo";
import { createVantigoTheme, vantigoCssVariablesResolver } from "../theme";
import { clearBrandMirror, readBrandMirror, understands, writeBrandMirror } from "./mirror";
import { brandRefetchDelay } from "./refetch";
import { WHITE } from "./scale";
import { BRAND_PAYLOAD_VERSION, type BrandPayload, type BrandTokens, builtinPayload, RADIUS_VALUES } from "./tokens";

/** Where the admin's "preview as me" is kept for the tab (brands design B20). */
export const BRAND_PREVIEW_KEY = "vantigo-brand-preview";
/** Fired on `window` when the preview changes, so the provider asks again. */
export const BRAND_PREVIEW_EVENT = "vantigo:brand-preview";

/** Fired on `window` to make the provider ask the server for the brand again (a live event, a choice). */
export const BRAND_REFRESH_EVENT = "vantigo:brand-refresh";

/** Asks the provider to fetch the brand again. */
export function refreshBrand(): void {
  window.dispatchEvent(new Event(BRAND_REFRESH_EVENT));
}

/** The brand being previewed in this tab, or null. */
export function getBrandPreview(): string | null {
  try {
    return window.sessionStorage.getItem(BRAND_PREVIEW_KEY);
  } catch {
    return null;
  }
}

/** Starts (a brand id) or stops (null) "preview as me". */
export function setBrandPreview(brandId: string | null): void {
  try {
    if (brandId) window.sessionStorage.setItem(BRAND_PREVIEW_KEY, brandId);
    else window.sessionStorage.removeItem(BRAND_PREVIEW_KEY);
  } catch {
    // Without session storage there is no preview.
  }
  window.dispatchEvent(new Event(BRAND_PREVIEW_EVENT));
}

/** What the shell shows of the Vantigo mark when a brand has none: the bundled logo. */
export interface BrandMark {
  src: string;
  /** True when the mark is a light-background mark shown on a dark surface: the shell puts it on a chip. */
  chip: boolean;
}

export interface Brand extends BrandPayload {
  /** The logo for the colour scheme (B14): the brand's own, else the bundled Vantigo mark. */
  markFor: (scheme: "light" | "dark") => BrandMark;
}

const brandOf = (payload: BrandPayload): Brand => ({
  ...payload,
  markFor: (scheme) => {
    const { mark_light: light, mark_dark: dark } = payload.logos;
    if (scheme === "dark") {
      if (dark) return { src: dark, chip: false };
      if (light) return { src: light, chip: true };
      return { src: vantigoLogoDark, chip: false };
    }
    return { src: light ?? vantigoLogo, chip: false };
  },
});

/**
 * The brand with nothing from the server: Vantigo's own, plus whatever the environment still injects
 * (APP_TITLE, APP_LOGO_URL, APP_SUPPORT_* are honoured until the default brand has imported them, B17).
 */
function fallbackPayload(): BrandPayload {
  const base = builtinPayload();
  const config = appConfig();
  return {
    ...base,
    title: config.title,
    name: config.title,
    logos: config.logoUrl ? { mark_light: config.logoUrl } : {},
    support: { ...config.support },
  };
}

/**
 * The brand the stylesheet painted, read back from the document, so the theme does not flash to
 * indigo between the first paint and the answer of the server. Null when the stylesheet did not load.
 */
export function brandFromDocument(): BrandPayload | null {
  const style = getComputedStyle(document.documentElement);
  if (!style.getPropertyValue("--vantigo-brand-loaded")) return null;
  const shades = Array.from({ length: 10 }, (_, i) => style.getPropertyValue(`--mantine-color-vantigo-${i}`).trim());
  if (shades.some((s) => !/^#[0-9a-f]{6}$/i.test(s))) return null;
  const radius = style.getPropertyValue("--mantine-radius-default").trim();
  const known = Object.values(RADIUS_VALUES).includes(radius) ? radius : RADIUS_VALUES.md;
  const base = fallbackPayload();
  const scheme = {
    shades,
    filledText: WHITE,
    link: shades[8] as string,
    subtleBg: shades[0] as string,
    derived: {},
  };
  return { ...base, tokens: { light: scheme, dark: scheme, radius: known } };
}

const initialPayload = (): BrandPayload => brandFromDocument() ?? fallbackPayload();

/** A payload this client understands, or null. */
export function parsePayload(body: unknown): BrandPayload | null {
  const p = body as Partial<BrandPayload> | null;
  if (!p || typeof p !== "object" || !understands(p.v, p.algorithm) || (p.v as number) > BRAND_PAYLOAD_VERSION)
    return null;
  if (typeof p.title !== "string" || typeof p.name !== "string" || !p.tokens?.light?.shades || !p.tokens.dark?.shades) {
    return null;
  }
  return { ...p, logos: p.logos ?? {}, support: p.support ?? {} } as BrandPayload;
}

/** Removes the variables the pre-paint script set from the mirror: the stylesheet and the theme own them now. */
function clearInlineBrand(): void {
  const root = document.documentElement;
  for (const name of Array.from(root.style)) {
    if (name.startsWith("--mantine-") || name.startsWith("--vantigo-")) root.style.removeProperty(name);
  }
}

/** Points every `rel=icon` link at the brand's icon, or back at what the document shipped with. */
function applyFavicon(icon: string | undefined): void {
  for (const link of Array.from(document.querySelectorAll<HTMLLinkElement>('link[rel~="icon"]'))) {
    if (link.dataset.vantigoOriginal === undefined) link.dataset.vantigoOriginal = link.getAttribute("href") ?? "";
    link.setAttribute("href", icon ?? link.dataset.vantigoOriginal);
  }
}

const BrandContext = createContext<Brand | null>(null);
const DomainBrandContext = createContext<((payload: BrandPayload | null) => void) | null>(null);

/**
 * Shows another brand than the provider's for as long as the page asks (brands design B26): the sign-in
 * page shows the brand of the typed address's domain. `null` goes back to the provider's own. It is
 * never mirrored to local storage, and it is dropped when a person signs in.
 */
export function useSetDomainBrand(): (payload: BrandPayload | null) => void {
  const set = useContext(DomainBrandContext);
  return set ?? noDomainBrand;
}
const noDomainBrand = () => {};

export interface BrandProviderProps {
  /**
   * Who is signed in: `undefined` while that is not known yet, `null` when nobody is, else the
   * person's id. The brand is the person's own when signed in (`GET /brand`), the public one otherwise.
   */
  userId: string | null | undefined;
  /**
   * Pages that ask the server for no session at all (the pay page, the RSVP page, a station): the
   * stylesheet already carries the default brand, and nothing is requested.
   */
  sessionless?: boolean;
  children: ReactNode;
}

async function fetchPayload(path: string): Promise<{ status: number; payload: BrandPayload | null }> {
  const res = await fetch(appUrl(path), { headers: { Accept: "application/json" }, credentials: "same-origin" });
  if (!res.ok) return { status: res.status, payload: null };
  return { status: res.status, payload: parsePayload(await res.json()) };
}

/**
 * Holds the brand (brands design B13, B14): the person's own when signed in, the public one before,
 * the one the document was painted with until the server has answered. It mirrors the brand to local
 * storage for the next offline load, clears the mirror on sign-out, and sets the tab's title and icon.
 */
export function BrandProvider({ userId, sessionless = false, children }: BrandProviderProps) {
  const [fetched, setPayload] = useState<BrandPayload>(initialPayload);
  const [domainBrand, setDomainBrandState] = useState<BrandPayload | null>(null);
  const payload = domainBrand ?? fetched;
  const setDomainBrand = useCallback((next: BrandPayload | null) => {
    if (next) clearInlineBrand(); // the pre-paint mirror's inline variables would outrank the theme
    setDomainBrandState(next);
  }, []);
  useEffect(() => {
    if (userId) setDomainBrandState(null); // a person's own brand takes over
  }, [userId]);
  const [previewTick, setPreviewTick] = useState(0);

  useEffect(() => {
    const onAgain = () => setPreviewTick((n) => n + 1);
    window.addEventListener(BRAND_PREVIEW_EVENT, onAgain);
    window.addEventListener(BRAND_REFRESH_EVENT, onAgain);
    return () => {
      window.removeEventListener(BRAND_PREVIEW_EVENT, onAgain);
      window.removeEventListener(BRAND_REFRESH_EVENT, onAgain);
    };
  }, []);

  useEffect(() => {
    void previewTick;
    if (sessionless) {
      // An anonymous page (the pay page, a station) asks for no brand, but a mirror left by someone who
      // used this browser must not be painted for whoever is here now if the stylesheet fails.
      clearBrandMirror();
      return;
    }
    if (userId === undefined) return;
    let cancelled = false;
    (async () => {
      let result: { status: number; payload: BrandPayload | null };
      if (userId === null) {
        clearBrandMirror();
        result = await fetchPayload("/api/v1/identity/brand/public");
      } else {
        readBrandMirror(userId); // purges another person's mirror
        const preview = getBrandPreview();
        result = await fetchPayload(
          `/api/v1/identity/brand${preview ? `?preview=${encodeURIComponent(preview)}` : ""}`,
        );
        if (preview && (result.status === 403 || result.status === 404)) {
          setBrandPreview(null); // not allowed, or gone: stop previewing and ask again for one's own
          return;
        }
      }
      if (cancelled || !result.payload) return;
      clearInlineBrand();
      setPayload(result.payload);
      if (userId && result.payload.source !== "preview") writeBrandMirror(userId, result.payload);
    })().catch(() => {
      // Offline or failed: the stylesheet, and the mirror behind it, keep the page right.
    });
    return () => {
      cancelled = true;
    };
  }, [userId, sessionless, previewTick]);

  // A campaign starts or ends at a date the server named (B24): ask again then, at the latest in six
  // hours, so no timer is longer than a browser keeps, and ask when a tab wakes after the boundary
  // (a laptop that slept) instead of waiting for a timer that never ran.
  useEffect(() => {
    if (sessionless || userId === undefined) return;
    const validUntil = fetched.validUntil;
    if (!validUntil) return;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const arm = () => {
      clearTimeout(timer);
      const delay = brandRefetchDelay(validUntil, Date.now());
      if (delay === null) return;
      timer = setTimeout(() => {
        refreshBrand();
        arm();
      }, delay);
    };
    const onVisible = () => {
      if (document.visibilityState !== "visible") return;
      if (Date.parse(validUntil) <= Date.now()) refreshBrand();
      arm();
    };
    arm();
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      clearTimeout(timer);
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, [fetched, userId, sessionless]);

  useEffect(() => {
    applyFavicon(payload.logos.icon);
    document.title = payload.title;
  }, [payload.logos.icon, payload.title]);

  const brand = useMemo(() => brandOf(payload), [payload]);
  return (
    <DomainBrandContext.Provider value={setDomainBrand}>
      <BrandContext.Provider value={brand}>{children}</BrandContext.Provider>
    </DomainBrandContext.Provider>
  );
}

/** The brand: the title, logos and support contacts the shell shows (B14). Vantigo's own outside a provider. */
export function useBrand(): Brand {
  const brand = useContext(BrandContext);
  return useMemo(() => brand ?? brandOf(fallbackPayload()), [brand]);
}

/** The Mantine theme of the brand in scope. */
export function useBrandTheme() {
  const brand = useBrand();
  return useMemo(() => createVantigoTheme({ tokens: brand.tokens as BrandTokens }), [brand.tokens]);
}

/**
 * The MantineProvider with the brand's theme (brands design B14): the host's one provider. It is
 * inside a `BrandProvider` and re-themes when the brand changes.
 */
export function BrandedMantineProvider(props: Omit<MantineProviderProps, "theme">) {
  const theme = useBrandTheme();
  return <MantineProvider cssVariablesResolver={vantigoCssVariablesResolver} {...props} theme={theme} />;
}

/** The brand mark for the current colour scheme, ready for an `<img>`. */
export function useBrandMark(): BrandMark {
  const brand = useBrand();
  const scheme = useComputedColorScheme("light");
  return brand.markFor(scheme);
}
