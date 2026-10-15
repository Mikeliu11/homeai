import { Component, type ReactNode } from "react";

export class ErrorBoundary extends Component<{ children: ReactNode }, { error?: Error }> {
  state: { error?: Error } = {};
  static getDerivedStateFromError(error: Error) { return { error }; }
  render() {
    if (this.state.error) {
      return (
        <div className="card stack" style={{ marginTop: 24 }}>
          <strong>Something went wrong on this page.</strong>
          <div className="small muted">Nothing was changed. Reload to try again; if it keeps happening, the details below help whoever maintains the app.</div>
          <pre className="small" style={{ whiteSpace: "pre-wrap" }}>{String(this.state.error.message)}</pre>
          <div className="row"><button className="primary" onClick={() => location.reload()}>Reload</button><a className="btn" href="/home">Back to Home</a></div>
        </div>
      );
    }
    return this.props.children;
  }
}
