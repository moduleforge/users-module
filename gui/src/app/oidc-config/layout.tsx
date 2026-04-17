/**
 * Route-group layout for `/oidc-config`. Intentionally a passthrough.
 *
 * The unconfirmed-state bypass is handled in `ClientLayout` by checking the
 * current pathname: when OIDC is unconfirmed and the user is on this route,
 * `ClientLayout` renders children *without* the `AuthProvider` (so no
 * `/v1/self` fetch is fired against the 503-ing API). This layout file is
 * kept as a clear extension point should per-route chrome be needed later.
 */
export default function OIDCConfigLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return <>{children}</>;
}
