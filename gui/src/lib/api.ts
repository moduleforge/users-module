const API_BASE_URL = process.env.NEXT_PUBLIC_API_BASE_URL ?? 'http://localhost:8080';

export interface ApiError {
  code: string;
  message: string;
}

export interface ApiErrorResponse {
  error: ApiError;
}

export class ApiRequestError extends Error {
  constructor(
    public readonly code: string,
    message: string,
    public readonly status: number,
  ) {
    super(message);
    this.name = 'ApiRequestError';
  }
}

function getToken(): string | null {
  if (typeof window === 'undefined') return null;
  return localStorage.getItem('auth_token');
}

async function request<T>(
  path: string,
  options: RequestInit = {},
): Promise<T> {
  const token = getToken();
  const headers: HeadersInit = {
    'Content-Type': 'application/json',
    ...options.headers,
  };

  if (token) {
    (headers as Record<string, string>)['Authorization'] = `Bearer ${token}`;
  }

  let response: Response;
  try {
    response = await fetch(`${API_BASE_URL}${path}`, {
      ...options,
      headers,
    });
  } catch (err) {
    // Network error — API is unreachable.
    console.error(`[api] Network error: ${path}`, err);
    throw new ApiRequestError(
      'network_error',
      'Could not reach the API server. Is it running?',
      0,
    );
  }

  if (response.status === 401) {
    if (typeof window !== 'undefined') {
      localStorage.removeItem('auth_token');
      window.location.href = '/auth/login';
    }
    throw new ApiRequestError('unauthorized', 'Authentication required', 401);
  }

  if (!response.ok) {
    let errorCode = 'unknown_error';
    let errorMessage = `Request failed with status ${response.status}`;
    try {
      const errorBody = (await response.json()) as ApiErrorResponse;
      if (errorBody.error) {
        errorCode = errorBody.error.code;
        errorMessage = errorBody.error.message;
      }
    } catch {
      // ignore JSON parse errors
    }
    throw new ApiRequestError(errorCode, errorMessage, response.status);
  }

  if (response.status === 204) {
    return undefined as T;
  }

  return response.json() as Promise<T>;
}

// ─── Auth ────────────────────────────────────────────────────────────────────

export interface LoginResponse {
  token: string;
  user: UserSelf;
}

export interface OIDCProvider {
  id: string;
  display_name: string;
}

/**
 * Fetches the list of configured OIDC providers for the login page.
 *
 * Intentionally does NOT send an Authorization header (endpoint is public) and
 * never throws: on any network or HTTP failure the login page must still
 * render so local auth keeps working. Failures are logged to the console.
 */
export async function fetchProviders(): Promise<OIDCProvider[]> {
  try {
    const response = await fetch(`${API_BASE_URL}/v1/auth/providers`, {
      headers: { 'Content-Type': 'application/json' },
    });
    if (!response.ok) {
      console.error(
        `[api] fetchProviders failed with status ${response.status}`,
      );
      return [];
    }
    const body = (await response.json()) as unknown;
    if (!Array.isArray(body)) {
      console.error('[api] fetchProviders: unexpected response shape', body);
      return [];
    }
    return body as OIDCProvider[];
  } catch (err) {
    console.error('[api] fetchProviders network error', err);
    return [];
  }
}

export interface RegisterRequest {
  email: string;
  password: string;
  given_name: string;
  family_name: string;
}

export interface EmailCodeRequest {
  email: string;
}

export interface EmailCodeVerifyRequest {
  email: string;
  code: string;
}

export interface ForgotPasswordRequest {
  email: string;
}

export interface ResetPasswordRequest {
  token: string;
  new_password: string;
}

// ─── Users ───────────────────────────────────────────────────────────────────

export interface UserSelf {
  uuid: string;
  email: string;
  given_name: string;
  family_name: string;
  is_admin: boolean;
  created_at: string;
  updated_at: string;
}

export interface User {
  uuid: string;
  email: string;
  given_name: string;
  family_name: string;
  is_admin: boolean;
  created_at: string;
  updated_at: string;
}

export interface UserListResponse {
  users: User[];
  total: number;
}

export interface UpdateProfileRequest {
  given_name?: string;
  family_name?: string;
}

// ─── Audit ───────────────────────────────────────────────────────────────────

export interface AuditEntry {
  id: string;
  actor_uuid: string;
  actor_email: string;
  action: string;
  entity_uuid: string;
  entity_type: string;
  changes: Record<string, unknown>;
  created_at: string;
}

