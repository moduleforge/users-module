'use client';

import { useEffect, useState } from 'react';
import Link from 'next/link';
import { CheckCircle2 } from 'lucide-react';
import { ApiRequestError } from '@/lib/api';
import {
  fetchOIDCSaved,
  fetchOIDCStatus,
  postOIDCConfirm,
  type OIDCProviderStatus,
  type OIDCStatus,
} from '@/lib/oidc-config';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from '@/components/ui/card';
import { ErrorMessage } from '@/components/error-message';

const REDIRECT_DELAY_MS = 2000;

/**
 * `/oidc-config` setup page. Entirely client-rendered.
 *
 * Flow:
 *   1. Fetch status on mount.
 *   2. If already confirmed → "Setup complete" card + link to /auth/login.
 *   3. Otherwise render setup form: token input, provider toggles (one
 *      Switch per provider), Revert + Confirm buttons, inline errors.
 */
export default function OIDCConfigPage() {
  const [status, setStatus] = useState<OIDCStatus | null>(null);
  const [statusError, setStatusError] = useState<string | null>(null);

  const [setupToken, setSetupToken] = useState('');
  // Per-provider enabled map. Keyed by provider id. Seeded from status
  // on first fetch; mutated by the Switch toggles and by Revert.
  const [toggles, setToggles] = useState<Record<string, boolean>>({});
  const [formError, setFormError] = useState<string | null>(null);
  const [revertMessage, setRevertMessage] = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [isReverting, setIsReverting] = useState(false);
  const [isSuccess, setIsSuccess] = useState(false);

  // Initial status fetch.
  useEffect(() => {
    let cancelled = false;
    fetchOIDCStatus()
      .then((s) => {
        if (cancelled) return;
        setStatus(s);
        // Seed toggles from the server's reported enabled state so the
        // form reflects current runtime config on first render.
        setToggles(
          Object.fromEntries(s.providers.map((p) => [p.id, p.enabled])),
        );
      })
      .catch((err: unknown) => {
        if (cancelled) return;
        if (err instanceof ApiRequestError) {
          setStatusError(err.message);
        } else {
          setStatusError('Could not load configuration status.');
        }
      });
    return () => {
      cancelled = true;
    };
  }, []);

  function handleToggle(providerId: string, next: boolean) {
    setToggles((prev) => ({ ...prev, [providerId]: next }));
    // Clear any stale revert hint once the user starts editing again.
    setRevertMessage(null);
  }

  async function handleRevert() {
    setFormError(null);
    setRevertMessage(null);
    setIsReverting(true);
    try {
      const saved = await fetchOIDCSaved();
      const enabled = saved.enabled_providers;
      if (!enabled || Object.keys(enabled).length === 0) {
        setRevertMessage('No saved config to revert to.');
        return;
      }
      // Preserve the shape (keys) of the current toggle map: for each
      // known provider, apply the saved value if present, otherwise
      // default to false. This keeps rendering stable even if the
      // saved config predates a newly-added provider.
      setToggles((prev) => {
        const next: Record<string, boolean> = {};
        for (const id of Object.keys(prev)) {
          next[id] = enabled[id] === true;
        }
        return next;
      });
      setRevertMessage('Reverted to last saved configuration.');
    } catch (err) {
      if (err instanceof ApiRequestError) {
        setFormError(err.message);
      } else {
        setFormError('Could not load saved configuration.');
      }
    } finally {
      setIsReverting(false);
    }
  }

  async function handleConfirm(e: React.FormEvent) {
    e.preventDefault();
    setFormError(null);
    setRevertMessage(null);
    setIsSubmitting(true);

    // Only include providers that are BOTH toggled on AND actually
    // configured in the environment. Toggling on an un-configured
    // provider is already prevented by the disabled Switch, but we
    // belt-and-suspender here so a stale toggle key can't sneak in.
    const configuredIds = new Set(
      (status?.providers ?? []).filter((p) => p.configured).map((p) => p.id),
    );
    const enabledProviders = Object.entries(toggles)
      .filter(([id, on]) => on && configuredIds.has(id))
      .map(([id]) => id);

    // Empty selection == opt-out (per plan §9.9b).
    const optOut = enabledProviders.length === 0;

    try {
      const updated = await postOIDCConfirm({
        setup_token: setupToken.trim(),
        enabled_providers: enabledProviders,
        opt_out: optOut,
      });
      setStatus(updated);
      // Clear the setup token from component state; it's single-use and
      // there's no reason to keep it around after a successful confirm.
      setSetupToken('');
      setIsSuccess(true);
      // Brief success UI, then hand off to the login page. We use a hard
      // navigation (not router.replace) so ClientLayout remounts and
      // re-fetches oidc status — otherwise its cached `needs-setup`
      // state bounces the user right back here.
      setTimeout(() => {
        window.location.assign('/auth/login');
      }, REDIRECT_DELAY_MS);
    } catch (err) {
      if (err instanceof ApiRequestError) {
        setFormError(err.message);
      } else {
        console.error('[oidc-config]', err);
        setFormError('Something went wrong. Check the browser console.');
      }
    } finally {
      setIsSubmitting(false);
    }
  }

  // ─── Rendering branches ────────────────────────────────────────────────

  if (statusError) {
    return (
      <PageShell>
        <Card className="w-full max-w-lg">
          <CardHeader>
            <CardTitle>OIDC configuration</CardTitle>
            <CardDescription>Could not load status.</CardDescription>
          </CardHeader>
          <CardContent>
            <ErrorMessage message={statusError} />
          </CardContent>
        </Card>
      </PageShell>
    );
  }

  if (status === null) {
    return (
      <PageShell>
        <p className="text-sm text-muted-foreground">Loading status...</p>
      </PageShell>
    );
  }

  if (status.confirmed) {
    return (
      <PageShell>
        <Card className="w-full max-w-lg">
          <CardHeader>
            <div className="flex items-center gap-2">
              <CheckCircle2 className="size-5 text-primary" />
              <CardTitle>Setup already complete</CardTitle>
            </div>
            <CardDescription>
              OIDC configuration has been confirmed. Head to the login page to
              sign in.
            </CardDescription>
          </CardHeader>
          <CardFooter>
            <Link href="/auth/login" className="text-sm underline">
              Go to login
            </Link>
          </CardFooter>
        </Card>
      </PageShell>
    );
  }

  if (isSuccess) {
    return (
      <PageShell>
        <Card className="w-full max-w-lg">
          <CardHeader>
            <div className="flex items-center gap-2">
              <CheckCircle2 className="size-5 text-primary" />
              <CardTitle>Configuration saved</CardTitle>
            </div>
            <CardDescription>
              Redirecting to the login page...
            </CardDescription>
          </CardHeader>
        </Card>
      </PageShell>
    );
  }

  return (
    <PageShell>
      <Card className="w-full max-w-lg">
        <CardHeader>
          <CardTitle>OIDC configuration</CardTitle>
          <CardDescription>
            Paste the setup token from the server logs and choose which
            providers to enable.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={handleConfirm} className="flex flex-col gap-5">
            <ErrorMessage message={formError} />

            <div className="flex flex-col gap-1.5">
              <Label htmlFor="setup-token">Setup token</Label>
              <Input
                id="setup-token"
                type="text"
                required
                autoComplete="off"
                spellCheck={false}
                value={setupToken}
                onChange={(e) => setSetupToken(e.target.value)}
                placeholder="hex token from server logs"
              />
            </div>

            <ProviderList
              providers={status.providers}
              toggles={toggles}
              onToggle={handleToggle}
            />

            {revertMessage && (
              <p className="text-xs text-muted-foreground">{revertMessage}</p>
            )}

            <div className="flex items-center justify-between gap-2 pt-1">
              <Button
                type="button"
                variant="outline"
                onClick={handleRevert}
                disabled={isReverting || isSubmitting}
              >
                {isReverting ? 'Reverting...' : 'Revert'}
              </Button>
              <Button
                type="submit"
                disabled={isSubmitting || setupToken.trim() === ''}
              >
                {isSubmitting ? 'Confirming...' : 'Confirm'}
              </Button>
            </div>
          </form>
        </CardContent>
        <CardFooter>
          <p className="text-xs text-muted-foreground">
            Turning every provider off records an opt-out; only local auth
            will be available.
          </p>
        </CardFooter>
      </Card>
    </PageShell>
  );
}

