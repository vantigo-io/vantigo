export interface VantigoConfig {
  siteUrl: string;
  sitePath: string;
}

export function getVantigoConfig(): VantigoConfig {
  const win = window as unknown as { __VANTIGO_CONFIG__?: VantigoConfig };
  if (typeof window !== "undefined" && win.__VANTIGO_CONFIG__) {
    return win.__VANTIGO_CONFIG__;
  }
  return {
    siteUrl:
      typeof window !== "undefined"
        ? window.location.origin
        : "http://localhost:3000",
    sitePath: "/idp",
  };
}
