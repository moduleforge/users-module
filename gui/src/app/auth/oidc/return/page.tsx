'use client';

import { useEffect, useRef, useState } from 'react';
import { useRouter } from 'next/navigation';
import { useAuth } from '@/lib/auth-context';
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card';

/**
 * Default destination when the OAuth flow completes without a (safe) return
 * path. Keep in sync with the default passed to `/v1/auth/oidc/{id}/start`
 * from the login page.
 */
const DEFAULT_RETURN = '/profile';

/**
 * Validates that a candidate return path is a safe, same-origin relative
 * path. Rejects absolute URLs, protocol-relative URLs (`//evil.com`), and
 * any string attempting to embed a scheme before the first slash.
 */
function isSafeReturnPath(candidate: string | null): candidate is string {
  if (!candidate) return false;
  // Must start with exactly one '/'.
  if (!candidate.startsWith('/')) return false;
  // Reject protocol-relative paths like `//evil.com/foo`.
  if (candidate.startsWith('//')) return false;
  // Reject anything trying to sneak a scheme in before the path separator,
  // e.g. `/\x0Ajavascript:...` variants or `javascript:...`. Since we already
  // require a leading `/`, a `:` anywhere in the first segment is suspicious.
  const firstSlashAfterStart = candidate.indexOf('/', 1);
  const firstSegment =
    firstSlashAfterStart === -1
      ? candidate.slice(1)
      : candidate.slice(1, firstSlashAfterStart);
  if (firstSegment.includes(':')) return false;
  return true;
}

/**
 * Minimal structural JWT check — three non-empty base64url segments. Full
 * cryptographic validation happens server-side when we exchange the token
 * for the `/v1/self` response.
 */
function looksLikeJwt(token: string): boolean {
  const parts = token.split('.');
  if (parts.length !== 3) return false;
  const b64url = /^[A-Za-z0-9_-]+$/;
  return parts.every((p) => p.length > 0 && b64url.test(p));
}

function OidcReturnPage() {
  const router = useRouter();
  const { completeExternalLogin } = useAuth();
  const [message, setMessage] = useState<string>('Signing you in...');
  // Guard against React 19 StrictMode's double-invoke and any other
  // accidental re-run of the effect — we must only consume the token once.
  const hasProcessed = useRef(false);

  useEffect(() => {
    if (hasProcessed.current) return;
    hasProcessed.current = true;

    async function run() {
      // Error path: server redirected with ?error=... in the query string.
      const query = new URLSearchParams(window.location.search);
      const errorParam = query.get('error');
      if (errorParam) {
        router.replace(
          `/auth/login?error=${encodeURIComponent(errorParam)}`,
        );
        return;
      }

      // Success path: token + return come back in the fragment so they are
      // never sent to the server in access logs.
      const rawHash = window.location.hash.startsWith('#')
        ? window.location.hash.slice(1)
        : window.location.hash;
      const hashParams = new URLSearchParams(rawHash);
      const token = hashParams.get('token');
      const returnCandidate = hashParams.get('return');

      if (!token || !looksLikeJwt(token)) {
        router.replace(
          `/auth/login?error=${encodeURIComponent(
            'Sign-in failed: malformed response from authentication provider.',
          )}`,
        );
        return;
      }

      // Remove the hash from the URL so the token does not linger in the
      // browser history. Must happen before any redirect to the return path.
      try {
        window.history.replaceState(
          null,
          '',
          window.location.pathname,
        );
      } catch {
        // history API may be unavailable in test environments — ignore.
      }

      const returnPath = isSafeReturnPath(returnCandidate)
        ? returnCandidate
        : DEFAULT_RETURN;

      try {
        await completeExternalLogin(token);
      } catch (err) {
        console.error('[oauth-return] completeExternalLogin failed', err);
        setMessage('Sign-in failed. Redirecting...');
        router.replace(
          `/auth/login?error=${encodeURIComponent(
            'Sign-in failed: your session could not be established.',
          )}`,
        );
        return;
      }

      router.replace(returnPath);
    }

    void run();
  }, [router, completeExternalLogin]);

  return (
    <div className="flex min-h-full items-center justify-center p-6">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle>Signing you in</CardTitle>
          <CardDescription>{message}</CardDescription>
        </CardHeader>
        <CardContent>
          <p className="text-sm text-muted-foreground">
            One moment while we finish setting up your session.
          </p>
        </CardContent>
      </Card>
    </div>
  );
}

export default OidcReturnPage;