function PageShell({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex min-h-screen items-center justify-center p-6">
      {children}
    </div>
  );
}

interface ProviderListProps {
  providers: OIDCProviderStatus[];
  toggles: Record<string, boolean>;
  onToggle: (id: string, next: boolean) => void;
}

function ProviderList({ providers, toggles, onToggle }: ProviderListProps) {
  if (providers.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        No providers registered. Set provider env vars and restart the API.
      </p>
    );
  }
  return (
    <div className="flex flex-col gap-3">
      <Label>Providers</Label>
      <div className="flex flex-col gap-2 rounded-md border">
        {providers.map((p) => (
          <ProviderRow
            key={p.id}
            provider={p}
            checked={toggles[p.id] ?? false}
            onToggle={(next) => onToggle(p.id, next)}
          />
        ))}
      </div>
    </div>
  );
}

interface ProviderRowProps {
  provider: OIDCProviderStatus;
  checked: boolean;
  onToggle: (next: boolean) => void;
}

function ProviderRow({ provider, checked, onToggle }: ProviderRowProps) {
  const disabled = !provider.configured;
  const switchId = `provider-${provider.id}`;
  return (
    <div className="flex flex-col gap-1 px-3 py-2.5 [&:not(:last-child)]:border-b">
      <div className="flex items-center justify-between gap-3">
        <Label htmlFor={switchId} className="text-sm font-medium">
          {provider.display_name}
        </Label>
        <Switch
          id={switchId}
          checked={checked}
          onCheckedChange={onToggle}
          disabled={disabled}
        />
      </div>
      {!provider.configured && (
        <p className="text-xs text-muted-foreground">
          Env vars not set — edit <code>.env</code> and restart to enable.
        </p>
      )}
      {provider.configured && !provider.init_ok && provider.error && (
        <p className="text-xs text-destructive">
          Init error: {provider.error}
        </p>
      )}
    </div>
  );
}
