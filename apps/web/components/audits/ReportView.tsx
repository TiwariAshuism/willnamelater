import { Card, CardTitle } from "@/components/ui/Card";
import type { Report } from "@influaudit/contracts";

function Metric({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col">
      <span className="text-xs text-[var(--muted)]">{label}</span>
      <span
        className="text-2xl font-semibold"
        style={{ fontVariantNumeric: "tabular-nums" }}
      >
        {value}
      </span>
    </div>
  );
}

export function ReportView({ report }: { report: Report }) {
  const { score, fraud, narrative, narrative_available } = report;

  return (
    <div className="flex flex-col gap-6">
      {/* Score */}
      <Card className="flex flex-col gap-4">
        <CardTitle>Score</CardTitle>
        {score.available ? (
          <>
            <div className="flex flex-wrap gap-8">
              <Metric label="Overall" value={score.overall.toFixed(1)} />
              <Metric
                label="Authenticity"
                value={score.authenticity.toFixed(1)}
              />
              {score.tier && <Metric label="Tier" value={score.tier} />}
              {score.niche && <Metric label="Niche" value={score.niche} />}
            </div>
            {score.subscores.length > 0 && (
              <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
                {score.subscores.map((s) => (
                  <div
                    key={s.name}
                    className="rounded-lg border border-[var(--line)] p-3"
                  >
                    <p className="text-xs capitalize text-[var(--muted)]">
                      {s.name.replace(/_/g, " ")}
                    </p>
                    <p
                      className="text-lg font-semibold"
                      style={{ fontVariantNumeric: "tabular-nums" }}
                    >
                      {s.value.toFixed(1)}
                    </p>
                    <p className="text-xs text-[var(--muted)]">
                      confidence {Math.round(s.confidence * 100)}%
                    </p>
                  </div>
                ))}
              </div>
            )}
            {score.benchmark_label && (
              <p className="text-xs text-[var(--muted)]">
                Benchmarks: {score.benchmark_label}
              </p>
            )}
          </>
        ) : (
          <p className="text-sm text-[var(--ink-secondary)]">
            Score not available for this audit.
          </p>
        )}
      </Card>

      {/* Fraud — explicitly labelled an estimate */}
      <Card className="flex flex-col gap-4">
        <CardTitle>Fraud signals (estimate)</CardTitle>
        {fraud.available ? (
          <>
            <div className="grid grid-cols-2 gap-3 sm:grid-cols-3">
              <Metric
                label="Fake follower rate"
                value={`${Math.round(fraud.fake_follower_rate * 100)}%`}
              />
              <Metric
                label="Bot comment rate"
                value={`${Math.round(fraud.bot_comment_rate * 100)}%`}
              />
              <Metric label="Coordinated cliques" value={String(fraud.clique_count)} />
            </div>
            <p className="text-xs text-[var(--muted)]">
              This is a statistical estimate (confidence{" "}
              {Math.round(fraud.confidence * 100)}%, model {fraud.model_version}),
              not a definitive judgement.
            </p>
          </>
        ) : (
          <p className="text-sm text-[var(--ink-secondary)]">
            Fraud analysis not available for this audit.
          </p>
        )}
      </Card>

      {/* Narrative */}
      <Card className="flex flex-col gap-4">
        <CardTitle>Report</CardTitle>
        {narrative_available ? (
          <>
            <p className="text-sm text-[var(--ink)]">{narrative.summary}</p>

            {narrative.weakness_fix_pairs.length > 0 && (
              <div>
                <h3 className="mb-2 text-xs font-semibold uppercase text-[var(--muted)]">
                  Weaknesses &amp; fixes
                </h3>
                <ul className="flex flex-col gap-2">
                  {narrative.weakness_fix_pairs.map((pair, i) => (
                    <li key={i} className="text-sm">
                      <span className="font-medium">{pair.weakness}</span>
                      {" — "}
                      <span className="text-[var(--ink-secondary)]">
                        {pair.fix}
                      </span>
                    </li>
                  ))}
                </ul>
              </div>
            )}

            {narrative.growth_tips.length > 0 && (
              <div>
                <h3 className="mb-2 text-xs font-semibold uppercase text-[var(--muted)]">
                  Growth tips
                </h3>
                <ul className="list-disc pl-5 text-sm text-[var(--ink-secondary)]">
                  {narrative.growth_tips.map((tip, i) => (
                    <li key={i}>{tip}</li>
                  ))}
                </ul>
              </div>
            )}

            {narrative.brand_fit && (
              <div>
                <h3 className="mb-1 text-xs font-semibold uppercase text-[var(--muted)]">
                  Brand fit
                </h3>
                <p className="text-sm text-[var(--ink-secondary)]">
                  {narrative.brand_fit}
                </p>
              </div>
            )}
          </>
        ) : (
          <p className="text-sm text-[var(--ink-secondary)]">
            The written report is still being generated.
          </p>
        )}
      </Card>
    </div>
  );
}
