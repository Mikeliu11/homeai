import React from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router-dom";
import App from "./App";
import { loadConfig } from "./config";
import { configureAuth } from "./auth";
import "./styles.css";

const root = createRoot(document.getElementById("root")!);
loadConfig()
  .then(() => {
    const authEnabled = configureAuth();
    root.render(
      <React.StrictMode>
        <BrowserRouter>
          <App authEnabled={authEnabled} />
        </BrowserRouter>
      </React.StrictMode>,
    );
  })
  .catch((e) => {
    root.render(<div className="app"><h1>Helper Pay</h1><p className="error">{String(e)}</p></div>);
  });
