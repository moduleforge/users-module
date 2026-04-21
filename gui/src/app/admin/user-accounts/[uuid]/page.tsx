'use client';

import { useEffect, useState, use } from 'react';
import Link from 'next/link';
import { useRouter } from 'next/navigation';
import { api, ApiRequestError, type UserAccount, type AuditEntry } from '@/lib/api';
import { useAuth } from '@/lib/auth-context';
import { RequireAuth } from '@/components/require-auth';
import { Button, Input, Label, Card, CardContent, CardHeader, CardTitle, Badge } from '@moduleforge/core-gui';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table';
import { ErrorMessage } from '@/components/error-message';
import { CheckCircle2, ArrowLeft, ExternalLink } from 'lucide-react';

function UserAccountDetailContent({ uuid }: { uuid: string }) {
  const { setTokenAndUser, user: currentUser } = useAuth();
  const router = useRouter();
  const [userAccount, setUserAccount] = useState<UserAccount | null>(null);
  const [auditEntries, setAuditEntries] = useState<AuditEntry[]>([]);
  const [givenName, setGivenName] = useState('');
  const [familyName, setFamilyName] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [success, setSuccess] = useState(false);
  const [isLoading, setIsLoading] = useState(true);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [isActing, setIsActing] = useState(false);

  useEffect(() => {
    async function load() {
      setIsLoading(true);
      try {
        const [uaData, auditData] = await Promise.all([
          api.userAccounts.get(uuid),
          api.userAccounts.audit(uuid),
        ]);
        setUserAccount(uaData);
        setGivenName(uaData.given_name);
        setFamilyName(uaData.family_name);
        setAuditEntries(auditData.entries ?? []);
      } catch (err) {
        if (err instanceof ApiRequestError) {
          setError(err.message);
        } else {
          setError('Failed to load user account.');
        }
      } finally {
        setIsLoading(false);
      }
    }
    void load();
  }, [uuid]);

  async function handleUpdate(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setSuccess(false);
    setIsSubmitting(true);
    try {
      const updated = await api.userAccounts.update(uuid, {
        given_name: givenName,
        family_name: familyName,
      });
      setUserAccount(updated);
      setSuccess(true);
    } catch (err) {
      if (err instanceof ApiRequestError) {
        setError(err.message);
      } else {
        setError('Failed to update user account.');
      }
    } finally {
      setIsSubmitting(false);
    }
  }

  async function handleToggleAdmin() {
    if (!userAccount) return;
    setActionError(null);
    setIsActing(true);
    try {
      const updated = userAccount.is_admin
        ? await api.userAccounts.revokeAdmin(uuid)
        : await api.userAccounts.grantAdmin(uuid);
      setUserAccount(updated);
    } catch (err) {
      if (err instanceof ApiRequestError) {
        setActionError(err.message);
      } else {
        setActionError('Failed to update admin status.');
      }
    } finally {
      setIsActing(false);
    }
  }

  async function handleAssume() {
    setActionError(null);
    setIsActing(true);
    try {
      const response = await api.userAccounts.assume(uuid);
      setTokenAndUser(response.token, response.user);
      router.push('/profile');
    } catch (err) {
      if (err instanceof ApiRequestError) {
        setActionError(err.message);
      } else {
        setActionError('Failed to assume identity.');
      }
    } finally {
      setIsActing(false);
    }
  }

  if (isLoading) {
    return <p className="p-6 text-sm text-muted-foreground">Loading...</p>;
  }

  if (!userAccount) {
    return (
      <div className="p-6">
        <ErrorMessage message={error ?? 'User account not found.'} />
      </div>
    );
  }

  const isSelf = currentUser?.uuid === uuid;

  return (
    <div className="p-6 max-w-2xl">
      <div className="mb-6">
        <Link
          href="/admin/user-accounts"
          className="flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground mb-4"
        >
          <ArrowLeft className="size-4" />
          Back to user accounts
        </Link>
        <div className="flex items-center gap-3">
          <h1 className="text-2xl font-semibold">
            {userAccount.given_name} {userAccount.family_name}
          </h1>
          {userAccount.is_admin && <Badge>Admin</Badge>}
        </div>
        <p className="text-sm text-muted-foreground mt-1">{userAccount.email}</p>
      </div>

      <div className="flex flex-col gap-4">
        <Card>
          <CardHeader>
            <CardTitle>Edit profile</CardTitle>
          </CardHeader>
          <CardContent>
            <form onSubmit={handleUpdate} className="flex flex-col gap-4">
              <ErrorMessage message={error} />
              {success && (
                <div className="flex items-center gap-2 rounded-lg border border-green-200 bg-green-50 px-3 py-2 text-sm text-green-800 dark:border-green-800 dark:bg-green-950 dark:text-green-200">
                  <CheckCircle2 className="size-4" />
                  Profile updated successfully.
                </div>
              )}
              <div className="grid grid-cols-2 gap-3">
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="given-name">First name</Label>
                  <Input
                    id="given-name"
                    type="text"
                    value={givenName}
                    onChange={(e) => setGivenName(e.target.value)}
                    required
                  />
                </div>
                <div className="flex flex-col gap-1.5">
                  <Label htmlFor="family-name">Last name</Label>
                  <Input
                    id="family-name"
                    type="text"
                    value={familyName}
                    onChange={(e) => setFamilyName(e.target.value)}
                    required
                  />
                </div>
              </div>
              <div className="flex justify-end">
                <Button type="submit" disabled={isSubmitting}>
                  {isSubmitting ? 'Saving...' : 'Save changes'}
                </Button>
              </div>
            </form>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Admin actions</CardTitle>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            <ErrorMessage message={actionError} />
            <div className="flex flex-wrap gap-2">
              <Button
                variant={userAccount.is_admin ? 'destructive' : 'outline'}
                onClick={handleToggleAdmin}
                disabled={isActing || isSelf}
              >
                {userAccount.is_admin ? 'Revoke admin' : 'Grant admin'}
              </Button>
              <Button
                variant="outline"
                onClick={handleAssume}
                disabled={isActing || isSelf}
              >
                Assume identity
              </Button>
              <Button variant="outline" asChild>
                <Link href={`/admin/audit?user_uuid=${uuid}`}>
                  <ExternalLink className="size-4" />
                  View full audit log
                </Link>
              </Button>
            </div>
            {isSelf && (
              <p className="text-xs text-muted-foreground">
                Admin actions are not available on your own account.
              </p>
            )}
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Recent activity</CardTitle>
          </CardHeader>
          <CardContent>
            {auditEntries.length === 0 ? (
              <p className="text-sm text-muted-foreground">No activity recorded.</p>
            ) : (
              <div className="rounded-lg border">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Action</TableHead>
                      <TableHead>Entity</TableHead>
                      <TableHead>When</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {auditEntries.slice(0, 10).map((entry) => (
                      <TableRow key={entry.id}>
                        <TableCell className="font-mono text-xs">{entry.action}</TableCell>
                        <TableCell className="text-xs text-muted-foreground font-mono truncate max-w-[180px]">
                          {entry.entity_type}: {entry.entity_uuid}
                        </TableCell>
                        <TableCell className="text-xs text-muted-foreground">
                          {new Date(entry.created_at).toLocaleString()}
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            )}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}

export default function UserAccountDetailPage({
  params,
}: {
  params: Promise<{ uuid: string }>;
}) {
  const { uuid } = use(params);
  return (
    <RequireAuth requireAdmin>
      <UserAccountDetailContent uuid={uuid} />
    </RequireAuth>
  );
}
