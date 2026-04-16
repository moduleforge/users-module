'use client';

import { Suspense, useEffect, useState } from 'react';
import Link from 'next/link';
import { useRouter, useSearchParams } from 'next/navigation';
import { useAuth } from '@/lib/auth-context';
import {
  ApiRequestError,
  fetchProviders,
  type OIDCProvider,
} from '@/lib/api';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from '@/components/ui/card';
import { ErrorMessage } from '@/components/error-message';

// Inline brand glyphs keep the bundle small and avoid pulling in an icon
// package for two logos. Colors are the brand-correct Google/Microsoft marks.
function GoogleIcon() {
  return (
    <svg
      aria-hidden="true"
      viewBox="0 0 24 24"
      className="size-4"
    >
      <path
        fill="#EA4335"
        d="M12 10.2v3.9h5.5c-.24 1.43-1.7 4.2-5.5 4.2-3.3 0-6-2.73-6-6.1s2.7-6.1 6-6.1c1.88 0 3.14.8 3.86 1.48l2.63-2.53C16.73 3.52 14.57 2.6 12 2.6 6.98 2.6 2.9 6.68 2.9 11.7S6.98 20.8 12 20.8c6.93 0 9.3-4.87 9.3-7.82 0-.53-.06-.93-.13-1.33H12z"
      />
      <path
        fill="#34A853"
        d="M3.88 7.52l3.2 2.35c.87-1.65 2.43-2.77 4.92-2.77 1.88 0 3.14.8 3.86 1.48l2.63-2.53C16.73 3.52 14.57 2.6 12 2.6 8.16 2.6 4.87 4.94 3.88 7.52z"
      />
      <path
        fill="#FBBC05"
        d="M12 20.8c2.52 0 4.63-.83 6.17-2.25l-2.97-2.3c-.82.56-1.91.95-3.2.95-2.46 0-4.55-1.64-5.3-3.88l-3.14 2.42C5.52 18.64 8.53 20.8 12 20.8z"
      />
      <path
        fill="#4285F4"
        d="M21.3 11.65c0-.53-.06-.93-.13-1.33H12v3.9h5.5c-.12.72-.76 1.97-2.23 2.88l2.97 2.3c1.75-1.62 3.06-4.01 3.06-7.75z"
      />
    </svg>
  );
}

function MicrosoftIcon() {
  return (
    <svg aria-hidden="true" viewBox="0 0 24 24" className="size-4">
      <path fill="#F25022" d="M3 3h8.5v8.5H3z" />
      <path fill="#7FBA00" d="M12.5 3H21v8.5h-8.5z" />
      <path fill="#00A4EF" d="M3 12.5h8.5V21H3z" />
      <path fill="#FFB900" d="M12.5 12.5H21V21h-8.5z" />
    </svg>
  );
}

function providerIcon(id: string) {
  switch (id) {
    case 'google':
      return <GoogleIcon />;
    case 'microsoft':
      return <MicrosoftIcon />;
    default:
      return null;
  }
}

function LoginPageInner() {
  const { login } = useAuth();
  const router = useRouter();
  const searchParams = useSearchParams();
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState<string | null>(
    searchParams.get('error'),
  );
  const [isSubmitting, setIsSubmitting] = useState(false);
  // `null` = still loading; `[]` = loaded, none configured; otherwise the
  // list. We only render the provider section once the fetch resolves so
  // there's no flash of empty space.
  const [providers, setProviders] = useState<OIDCProvider[] | null>(null);

  useEffect(() => {
    let cancelled = false;
    fetchProviders().then((list) => {
      if (!cancelled) setProviders(list);
    });
    return () => {
      cancelled = true;
    };
  }, []);

  function handleProviderClick(providerId: string) {
    // Full page navigation — NOT a fetch. The API responds with a 302 to the
    // provider's authorization endpoint, and the `return` param is echoed
    // back through the OAuth round-trip.
    window.location.assign(
      `/v1/auth/oidc/${encodeURIComponent(providerId)}/start?return=/profile`,
    );
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setIsSubmitting(true);
    try {
      await login(email, password);
      router.push('/profile');
    } catch (err) {
      if (err instanceof ApiRequestError) {
        setError(err.message);
      } else {
        console.error('[login]', err);
        setError('Something went wrong. Check the browser console for details.');
      }
    } finally {
      setIsSubmitting(false);
    }
  }

  return (
    <div className="flex min-h-full items-center justify-center p-6">
      <Card className="w-full max-w-sm">
        <CardHeader>
          <CardTitle>Sign in</CardTitle>
          <CardDescription>Enter your credentials to continue</CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={handleSubmit} className="flex flex-col gap-4">
            <ErrorMessage message={error} />
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="email">Email</Label>
              <Input
                id="email"
                type="email"
                autoComplete="email"
                required
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                placeholder="you@example.com"
              />
            </div>
            <div className="flex flex-col gap-1.5">
              <div className="flex items-center justify-between">
                <Label htmlFor="password">Password</Label>
                <Link
                  href="/auth/forgot-password"
                  className="text-xs text-muted-foreground hover:text-foreground"
                >
                  Forgot password?
                </Link>
              </div>
              <Input
                id="password"
                type="password"
                autoComplete="current-password"
                required
                value={password}
                onChange={(e) => setPassword(e.target.value)}
              />
            </div>
            <Button type="submit" className="w-full" disabled={isSubmitting}>
              {isSubmitting ? 'Signing in...' : 'Sign in'}
            </Button>
          </form>
          {providers && providers.length > 0 && (
            <div className="mt-6 flex flex-col gap-3">
              <div
                role="separator"
                aria-label="or continue with"
                className="relative flex items-center text-xs text-muted-foreground"
              >
                <span className="flex-1 border-t border-border" />
                <span className="px-2">or continue with</span>
                <span className="flex-1 border-t border-border" />
              </div>
              {providers.map((p) => (
                <Button
                  key={p.id}
                  type="button"
                  variant="outline"
                  className="w-full"
                  onClick={() => handleProviderClick(p.id)}
                >
                  {providerIcon(p.id)}
                  <span>Sign in with {p.display_name}</span>
                </Button>
              ))}
            </div>
          )}
        </CardContent>
        <CardFooter className="flex flex-col gap-2 text-sm text-center">
          <Link
            href="/auth/email-code"
            className="text-muted-foreground hover:text-foreground"
          >
            Sign in with email code instead
          </Link>
          <p className="text-muted-foreground">
            No account?{' '}
            <Link href="/auth/register" className="text-foreground hover:underline">
              Create one
            </Link>
          </p>
        </CardFooter>
      </Card>
    </div>
  );
}

export default function LoginPage() {
  return (
    <Suspense
      fallback={
        <div className="flex min-h-full items-center justify-center p-6 text-sm text-muted-foreground">
          Loading...
        </div>
      }
    >
      <LoginPageInner />
    </Suspense>
  );
}
