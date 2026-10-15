import { Amplify } from "aws-amplify";
import { fetchAuthSession, signOut as amplifySignOut } from "aws-amplify/auth";
import { Authenticator } from "@aws-amplify/ui-react";
import "@aws-amplify/ui-react/styles.css";
import type { ReactNode } from "react";
import { config } from "./config";
import { setTokenProvider } from "./api";

export function configureAuth() {
  const c = config();
  if (c.local || !c.userPoolId || !c.userPoolClientId) {
    setTokenProvider(async () => null);
    return false;
  }
  Amplify.configure({ Auth: { Cognito: { userPoolId: c.userPoolId, userPoolClientId: c.userPoolClientId } } });
  setTokenProvider(async () => {
    try {
      const s = await fetchAuthSession();
      return s.tokens?.idToken?.toString() ?? null;
    } catch {
      return null;
    }
  });
  return true;
}

export async function signOut() {
  if (config().local) return;
  await amplifySignOut();
}

export function AuthGate({ enabled, children }: { enabled: boolean; children: ReactNode }) {
  if (!enabled) return <>{children}</>;
  return (
    <Authenticator hideSignUp components={{ Header: () => <div className="auth-shell" style={{ margin: "0 auto 8px", textAlign: "center" }}><h1>Helper Pay</h1><p className="muted">Your household's helper payments, in one place.</p></div>, SignIn: { Footer: () => null } }}>
      {() => <>{children}</>}
    </Authenticator>
  );
}
