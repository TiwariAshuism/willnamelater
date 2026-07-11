import { requireToken } from "@/lib/auth";
import { listConnections } from "@/lib/api/oauth";
import { Card, CardTitle } from "@/components/ui/Card";
import { ConnectAccountButton } from "@/components/connections/ConnectAccountButton";
import { YOUTUBE_PROVIDER } from "@/lib/providers";
import type { ConnectionResponse } from "@influaudit/contracts";

export default async function ConnectionsPage({
  searchParams,
}: {
  searchParams: Promise<{ connected?: string; error?: string }>;
}) {
  const token = await requireToken();
  const { connected, error } = await searchParams;

  let connections: ConnectionResponse[] = [];
  let loadError: string | null = null;
  try {
    connections = await listConnections(token);
  } catch {
    loadError = "Could not load your connected accounts.";
  }

  return (
    <div className="flex flex-col gap-6">
      <div>
        <h1 className="text-xl font-semibold">Connections</h1>
        <p className="mt-1 text-sm text-[var(--ink-secondary)]">
          Connect a platform account so an audit can read its real metrics.
        </p>
      </div>

      {connected && (
        <p className="rounded-md border border-[var(--line)] bg-[var(--surface-2)] px-4 py-2 text-sm text-[var(--color-good)]">
          Connected {connected} successfully.
        </p>
      )}
      {error && (
        <p className="rounded-md border border-[var(--line)] bg-[var(--surface-2)] px-4 py-2 text-sm text-[var(--color-critical)]">
          Connection failed: {error}
        </p>
      )}

      <Card className="flex flex-col gap-4">
        <div className="flex items-center justify-between">
          <div>
            <CardTitle>YouTube</CardTitle>
            <p className="mt-1 text-sm text-[var(--ink-secondary)]">
              Authorize via Google (read-only analytics).
            </p>
          </div>
          <ConnectAccountButton
            provider={YOUTUBE_PROVIDER}
            label="Connect YouTube"
          />
        </div>
      </Card>

      <Card>
        <CardTitle className="mb-4">Connected accounts</CardTitle>
        {loadError && (
          <p className="text-sm text-[var(--color-critical)]">{loadError}</p>
        )}
        {!loadError && connections.length === 0 && (
          <p className="text-sm text-[var(--ink-secondary)]">
            No accounts connected yet.
          </p>
        )}
        <ul className="flex flex-col divide-y divide-[var(--line)]">
          {connections.map((conn) => (
            <li
              key={`${conn.provider}-${conn.provider_account_id}`}
              className="flex items-center justify-between py-3"
            >
              <div>
                <p className="font-medium capitalize">{conn.platform}</p>
                <p className="text-xs text-[var(--muted)]">
                  {conn.provider} · connected{" "}
                  {new Date(conn.connected_at).toLocaleDateString()}
                </p>
              </div>
              <span className="text-xs text-[var(--color-good)]">Active</span>
            </li>
          ))}
        </ul>
      </Card>
    </div>
  );
}