export interface AuditListResponse {
  entries: AuditEntry[];
  total: number;
}

// ─── Apps ────────────────────────────────────────────────────────────────────

export interface App {
  uuid: string;
  name: string;
  description: string;
  created_at: string;
  updated_at: string;
}

export interface AppListResponse {
  apps: App[];
  total: number;
}

export interface AppMember {
  user_uuid: string;
  email: string;
  given_name: string;
  family_name: string;
  role: string;
  added_at: string;
}

export interface AppMembersResponse {
  members: AppMember[];
}

export interface CreateAppRequest {
  name: string;
  description?: string;
}

export interface AddAppMemberRequest {
  user_uuid: string;
  role: string;
}

// ─── API methods ─────────────────────────────────────────────────────────────

export const api = {
  auth: {
    login: (email: string, password: string) =>
      request<LoginResponse>('/v1/auth/login', {
        method: 'POST',
        body: JSON.stringify({ email, password }),
      }),

    register: (data: RegisterRequest) =>
      request<LoginResponse>('/v1/auth/register', {
        method: 'POST',
        body: JSON.stringify(data),
      }),

    forgotPassword: (data: ForgotPasswordRequest) =>
      request<void>('/v1/auth/forgot-password', {
        method: 'POST',
        body: JSON.stringify(data),
      }),

    resetPassword: (data: ResetPasswordRequest) =>
      request<void>('/v1/auth/reset-password', {
        method: 'POST',
        body: JSON.stringify(data),
      }),

    requestEmailCode: (data: EmailCodeRequest) =>
      request<void>('/v1/auth/email-code', {
        method: 'POST',
        body: JSON.stringify(data),
      }),

    verifyEmailCode: (data: EmailCodeVerifyRequest) =>
      request<LoginResponse>('/v1/auth/email-code/verify', {
        method: 'POST',
        body: JSON.stringify(data),
      }),
  },

  self: {
    get: () => request<UserSelf>('/v1/self'),
    update: (data: UpdateProfileRequest) =>
      request<UserSelf>('/v1/self', {
        method: 'PUT',
        body: JSON.stringify(data),
      }),
  },

  users: {
    list: (query?: string) => {
      const qs = query ? `?q=${encodeURIComponent(query)}` : '';
      return request<UserListResponse>(`/v1/users${qs}`);
    },
    get: (uuid: string) => request<User>(`/v1/users/${uuid}`),
    update: (uuid: string, data: UpdateProfileRequest) =>
      request<User>(`/v1/users/${uuid}`, {
        method: 'PUT',
        body: JSON.stringify(data),
      }),
    grantAdmin: (uuid: string) =>
      request<User>(`/v1/users/${uuid}/admin`, { method: 'POST' }),
    revokeAdmin: (uuid: string) =>
      request<User>(`/v1/users/${uuid}/admin`, { method: 'DELETE' }),
    assume: (uuid: string) =>
      request<LoginResponse>(`/v1/users/${uuid}/assume`, { method: 'POST' }),
    audit: (uuid: string) =>
      request<AuditListResponse>(`/v1/users/${uuid}/audit`),
  },

  audit: {
    list: () => request<AuditListResponse>('/v1/audit'),
    byEntity: (entityUuid: string) =>
      request<AuditListResponse>(`/v1/audit?entity_uuid=${encodeURIComponent(entityUuid)}`),
  },

  apps: {
    list: () => request<AppListResponse>('/v1/apps'),
    get: (uuid: string) => request<App>(`/v1/apps/${uuid}`),
    create: (data: CreateAppRequest) =>
      request<App>('/v1/apps', {
        method: 'POST',
        body: JSON.stringify(data),
      }),
    update: (uuid: string, data: Partial<CreateAppRequest>) =>
      request<App>(`/v1/apps/${uuid}`, {
        method: 'PUT',
        body: JSON.stringify(data),
      }),
    delete: (uuid: string) =>
      request<void>(`/v1/apps/${uuid}`, { method: 'DELETE' }),
    getMembers: (uuid: string) =>
      request<AppMembersResponse>(`/v1/apps/${uuid}/members`),
    addMember: (uuid: string, data: AddAppMemberRequest) =>
      request<void>(`/v1/apps/${uuid}/members`, {
        method: 'POST',
        body: JSON.stringify(data),
      }),
    removeMember: (uuid: string, userUuid: string) =>
      request<void>(`/v1/apps/${uuid}/members/${userUuid}`, {
        method: 'DELETE',
      }),
  },
};
