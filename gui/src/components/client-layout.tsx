'use client';

import { useEffect, useState } from 'react';
import { usePathname, useRouter } from 'next/navigation';
import { AuthProvider } from '@/lib/auth-context';
import { SidebarNav } from '@/components/sidebar-nav';
import { fetchOIDCStatus } from '@/lib/oidc-config';

/**
 * Tri-state for the OIDC onboarding gate:
 * - `loading`: initial fetch in flight; render a minimal placeholder.
 * - `ok`:      OIDC is confirmed; proceed with the normal AuthProvider flow.
 * - `needs-setup`: API reports unconfirmed (or was unreachable); route to
 *   `/oidc-config` unless the user is already there.
 */
type OIDCReady = 'loading' | 'ok' | 'needs-setup';

const CONFIG_PATH = '/oidc-config';

function LoadingScreen() {
  return (
    <div className="flex h-screen items-center justify-center text-sm text-muted-foreground">
      Loading...
    </div>
  );
}

export function ClientLayout({ children }: { children: React.ReactNode }) {
  const pathname = usePathname();
  const router = useRouter();
  const [oidcReady, setOidcReady] = useState<OIDCReady>('loading');

  // Probe OIDC status exactly once on mount. Any failure (network error,
  // 5xx, invalid JSON) is treated as "needs setup" so an operator with a
  // broken API can still reach the config page to fix things.
  useEffect(() => {
    let cancelled = false;
    fetchOIDCStatus()
      .then((status) => {
        if (cancelled) return;
        setOidcReady(status.confirmed ? 'ok' : 'needs-setup');
      })
      .catch(() => {
        if (cancelled) return;
        setOidcReady('needs-setup');
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // Redirect to /oidc-config whenever the API says we're unconfirmed and
  // we're not already there. Runs on status change or route change.
  useEffect(() => {
    if (oidcReady === 'needs-setup' && pathname !== CONFIG_PATH) {
      router.replace(CONFIG_PATH);
    }
  }, [oidcReady, pathname, router]);

  if (oidcReady === 'loading') {
    return <LoadingScreen />;
  }

  // When unconfirmed, the only route we allow through is the setup page
  // itself. Everything else renders the loading screen while the redirect
  // above dispatches — this guarantees the AuthProvider never mounts and
  // therefore never fires `/v1/self` (which would 503 under unconfirmed).
  if (oidcReady === 'needs-setup') {
    if (pathname !== CONFIG_PATH) {
      return <LoadingScreen />;
    }
    // On the setup page itself, render children without an AuthProvider.
    // The page is intentionally unauthenticated; no consumer on that page
    // should be calling `useAuth()`.
    return <main className="h-screen overflow-y-auto">{children}</main>;
  }

  return (
    <AuthProvider>
      <div className="flex h-screen overflow-hidden">
        <SidebarNav />
        <main className="flex-1 overflow-y-auto">{children}</main>
      </div>
    </AuthProvider>
  );
}
