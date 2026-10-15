import { NavLink, Navigate, Route, Routes, useLocation } from "react-router-dom";
import { AuthGate, signOut } from "./auth";
import Helpers from "./pages/Helpers";
import HelperDetail from "./pages/HelperDetail";
import Home from "./pages/Home";
import Memories from "./pages/Memories";
import BillDetail from "./pages/BillDetail";
import Bills from "./pages/Bills";
import Settings from "./pages/Settings";
import Help from "./pages/Help";
import { ErrorBoundary } from "./components/ErrorBoundary";

export default function App({ authEnabled }: { authEnabled: boolean }) {
  const location = useLocation();
  return (
    <AuthGate enabled={authEnabled}>
      <div className="app">
        <div className="sheet">
        <header className="topbar">
          <div className="brand">Helper Pay</div>
          <nav>
            <NavLink to="/home">Home</NavLink>
            <NavLink to="/helpers">Helpers</NavLink>
            <NavLink to="/bills">Bills</NavLink>
            <NavLink to="/memories">Memories</NavLink>
            <NavLink to="/settings">Settings</NavLink>
            <NavLink to="/help">Help</NavLink>
          </nav>
          <div className="spacer" />
          {authEnabled ? <button className="link" onClick={() => signOut()}>Sign out</button> : <span className="badge">local</span>}
        </header>
        <ErrorBoundary>
        <div className="page" key={location.pathname}>
        <Routes>
          <Route path="/" element={<Navigate to="/home" replace />} />
          <Route path="/inbox" element={<Navigate to="/home" replace />} />
          <Route path="/home" element={<Home />} />
          <Route path="/memories" element={<Memories />} />
          <Route path="/helpers" element={<Helpers />} />
          <Route path="/helpers/:id" element={<HelperDetail />} />
          <Route path="/bills" element={<Bills />} />
          <Route path="/bills/:id" element={<BillDetail />} />
          <Route path="/settings" element={<Settings />} />
          <Route path="/help" element={<Help />} />
        </Routes>
        </div>
        </ErrorBoundary>
        </div>
      </div>
    </AuthGate>
  );
}
