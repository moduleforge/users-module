'use client';

import { useEffect, useMemo, useState } from 'react';
import { CheckCircle2 } from 'lucide-react';
import { ApiRequestError } from '@/lib/api';
import { useOptionalAuth } from '@/lib/auth-context';
import {
  fetchOIDCSaved,
  fetchOIDCStatus,
  postOIDCConfirm,
  type OIDCProviderStatus,
  type OIDCStatus,
} from '@/lib/oidc-config';
import { Badge } from '@/components/ui/badge';
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
 * `/oidc-config` — dual-mode setup + reconfigure page.
 *
 * Always renders the provider list + toggles + per-provider OK/Failed
 * status badges. Two authorization paths:
 *   - **Token mode**: no admin session. User pastes the setup token from
 *     the server logs. On successful confirm → hard-redirect to /auth/login.
 *   - **Admin mode**: AuthProvider is mounted and the user is an admin.
 *     Token input is hidden; submit sends Authorization: Bearer <jwt>.
 *     Success stays on the page and refreshes status — admin can tweak
 *     and re-confirm without another round trip.
 *
 * Partial-failure / strict confirmation: if the submitted config still
 * has a broken provider, the API returns 200 with `confirmed: false`. We
 * show that inline and do NOT redirect, giving the admin a chance to
 * disable the failing provider explicitly.
 */
export default function OIDCConfigPage() {
  const auth = useOptionalAuth();
  const isAdminMode = auth?.isAdmin === true && auth.token !== null;

  const [status, setStatus] = useState<OIDCStatus | null>(null);
  const [statusError, setStatusError] = useState<string | null>(null);

  const [setupToken, setSetupToken] = useState('');
  const [toggles, setToggles] = useState<Record<string, boolean>>({});
  const [formError, setFormError] = useState<string | null>(null);
  const [revertMessage, setRevertMessage] = useState<string | null>(null);
  const [successMessage, setSuccessMessage] = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [isReverting, setIsReverting] = useState(false);
  const [isTokenSuccess, setIsTokenSuccess] = useState(false);

  function applyStatus(s: OIDCStatus) {
    setStatus(s);
    setToggles(
      Object.fromEntries(s.providers.map((p) => [p.id, p.enabled])),
    );
  }

  // Initial status fetch.
  useEffect(() => {
    let cancelled = false;
    fetchOIDCStatus()
      .then((s) => {
        if (cancelled) return;
        applyStatus(s);
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
    setRevertMessage(null);
    setSuccessMessage(null);
  }

  async function handleRevert() {
    setFormError(null);
    setRevertMessage(null);
    setSuccessMessage(null);
    setIsReverting(true);
    try {
      const saved = await fetchOIDCSaved();
      const enabled = saved.enabled_providers;
      if (!enabled || Object.keys(enabled).length === 0) {
        setRevertMessage('No saved config to revert to.');
        return;
      }
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
    setSuccessMessage(null);
    setIsSubmitting(true);

    const configuredIds = new Set(
      (status?.providers ?? []).filter((p) => p.configured).map((p) => p.id),
    );
    const enabledProviders = Object.entries(toggles)
      .filter(([id, on]) => on && configuredIds.has(id))
      .map(([id]) => id);
    const optOut = enabledProviders.length === 0;

    try {
      const updated = isAdminMode
        ? await postOIDCConfirm({
            adminBearer: auth!.token!,
            enabled_providers: enabledProviders,
            opt_out: optOut,
          })
        : await postOIDCConfirm({
            setup_token: setupToken.trim(),
            enabled_providers: enabledProviders,
            opt_out: optOut,
          });

      // Strict confirmation (Phase 9.10a): the API may return 200 with
      // confirmed=false if an enabled provider still fails init. Don't
      // redirect; let the admin see the error list and try again.
      if (!updated.confirmed) {
        applyStatus(updated);
        const failing = updated.providers.filter(
          (p) => p.enabled && !p.init_ok,
        );
        if (failing.length > 0) {
          const details = failing
            .map((p) => `${p.display_name}: ${p.error ?? 'init failed'}`)
            .join('; ');
          setFormError(
            `Configuration saved but providers still fail to initialize: ${details}. Disable the failing providers or fix their env settings.`,
          );
        } else {
          setFormError(
            'Configuration saved but the system is not in a confirmed state. Check server logs.',
          );
        }
        return;
      }

      if (isAdminMode) {
        // Admin session stays valid; keep them on the page with an
        // inline success + refreshed status so they can see the new
        // badges and re-toggle if needed.
        applyStatus(updated);
        setSuccessMessage('Configuration saved.');
      } else {
        // Token flow: clear single-use token and hand off to login via a
        // hard navigation so ClientLayout remounts and re-fetches status.
        setSetupToken('');
        setIsTokenSuccess(true);
        setTimeout(() => {
          window.location.assign('/auth/login');
        }, REDIRECT_DELAY_MS);
      }
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

  if (isTokenSuccess) {
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
            {isAdminMode
              ? 'Toggle providers and confirm. Changes take effect immediately.'
              : 'Paste the setup token from the server logs and choose which providers to enable.'}
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={handleConfirm} className="flex flex-col gap-5">
            <ErrorMessage message={formError} />
            {successMessage && (
              <div className="flex items-center gap-2 rounded-md border border-primary/20 bg-primary/5 px-3 py-2 text-sm">
                <CheckCircle2 className="size-4 text-primary" />
                <span>{successMessage}</span>
              </div>
            )}

            {!isAdminMode && (
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
            )}

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
                disabled={
                  isSubmitting ||
                  (!isAdminMode && setupToken.trim() === '')
                }
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
  const statusBadge = useMemo(() => {
    if (!provider.configured) return null;
    if (provider.init_ok) {
      return <Badge variant="default">OK</Badge>;
    }
    return <Badge variant="destructive">Failed</Badge>;
  }, [provider.configured, provider.init_ok]);

  return (
    <div className="flex flex-col gap-1 px-3 py-2.5 [&:not(:last-child)]:border-b">
      <div className="flex items-center justify-between gap-3">
        <div className="flex items-center gap-2">
          <Label htmlFor={switchId} className="text-sm font-medium">
            {provider.display_name}
          </Label>
          {statusBadge}
        </div>
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
