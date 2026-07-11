import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { AuditStatusBadge } from "./AuditStatusBadge";

describe("AuditStatusBadge", () => {
  it("renders the status label", () => {
    render(<AuditStatusBadge status="partial" />);
    expect(screen.getByText("partial")).toBeInTheDocument();
  });

  it("falls back to the queued style for an unknown status", () => {
    render(<AuditStatusBadge status="mystery" />);
    expect(screen.getByText("mystery")).toBeInTheDocument();
  });
});
