import { Box, Image } from "@mantine/core";
import { useCallback, useEffect, useRef, useState } from "react";
import { appUrl } from "../app-config";
import { parsePayload, useBrand, useBrandMark, useSetDomainBrand } from "./provider";
import type { BrandPayload } from "./tokens";

/**
 * The sign-in page's fade into the brand of the typed address's domain (brands design B26). The page
 * starts in the stylesheet's brand; once the e-mail has a valid-looking domain and has been still for
 * `DOMAIN_BRAND_IDLE_MS`, or the field is blurred, one request per distinct domain asks which brand an
 * admin attached to it. Only the domain is ever sent, never the local part (B25), and nothing is
 * remembered across people: the answers live in memory for the page, and the pre-paint mirror is untouched.
 */
export const DOMAIN_BRAND_IDLE_MS = 400;
/** How long the colours, wordmark and title take to change; theme.css carries the same time. */
export const DOMAIN_BRAND_FADE_MS = 300;

/** The attribute on the root that switches the fade's transitions on (theme.css). */
const FADE_ATTRIBUTE = "data-vantigo-brand-fade";

/** What follows the last @ of a typed address, lower case and without a trailing dot; null without an @. */
export function domainOfEmail(email: string): string | null {
  const at = email.lastIndexOf("@");
  if (at < 0) return null;
  return email
    .slice(at + 1)
    .trim()
    .toLowerCase()
    .replace(/\.$/, "");
}

/**
 * Whether a typed domain looks like one (B25): dotted labels of letters, digits and hyphens, none starting
 * or ending in a hyphen, the last of two or more letters. The server checks the same; this keeps partial
 * typing from firing requests.
 */
export function looksLikeDomain(domain: string): boolean {
  if (domain.length > 253 || !domain.includes(".")) return false;
  const labels = domain.split(".");
  if (!labels.every((l) => /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/.test(l))) return false;
  const tld = labels[labels.length - 1] as string;
  return /^[a-z]{2,}$/.test(tld) || /^xn--[a-z0-9-]+$/.test(tld);
}

const reducedMotion = () =>
  typeof window.matchMedia === "function" && window.matchMedia("(prefers-reduced-motion: reduce)").matches;

let fadeTimer: number | undefined;

/** Runs `change` with the fade switched on for a moment; with reduced motion the change is instant. */
function withFade(change: () => void): void {
  const root = document.documentElement;
  if (!reducedMotion()) {
    root.setAttribute(FADE_ATTRIBUTE, "");
    window.clearTimeout(fadeTimer);
    fadeTimer = window.setTimeout(() => root.removeAttribute(FADE_ATTRIBUTE), DOMAIN_BRAND_FADE_MS + 100);
  }
  change();
}

/** The brand attached to `domain` (null for the default), or undefined when the lookup failed. */
async function lookUp(domain: string): Promise<BrandPayload | null | undefined> {
  const res = await fetch(appUrl(`/api/v1/identity/brand/public?domain=${encodeURIComponent(domain)}`), {
    headers: { Accept: "application/json" },
    credentials: "omit",
  });
  if (!res.ok) return undefined; // 429 or an error: keep the brand the page has
  const body = await res.json();
  const payload = parsePayload(body);
  return payload && (body as { matched?: boolean }).matched === true ? payload : null;
}

/**
 * Fades the page into the brand of `email`'s domain and back (B26). Returns the `onBlur` for the e-mail
 * field, which asks at once instead of waiting for the pause. The fade is cosmetic: a failed lookup leaves
 * the page as it is, and nothing here gates signing in.
 */
export function useDomainBrand(email: string): { onBlur: () => void } {
  const setDomainBrand = useSetDomainBrand();
  const [settled, setSettled] = useState(email);
  const latest = useRef(email);
  latest.current = email;
  const answers = useRef(new Map<string, Promise<BrandPayload | null | undefined>>());
  const shown = useRef<string | null>(null);

  useEffect(() => {
    const timer = window.setTimeout(() => setSettled(email), DOMAIN_BRAND_IDLE_MS);
    return () => window.clearTimeout(timer);
  }, [email]);
  const onBlur = useCallback(() => setSettled(latest.current), []);

  const typed = domainOfEmail(settled);
  const domain = typed && looksLikeDomain(typed) ? typed : null;

  useEffect(() => {
    let cancelled = false;
    const show = (payload: BrandPayload | null) => {
      const key = payload ? payload.version : null;
      if (cancelled || key === shown.current) return;
      shown.current = key;
      withFade(() => setDomainBrand(payload));
    };
    if (!domain) {
      show(null);
      return () => {
        cancelled = true;
      };
    }
    let answer = answers.current.get(domain);
    if (!answer) {
      answer = lookUp(domain).catch(() => undefined);
      answers.current.set(domain, answer);
      void answer.then((a) => {
        if (a === undefined) answers.current.delete(domain); // ask again next time
      });
    }
    void answer.then((payload) => {
      if (payload !== undefined) show(payload);
    });
    return () => {
      cancelled = true;
    };
  }, [domain, setDomainBrand]);

  // Leaving the page drops the brand: the next page, and the next person, start in the stylesheet's.
  useEffect(
    () => () => {
      document.documentElement.removeAttribute(FADE_ATTRIBUTE);
      setDomainBrand(null);
    },
    [setDomainBrand],
  );

  return { onBlur };
}

/**
 * The brand's mark above a public page's form: the wordmark when it has one, else the mark for the colour
 * scheme. Its box keeps one height whatever the logo, so the page does not move when the brand changes, and a
 * new logo fades in (theme.css).
 */
export function AuthBrandMark() {
  const brand = useBrand();
  const mark = useBrandMark();
  const src = brand.logos.wordmark ?? mark.src;
  const chip = !brand.logos.wordmark && mark.chip;
  return (
    <Box h={48} style={{ display: "flex", justifyContent: "center", alignItems: "center" }}>
      <Box bg={chip ? "white" : undefined} p={chip ? 2 : 0} style={{ borderRadius: 4, lineHeight: 0 }}>
        <Image
          key={src}
          className="vantigo-brand-mark"
          src={src}
          alt={brand.name}
          h={chip ? 44 : 48}
          w="auto"
          maw="60vw"
          fit="contain"
        />
      </Box>
    </Box>
  );
}
