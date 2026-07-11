import Link from "next/link";
import { requireToken } from "@/lib/auth";
import { listInfluencers } from "@/lib/api/influencers";
import { Card, CardTitle } from "@/components/ui/Card";
import { CreateInfluencerForm } from "@/components/influencers/CreateInfluencerForm";
import type { InfluencerResponse } from "@influaudit/contracts";

export default async function DashboardPage() {
  const token = await requireToken();

  let influencers: InfluencerResponse[] = [];
  let loadError: string | null = null;
  try {
    const res = await listInfluencers(token);
    influencers = res.influencers ?? [];
  } catch {
    loadError = "Could not load influencer profiles from the backend.";
  }

  return (
    <div className="flex flex-col gap-8">
      <div>
        <h1 className="text-xl font-semibold">Overview</h1>
        <p className="mt-1 text-sm text-[var(--ink-secondary)]">
          Create an influencer profile, connect its accounts, then run an audit.
        </p>
      </div>

      <div className="grid gap-6 md:grid-cols-[2fr_1fr]">
        <Card>
          <CardTitle className="mb-4">Your influencers</CardTitle>
          {loadError && (
            <p className="text-sm text-[var(--color-critical)]">{loadError}</p>
          )}
          {!loadError && influencers.length === 0 && (
            <p className="text-sm text-[var(--ink-secondary)]">
              No influencer profiles yet. Add one to get started.
            </p>
          )}
          <ul className="flex flex-col divide-y divide-[var(--line)]">
            {influencers.map((inf) => (
              <li
                key={inf.id}
                className="flex items-center justify-between py-3"
              >
                <div>
                  <p className="font-medium">
                    {inf.display_name ?? "Untitled profile"}
                  </p>
                  <p className="text-xs text-[var(--muted)]">
                    {[inf.niche, inf.tier].filter(Boolean).join(" · ") ||
                      "No niche set"}
                  </p>
                </div>
                <Link
                  href={`/audits?influencer=${inf.id}`}
                  className="text-sm text-[var(--color-brand)] underline"
                >
                  Audit
                </Link>
              </li>
            ))}
          </ul>
        </Card>

        <Card>
          <CardTitle className="mb-4">Add an influencer</CardTitle>
          <CreateInfluencerForm />
        </Card>
      </div>
    </div>
  );
}
