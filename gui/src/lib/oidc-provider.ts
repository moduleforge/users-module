import { API_BASE_URL, ApiRequestError, type ApiErrorResponse } from '@/lib/api';

/**
 * Per-provider OIDC override CRUD helpers (phase 9.11b). These wrap the
 * /v1/oidc-config/providers/* endpoints added in 9.11a. Kept separate from
 * `oidc-config.ts` (which hosts the status/confirm/saved helpers) so the
 * onboarding flow and the per-provider edit flow evolve independently.
 *
 * Auth: every method accepts `auth` — either an admin bearer token (sent
 * as Authorization: Bearer) or a setup token. The setup token is sent
 * via the `X-Setup-Token` header on every method (covers GET and DELETE
 * which have no body). PUT and POST additionally echo it into the body
 * under `setup_token` for back-compat with the original shape, but the
 * header alone is sufficient. Mirrors the dual-auth pattern used by
 * {@link postOIDCConfirm}.
 */

export interface OIDCProviderView {
  id: string;
  display_name: string | null;
  display_name_default: string | null;
  issuer_url: string | null;
  issuer_url_default: string | null;
  client_id: string | null;
  client_id_default: string | null;
  has_client_secret: boolean;
  claim_style: string | null;
  claim_style_default: string | null;
  scopes: string[] | null;
  scopes_default: string[];
  enabled: boolean;
  init_ok: boolean;
  error?: string;
  callback_url: string;
  well_known: boolean;
}

/**
 * Fields that can be sent on a PUT or POST. Use these sentinels:
 *   - property absent  → keep existing DB value (for PUT) / don't set (POST).
 *   - property `null`  → clear the override (API falls back to env/default).
 *   - non-empty string → store as override.
 *
 * `client_secret` has the same semantics but must be omitted when you want
 * to preserve the existing secret — sending `null` or "" both clear it.
 */
export interface OIDCProviderWriteBody {
  display_name?: string | null;
  issuer_url?: string | null;
  client_id?: string | null;
  client_secret?: string | null;
  claim_style?: string | null;
  scopes?: string[] | null;
  enabled?: boolean;
}

export type OIDCProviderAuth =
  | { adminBearer: string; setupToken?: never }
  | { adminBearer?: never; setupToken: string };

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

function authHeaders(auth: OIDCProviderAuth): Record<string, string> {
  const headers: Record<string, string> = { 'Content-Type': 'application/json' };
  if ('adminBearer' in auth && auth.adminBearer) {
    headers.Authorization = `Bearer ${auth.adminBearer}`;
  } else if ('setupToken' in auth && auth.setupToken) {
    // PUT/POST also put the token in the JSON body, but sending it in
    // the header covers GET/DELETE (which have no body) with the same
    // auth surface. The API accepts either.
    headers['X-Setup-Token'] = auth.setupToken;
  }
  return headers;
}

/**
 * Attaches the setup_token to the body when not using admin-bearer auth.
 * Returns a new object so callers don't have to mutate their input.
 */
function bodyWithAuth(
  body: Record<string, unknown>,
  auth: OIDCProviderAuth,
): Record<string, unknown> {
  if ('setupToken' in auth && auth.setupToken) {
    return { ...body, setup_token: auth.setupToken };
  }
  return body;
}

/**
 * GET /v1/oidc-config/providers/:id. Accepts either admin bearer or a
 * setup token (sent as X-Setup-Token header since GET has no body).
 */
export async function fetchOIDCProvider(
  id: string,
  auth: OIDCProviderAuth,
): Promise<OIDCProviderView> {
  let response: Response;
  try {
    response = await fetch(
      `${API_BASE_URL}/v1/oidc-config/providers/${encodeURIComponent(id)}`,
      {
        method: 'GET',
        headers: authHeaders(auth),
      },
    );
  } catch (err) {
    console.error('[oidc-provider] fetchOIDCProvider network error', err);
    throw new ApiRequestError(
      'network_error',
      'Could not reach the API server.',
      0,
    );
  }
  if (!response.ok) {
    throw await parseError(response);
  }
  return response.json() as Promise<OIDCProviderView>;
}

