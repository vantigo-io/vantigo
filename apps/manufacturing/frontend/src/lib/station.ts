import { createContext, useContext } from "react";

/**
 * Whether the shop floor is rendered on a shared station (manufacturing phase
 * 2 M135, M140) rather than in a person's browser. On a station every request
 * is one of the operations open to stations, so what reads elsewhere —
 * a site's locations, an order's lots, a photo or a deviation's page — is left
 * out, and nothing links away from the station's page.
 */
export const StationContext = createContext(false);

/** Whether the component renders on a shared station. */
export const useAtStation = () => useContext(StationContext);
