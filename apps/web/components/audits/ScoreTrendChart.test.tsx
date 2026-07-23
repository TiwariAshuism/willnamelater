import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { ScoreTrendChart } from "./ScoreTrendChart";
import type { ScorePoint } from "@influaudit/contracts";

const points: ScorePoint[] = [
  {
    audit_job_id: "a1",
    overall: 71.2,
    confidence: 0.6,
    created_at: "2026-06-01T00:00:00Z",
  },
  {
    audit_job_id: "a2",
    overall: 82.5,
    confidence: 0.8,
    created_at: "2026-07-01T00:00:00Z",
  },
];

describe("ScoreTrendChart", () => {
  it("plots the series and direct-labels the latest score", () => {
    render(<ScoreTrendChart points={points} />);
    expect(
      screen.getByRole("img", { name: /overall score over time/i }),
    ).toBeInTheDocument();
    // Latest point (82.5) is directly labelled on the chart.
    expect(screen.getByText("82.5")).toBeInTheDocument();
  });

  it("shows an empty state without any points", () => {
    render(<ScoreTrendChart points={[]} />);
    expect(screen.getByText(/no score history yet/i)).toBeInTheDocument();
  });
});
