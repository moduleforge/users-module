'use client';

import React, {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useState,
} from 'react';
import { useRouter } from 'next/navigation';
import { api, ApiRequestError, type UserAccountSelf } from '@/lib/api';

const TOKEN_KEY = 'auth_token';

interface AuthContextValue {
  token: string | null;
  user: UserAccountSelf | null;
  isLoading: boolean;
  isAdmin: boolean;
  login: (email: string, password: string) => Promise<void>;
  register: (
    email: string,
    password: string,
    givenName: string,
    familyName: string,
  ) => Promise<void>;
  logout: () => void;
  setTokenAndUser: (token: string, user: UserAccountSelf) => void;
  /**
   * Finalize an externally-obtained session (e.g., OAuth callback) by
   * storing the token and hydrating the user from `/v1/self`. On failure,
   * clears the token and throws so the caller can surface the error.
   */
  completeExternalLogin: (token: string) => Promise<void>;
  refreshUser: () => Promise<void>;
}

const AuthContext = createContext<AuthContextValue | null>(null);

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const router = useRouter();
  const [token, setToken] = useState<string | null>(null);
  const [user, setUser] = useState<UserAccountSelf | null>(null);
  const [isLoading, setIsLoading] = useState(true);

  const setTokenAndUser = useCallback((newToken: string, newUser: UserAccountSelf) => {
    localStorage.setItem(TOKEN_KEY, newToken);
    setToken(newToken);
    setUser(newUser);
  }, []);

  const logout = useCallback(() => {
    localStorage.removeItem(TOKEN_KEY);
    setToken(null);
    setUser(null);
    router.push('/auth/login');
  }, [router]);

  const refreshUser = useCallback(async () => {
    try {
      const self = await api.self.get();
      setUser(self);
    } catch (err) {
      if (err instanceof ApiRequestError && err.status === 401) {
        logout();
      }
    }
  }, [logout]);

  // Validate token on mount
  useEffect(() => {
    const storedToken = localStorage.getItem(TOKEN_KEY);
    if (!storedToken) {
      setIsLoading(false);
      return;
    }
    setToken(storedToken);
    api.self
      .get()
      .then((self) => {
        setUser(self);
      })
      .catch(() => {
        localStorage.removeItem(TOKEN_KEY);
        setToken(null);
      })
      .finally(() => {
        setIsLoading(false);
      });
  }, []);

  const login = useCallback(
    async (email: string, password: string) => {
      const response = await api.auth.login(email, password);
      setTokenAndUser(response.token, response.user);
    },
    [setTokenAndUser],
  );

  const completeExternalLogin = useCallback(
    async (newToken: string) => {
      // Store the token first so the shared `request()` helper in api.ts
      // picks it up via localStorage for the `/v1/self` call below.
      localStorage.setItem(TOKEN_KEY, newToken);
      try {
        // `skipAuthRedirect` ensures a bad/expired token surfaces as a thrown
        // ApiRequestError instead of the shared helper hard-redirecting to
        // /auth/login before our catch block can run. The return page needs
        // to control that redirect so it can attach an `?error=...` message.
        const self = await api.self.get({ skipAuthRedirect: true });
        setTokenAndUser(newToken, self);
      } catch (err) {
        // Bad/expired token or API failure: don't leave a stale token behind.
        localStorage.removeItem(TOKEN_KEY);
        setToken(null);
        setUser(null);
        throw err;
      }
    },
    [setTokenAndUser],
  );

  const register = useCallback(
    async (
      email: string,
      password: string,
      givenName: string,
      familyName: string,
    ) => {
      const response = await api.auth.register({
        email,
        password,
        given_name: givenName,
        family_name: familyName,
      });
      setTokenAndUser(response.token, response.user);
    },
    [setTokenAndUser],
  );

  const value: AuthContextValue = {
    token,
    user,
    isLoading,
    isAdmin: user?.is_admin ?? false,
    login,
    register,
    logout,
    setTokenAndUser,
    completeExternalLogin,
    refreshUser,
  };

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthContextValue {
  const context = useContext(AuthContext);
  if (!context) {
    throw new Error('useAuth must be used within an AuthProvider');
  }
  return context;
}

/**
 * Like {@link useAuth} but returns `null` if no `AuthProvider` is mounted
 * rather than throwing. Used by pages that render in both authenticated
 * and pre-auth contexts — e.g. `/oidc-config`, which is reachable during
 * the initial setup flow (no provider mounted) AND post-confirmation
 * (provider mounted, possibly with an admin session).
 */
export function useOptionalAuth(): AuthContextValue | null {
  return useContext(AuthContext);
}
