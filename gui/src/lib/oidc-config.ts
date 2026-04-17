import { API_BASE_URL, ApiRequestError, type ApiErrorResponse } from '@/lib/api';

/**
 * OIDC onboarding API helpers. These endpoints are unauthenticated
 * (`/v1/oidc-config/*` is exempt from `RequireOIDCConfirmed` and does not
 * require a bearer token), so none of these helpers send an Authorization
 * header — mirroring the `fetchProviders()` pattern in `api.ts`.
 */

export type OIDCState =
  | 'confirmed_ok'
  | 'confirmed_opt_out'
  | 'init_failed'
  | 'no_env_no_flag';

export interface OIDCProviderStatus {
  id: string;
  display_name: string;
  configured: boolean;
  enabled: boolean;
  init_ok: boolean;
  error: string | null;
}

export interface OIDCStatus {
  state: OIDCState;
  confirmed: boolean;
  providers: OIDCProviderStatus[];
  no_oidc_accounts_env: boolean;
  needs_setup_token: boolean;
  /** Optional: the API returns this as an extra field; not always present. */
  saved_at?: string | null;
}

export interface OIDCConfirmRequest {
  setup_token: string;
  enabled_providers: string[];
  opt_out: boolean;
}

export interface OIDCSavedConfig {
  enabled_providers: Record<string, boolean> | null;
  opt_out: boolean;
  saved_at: string | null;
}

async function parseError(response: Response): Promise<ApiRequestError> {
  let code = 'unknown_error';
  let message = `Request failed with status ${response.status}`;
  try {
    const body = (await response.json()) as ApiErrorResponse;
    if (body.error) {
      code = body.error.code;
      message = body.error.message;
    }
  } catch {
    // ignore JSON parse errors — keep defaults
  }
  return new ApiRequestError(code, message, response.status);
}

/**
 * Fetches the current OIDC onboarding status. No auth header sent.
 *
 * Network failures are surfaced as an ApiRequestError with status=0 so the
 * caller can treat unreachable-API the same as unconfirmed (the plan calls
 * for routing to `/oidc-config` in that case).
 */
export async function fetchOIDCStatus(): Promise<OIDCStatus> {
  let response: Response;
  try {
    response = await fetch(`${API_BASE_URL}/v1/oidc-config/status`, {
      headers: { 'Content-Type': 'application/json' },
    });
  } catch (err) {
    console.error('[oidc-config] fetchOIDCStatus network error', err);
    throw new ApiRequestError(
      'network_error',
      'Could not reach the API server.',
      0,
    );
  }

  if (!response.ok) {
    throw await parseError(response);
  }

  return response.json() as Promise<OIDCStatus>;
}

/**
 * Submits the onboarding confirmation. On 200 the API returns the refreshed
 * status (same shape as GET /status). Errors (401 invalid token, 400 bad
 * body, 500 rebuild failure) surface as ApiRequestError so the page can
 * render `err.message` inline.
 */
export async function postOIDCConfirm(
  body: OIDCConfirmRequest,
): Promise<OIDCStatus> {
  let response: Response;
  try {
    response = await fetch(`${API_BASE_URL}/v1/oidc-config/confirm`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
  } catch (err) {
    console.error('[oidc-config] postOIDCConfirm network error', err);
    throw new ApiRequestError(
      'network_error',
      'Could not reach the API server.',
      0,
    );
  }

  if (!response.ok) {
    throw await parseError(response);
  }

  return response.json() as Promise<OIDCStatus>;
}

/**
 * Fetches the last-saved config to power the "Revert" button. Returns empty
 * / null fields when nothing has ever been saved.
 */
export async function fetchOIDCSaved(): Promise<OIDCSavedConfig> {
  let response: Response;
  try {
    response = await fetch(`${API_BASE_URL}/v1/oidc-config/saved`, {
      headers: { 'Content-Type': 'application/json' },
    });
  } catch (err) {
    console.error('[oidc-config] fetchOIDCSaved network error', err);
    throw new ApiRequestError(
      'network_error',
      'Could not reach the API server.',
      0,
    );
  }

  if (!response.ok) {
    throw await parseError(response);
  }

  return response.json() as Promise<OIDCSavedConfig>;
}
