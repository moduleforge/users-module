'use client';

import { useEffect, useState, useCallback } from 'react';
import Link from 'next/link';
import { api, ApiRequestError, type UserAccount } from '@/lib/api';
import { RequireAuth } from '@/components/require-auth';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { ErrorMessage } from '@/components/error-message';
import { Search } from 'lucide-react';
import { Input, Badge } from '@moduleforge/core-gui';

function UserAccountListContent() {
  const [userAccounts, setUserAccounts] = useState<UserAccount[]>([]);
  const [query, setQuery] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(true);

  const loadUserAccounts = useCallback(async (q: string) => {
    setIsLoading(true);
    setError(null);
    try {
      const response = await api.userAccounts.list(q || undefined);
      setUserAccounts(response.user_accounts ?? []);
    } catch (err) {
      if (err instanceof ApiRequestError) {
        setError(err.message);
      } else {
        setError('Failed to load user accounts.');
      }
    } finally {
      setIsLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadUserAccounts('');
  }, [loadUserAccounts]);

  function handleSearch(e: React.FormEvent) {
    e.preventDefault();
    void loadUserAccounts(query);
  }

  return (
    <div className="p-6">
      <div className="mb-6 flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold">User Accounts</h1>
          <p className="text-sm text-muted-foreground mt-1">
            Manage user accounts
          </p>
        </div>
      </div>

      <form onSubmit={handleSearch} className="mb-4 flex gap-2 max-w-sm">
        <div className="relative flex-1">
          <Search className="absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            type="text"
            placeholder="Search by name or email..."
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            className="pl-8"
          />
        </div>
      </form>

      <ErrorMessage message={error} />

      {isLoading ? (
        <p className="text-sm text-muted-foreground">Loading...</p>
      ) : (
        <div className="rounded-lg border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Email</TableHead>
                <TableHead>Role</TableHead>
                <TableHead>Created</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {userAccounts.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={4} className="text-center text-muted-foreground py-8">
                    No user accounts found.
                  </TableCell>
                </TableRow>
              ) : (
                userAccounts.map((ua) => (
                  <TableRow key={ua.uuid}>
                    <TableCell>
                      <Link
                        href={`/admin/user-accounts/${ua.uuid}`}
                        className="font-medium hover:underline"
                      >
                        {ua.given_name} {ua.family_name}
                      </Link>
                    </TableCell>
                    <TableCell className="text-muted-foreground">
                      {ua.email}
                    </TableCell>
                    <TableCell>
                      {ua.is_admin ? (
                        <Badge>Admin</Badge>
                      ) : (
                        <Badge variant="secondary">User</Badge>
                      )}
                    </TableCell>
                    <TableCell className="text-muted-foreground text-xs">
                      {new Date(ua.created_at).toLocaleDateString()}
                    </TableCell>
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        </div>
      )}
    </div>
  );
}

export default function AdminUserAccountsPage() {
  return (
    <RequireAuth requireAdmin>
      <UserAccountListContent />
    </RequireAuth>
  );
}
