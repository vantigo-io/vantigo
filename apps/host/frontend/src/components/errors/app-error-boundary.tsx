import { i18n } from "@vantigo/frontend-shell";
import { Component, type CSSProperties, type ErrorInfo, type ReactNode } from "react";
import "../../i18n";

// The host catalog is registered by the i18n import above; this reads it
// through the instance because no React provider exists at this level.
const text = (key: string) => String(i18n.t(key, { ns: "host" }));

const pageStyle: CSSProperties = {
  minHeight: "100vh",
  display: "flex",
  alignItems: "center",
  justifyContent: "center",
  padding: 24,
  fontFamily: "system-ui, -apple-system, 'Segoe UI', sans-serif",
  color: "#1f2937",
  background: "#f8fafc",
};

const cardStyle: CSSProperties = { maxWidth: 480, width: "100%", textAlign: "center" };

const buttonStyle: CSSProperties = {
  marginTop: 16,
  padding: "8px 16px",
  border: "1px solid #cbd5e1",
  borderRadius: 6,
  background: "#fff",
  cursor: "pointer",
  font: "inherit",
};

const detailsStyle: CSSProperties = {
  marginTop: 24,
  textAlign: "left",
  fontSize: 12,
  whiteSpace: "pre-wrap",
  overflowX: "auto",
  background: "#f1f5f9",
  padding: 12,
  borderRadius: 6,
};

/**
 * The last-resort page: plain HTML with inline styles, because it renders
 * when a provider above the router has crashed, so neither Mantine nor the
 * i18n provider can be assumed. Reloading is the only recovery on offer.
 */
export const AppCrashFallback = ({ error }: { error: unknown }) => (
  <div role="alert" style={pageStyle}>
    <div style={cardStyle}>
      <h1 style={{ fontSize: 24, margin: 0 }}>{text("unexpectedErrorTitle")}</h1>
      <p style={{ marginTop: 8, color: "#475569" }}>{text("appCrashBody")}</p>
      <button type="button" style={buttonStyle} onClick={() => window.location.reload()}>
        {text("errorReload")}
      </button>
      {import.meta.env.DEV && error instanceof Error && <pre style={detailsStyle}>{error.stack ?? error.message}</pre>}
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
