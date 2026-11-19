import { i18n } from "@vantigo/frontend-shell";
import { Component, type ErrorInfo, type ReactNode } from "react";
import "../../i18n";
import "./app-crash-fallback.css";

// The host catalog is registered by the i18n import above; this reads it
// through the instance because no React provider exists at this level.
const text = (key: string) => String(i18n.t(key, { ns: "host" }));

/**
 * The last-resort page: plain HTML with its own stylesheet, because it renders
 * when a provider above the router has crashed, so neither Mantine nor the
 * i18n provider can be assumed. Reloading is the only recovery on offer.
 */
export const AppCrashFallback = ({ error }: { error: unknown }) => (
  <div role="alert" className="crash-page">
    <div className="crash-card">
      <h1 className="crash-title">{text("unexpectedErrorTitle")}</h1>
      <p className="crash-body">{text("appCrashBody")}</p>
      <button type="button" className="crash-button" onClick={() => window.location.reload()}>
        {text("errorReload")}
      </button>
      {import.meta.env.DEV && error instanceof Error && (
        <pre className="crash-details">{error.stack ?? error.message}</pre>
      )}
    </div>
  </div>
);

interface State {
  crashed: boolean;
  error: unknown;
}

/**
 * The boundary around everything in main.tsx. Inside the router every
 * matched route already has its own catch boundary (the router's
 * defaultErrorComponent), so this only ever catches a crash in a provider
 * above the router, or in the error page itself — the cases that would
 * otherwise leave a blank page.
 */
export class AppErrorBoundary extends Component<{ children: ReactNode }, State> {
  state: State = { crashed: false, error: undefined };

  static getDerivedStateFromError(error: unknown): State {
    return { crashed: true, error };
  }

  componentDidCatch(error: unknown, info: ErrorInfo) {
    console.error("Unrecoverable error outside the router", error, info.componentStack);
  }

  render() {
    return this.state.crashed ? <AppCrashFallback error={this.state.error} /> : this.props.children;
  }
}