/**
 * PUT /v1/oidc-config/providers/:id. Accepts admin bearer or setup token.
 */
export async function updateOIDCProvider(
  id: string,
  body: OIDCProviderWriteBody,
  auth: OIDCProviderAuth,
): Promise<OIDCProviderView> {
  let response: Response;
  try {
    response = await fetch(
      `${API_BASE_URL}/v1/oidc-config/providers/${encodeURIComponent(id)}`,
      {
        method: 'PUT',
        headers: authHeaders(auth),
        body: JSON.stringify(bodyWithAuth(body as Record<string, unknown>, auth)),
      },
    );
  } catch (err) {
    console.error('[oidc-provider] updateOIDCProvider network error', err);
    throw new ApiRequestError(
      'network_error',
      'Could not reach the API server.',
      0,
    );
  }
  if (!response.ok) {
    throw await parseError(response);
  }
  return response.json() as Promise<OIDCProviderView>;
}

/**
 * POST /v1/oidc-config/providers. Body shape is the write-body plus `id`.
 * 201 on success, 400 on bad slug, 409 when the id already exists.
 */
export async function createOIDCProvider(
  id: string,
  body: OIDCProviderWriteBody,
  auth: OIDCProviderAuth,
): Promise<OIDCProviderView> {
  let response: Response;
  try {
    response = await fetch(`${API_BASE_URL}/v1/oidc-config/providers`, {
      method: 'POST',
      headers: authHeaders(auth),
      body: JSON.stringify(
        bodyWithAuth({ ...body, id } as Record<string, unknown>, auth),
      ),
    });
  } catch (err) {
    console.error('[oidc-provider] createOIDCProvider network error', err);
    throw new ApiRequestError(
      'network_error',
      'Could not reach the API server.',
      0,
    );
  }
  if (!response.ok) {
    throw await parseError(response);
  }
  return response.json() as Promise<OIDCProviderView>;
}

/**
 * DELETE /v1/oidc-config/providers/:id (Revert). Accepts admin bearer
 * or a setup token (via X-Setup-Token header). Returns nothing
 * (204 on success).
 */
export async function revertOIDCProvider(
  id: string,
  auth: OIDCProviderAuth,
): Promise<void> {
  let response: Response;
  try {
    response = await fetch(
      `${API_BASE_URL}/v1/oidc-config/providers/${encodeURIComponent(id)}`,
      {
        method: 'DELETE',
        headers: authHeaders(auth),
      },
    );
  } catch (err) {
    console.error('[oidc-provider] revertOIDCProvider network error', err);
    throw new ApiRequestError(
      'network_error',
      'Could not reach the API server.',
      0,
    );
  }
  if (!response.ok) {
    throw await parseError(response);
  }
}

/**
 * Slug validation mirrors the API regex from
 * `api/internal/handlers/oidc_providers.go` (providerIDPattern).
 */
export const PROVIDER_ID_PATTERN = /^[a-z][a-z0-9-]{0,30}[a-z0-9]$/;

/**
 * Well-known hints for the Add modal — lightweight client-side preview of
 * the defaults the server will apply when an override field is blank.
 * The server remains the source of truth; these are UI hints only.
 */
export interface WellKnownHint {
  display_name: string;
  issuer_url: string;
  claim_style: string;
  scopes: string[];
}

export const WELL_KNOWN_HINTS: Record<string, WellKnownHint> = {
  google: {
    display_name: 'Google',
    issuer_url: 'https://accounts.google.com',
    claim_style: 'google',
    scopes: ['openid', 'email', 'profile'],
  },
  microsoft: {
    display_name: 'Microsoft',
    issuer_url: 'https://login.microsoftonline.com/common/v2.0',
    claim_style: 'microsoft',
    scopes: ['openid', 'email', 'profile'],
  },
  authelia: {
    display_name: 'Authelia',
    issuer_url: '',
    claim_style: 'authelia',
    scopes: ['openid', 'email', 'profile'],
  },
};
