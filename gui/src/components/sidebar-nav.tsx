'use client';

import Link from 'next/link';
import { usePathname } from 'next/navigation';
import {
  ClipboardList,
  LayoutGrid,
  LogIn,
  LogOut,
  Settings,
  User,
  Users,
} from 'lucide-react';
import { cn } from '@/lib/utils';
import { useAuth } from '@/lib/auth-context';
import { Button } from '@/components/ui/button';

interface NavItem {
  label: string;
  href: string;
  icon: React.ReactNode;
  adminOnly?: boolean;
}

const navItems: NavItem[] = [
  { label: 'Profile', href: '/profile', icon: <User className="size-4" /> },
  {
    label: 'Users',
    href: '/admin/users',
    icon: <Users className="size-4" />,
    adminOnly: true,
  },
  {
    label: 'Audit',
    href: '/admin/audit',
    icon: <ClipboardList className="size-4" />,
    adminOnly: true,
  },
  {
    label: 'Apps',
    href: '/admin/apps',
    icon: <LayoutGrid className="size-4" />,
    adminOnly: true,
  },
  {
    label: 'OIDC Settings',
    href: '/oidc-config',
    icon: <Settings className="size-4" />,
    adminOnly: true,
  },
];

export function SidebarNav() {
  const pathname = usePathname();
  const { user, isAdmin, logout } = useAuth();

  return (
    <aside className="flex h-full w-56 flex-col border-r bg-sidebar">
      <div className="px-4 py-5 border-b">
        <h1 className="text-base font-semibold text-sidebar-foreground">User Manager</h1>
        {user && (
          <p className="mt-0.5 text-xs text-muted-foreground truncate">
            {user.given_name} {user.family_name}
          </p>
        )}
      </div>

      <nav className="flex flex-1 flex-col gap-1 p-2">
        {!user && (
          <Link
            href="/auth/login"
            className={cn(
              'flex items-center gap-2.5 rounded-md px-3 py-2 text-sm font-medium transition-colors',
              pathname === '/auth/login'
                ? 'bg-sidebar-accent text-sidebar-accent-foreground'
                : 'text-sidebar-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground',
            )}
          >
            <LogIn className="size-4" />
            Login
          </Link>
        )}

        {navItems
          .filter((item) => !item.adminOnly || isAdmin)
          .map((item) => (
            <Link
              key={item.href}
              href={item.href}
              className={cn(
                'flex items-center gap-2.5 rounded-md px-3 py-2 text-sm font-medium transition-colors',
                pathname === item.href || pathname.startsWith(item.href + '/')
                  ? 'bg-sidebar-accent text-sidebar-accent-foreground'
                  : 'text-sidebar-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground',
              )}
            >
              {item.icon}
              {item.label}
            </Link>
          ))}
      </nav>

      {user && (
        <div className="border-t p-2">
          <Button
            variant="ghost"
            size="sm"
            className="w-full justify-start gap-2.5 text-muted-foreground hover:text-foreground"
            onClick={logout}
          >
            <LogOut className="size-4" />
            Sign out
          </Button>
        </div>
      )}
    </aside>
  );
}
