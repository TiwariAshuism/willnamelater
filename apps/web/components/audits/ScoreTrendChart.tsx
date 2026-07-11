"use client";

import {
  useCallback,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import type { ScorePoint } from "@influaudit/contracts";

/**
 * Score trend — a single-series line chart of overall score over time.
 *
 * Design per the dataviz method: one series ⇒ no legend (the title names it);
 * one categorical hue (blue, slot 1), stepped for dark mode; recessive
 * hairline grid and muted axis ink; the latest point is directly labelled; a
 * crosshair + tooltip is the interaction layer. Colours are role-based CSS
 * variables so light/dark swap in one place.
 */

const WIDTH = 640;
const HEIGHT = 240;
const PAD = { top: 16, right: 44, bottom: 28, left: 36 };

interface Plotted {
  x: number;
  y: number;
  point: ScorePoint;
}

function niceDomain(values: number[]): [number, number] {
  if (values.length === 0) return [0, 100];
  const min = Math.min(...values);
  const max = Math.max(...values);
  if (min === max) {
    return [Math.max(0, min - 5), max + 5];
  }
  const pad = (max - min) * 0.15;
  return [Math.max(0, Math.floor(min - pad)), Math.ceil(max + pad)];
}

function formatDate(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

export function ScoreTrendChart({ points }: { points: ScorePoint[] }) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(WIDTH);
  const [hover, setHover] = useState<number | null>(null);

  useLayoutEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    const update = () => setWidth(Math.max(320, el.clientWidth));
    update();
    const ro = new ResizeObserver(update);
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const sorted = useMemo(
    () =>
      [...points].sort(
        (a, b) =>
          new Date(a.created_at).getTime() - new Date(b.created_at).getTime(),
      ),
    [points],
  );

  const [yMin, yMax] = useMemo(
    () => niceDomain(sorted.map((p) => p.overall)),
    [sorted],
  );

  const plotted = useMemo<Plotted[]>(() => {
    const innerW = width - PAD.left - PAD.right;
    const innerH = HEIGHT - PAD.top - PAD.bottom;
    const n = sorted.length;
    return sorted.map((point, i) => {
      const x = PAD.left + (n <= 1 ? innerW / 2 : (innerW * i) / (n - 1));
      const t = yMax === yMin ? 0.5 : (point.overall - yMin) / (yMax - yMin);
      const y = PAD.top + innerH * (1 - t);
      return { x, y, point };
    });
  }, [sorted, width, yMin, yMax]);

  const linePath = useMemo(
    () =>
      plotted
        .map((p, i) => `${i === 0 ? "M" : "L"}${p.x.toFixed(1)},${p.y.toFixed(1)}`)
        .join(" "),
    [plotted],
  );

  const areaPath = useMemo(() => {
    if (plotted.length === 0) return "";
    const baseline = HEIGHT - PAD.bottom;
    const first = plotted[0]!;
    const last = plotted[plotted.length - 1]!;
    return (
      `M${first.x.toFixed(1)},${baseline} ` +
      plotted.map((p) => `L${p.x.toFixed(1)},${p.y.toFixed(1)}`).join(" ") +
      ` L${last.x.toFixed(1)},${baseline} Z`
    );
  }, [plotted]);

  const yTicks = useMemo(() => {
    const count = 4;
    return Array.from({ length: count + 1 }, (_, i) => {
      const value = yMin + ((yMax - yMin) * i) / count;
      const innerH = HEIGHT - PAD.top - PAD.bottom;
      const y = PAD.top + innerH * (1 - i / count);
      return { value, y };
    });
  }, [yMin, yMax]);

  const onMove = useCallback(
    (event: React.PointerEvent<SVGSVGElement>) => {
      if (plotted.length === 0) return;
      const rect = event.currentTarget.getBoundingClientRect();
      const scaleX = width / rect.width;
      const px = (event.clientX - rect.left) * scaleX;
      let nearest = 0;
      let best = Infinity;
      for (let i = 0; i < plotted.length; i++) {
        const d = Math.abs(plotted[i]!.x - px);
        if (d < best) {
          best = d;
          nearest = i;
        }
      }
      setHover(nearest);
    },
    [plotted, width],
  );

  if (sorted.length === 0) {
    return (
      <p className="text-sm text-[var(--ink-secondary)]">
        No score history yet — run an audit to start the trend.
      </p>
    );
  }

  const active = hover === null ? null : plotted[hover] ?? null;
  const last = plotted[plotted.length - 1]!;

  return (
    <div
      ref={containerRef}
      className="viz-root w-full overflow-x-auto"
      style={
        {
          "--surface-1": "#fcfcfb",
          "--text-secondary": "#52514e",
          "--muted": "#898781",
          "--grid": "#e1e0d9",
          "--baseline": "#c3c2b7",
          "--series-1": "#2a78d6",
        } as React.CSSProperties
      }
    >
      <style>{`
        @media (prefers-color-scheme: dark) {
          .viz-root {
            --surface-1: #1a1a19;
            --text-secondary: #c3c2b7;
            --grid: #2c2c2a;
            --baseline: #383835;
            --series-1: #3987e5;
          }
        }
      `}</style>
      <svg
        role="img"
        aria-label="Overall score over time"
        viewBox={`0 0 ${width} ${HEIGHT}`}
        width="100%"
        height={HEIGHT}
        onPointerMove={onMove}
        onPointerLeave={() => setHover(null)}
      >
        <defs>
          <linearGradient id="scoreArea" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="var(--series-1)" stopOpacity="0.18" />
            <stop offset="100%" stopColor="var(--series-1)" stopOpacity="0" />
          </linearGradient>
        </defs>

        {/* gridlines + y ticks */}
        {yTicks.map((tick) => (
          <g key={tick.y}>
            <line
              x1={PAD.left}
              x2={width - PAD.right}
              y1={tick.y}
              y2={tick.y}
              stroke="var(--grid)"
              strokeWidth={1}
            />
            <text
              x={PAD.left - 8}
              y={tick.y + 3}
              textAnchor="end"
              fontSize={10}
              fill="var(--muted)"
              style={{ fontVariantNumeric: "tabular-nums" }}
            >
              {Math.round(tick.value)}
            </text>
          </g>
        ))}

        {/* x axis end labels */}
        <text
          x={PAD.left}
          y={HEIGHT - 8}
          textAnchor="start"
          fontSize={10}
          fill="var(--muted)"
        >
          {formatDate(sorted[0]!.created_at)}
        </text>
        {sorted.length > 1 && (
          <text
            x={width - PAD.right}
            y={HEIGHT - 8}
            textAnchor="end"
            fontSize={10}
            fill="var(--muted)"
          >
            {formatDate(sorted[sorted.length - 1]!.created_at)}
          </text>
        )}

        <path d={areaPath} fill="url(#scoreArea)" />
        <path
          d={linePath}
          fill="none"
          stroke="var(--series-1)"
          strokeWidth={2}
          strokeLinejoin="round"
          strokeLinecap="round"
        />

        {/* direct label on the latest point */}
        <circle cx={last.x} cy={last.y} r={4} fill="var(--series-1)" />
        <text
          x={last.x + 6}
          y={last.y + 3}
          fontSize={11}
          fontWeight={600}
          fill="var(--series-1)"
          style={{ fontVariantNumeric: "tabular-nums" }}
        >
          {last.point.overall.toFixed(1)}
        </text>

        {/* crosshair + hover marker */}
        {active && (
          <g>
            <line
              x1={active.x}
              x2={active.x}
              y1={PAD.top}
              y2={HEIGHT - PAD.bottom}
              stroke="var(--baseline)"
              strokeWidth={1}
              strokeDasharray="3 3"
            />
            <circle
              cx={active.x}
              cy={active.y}
              r={5}
              fill="var(--series-1)"
              stroke="var(--surface-1)"
              strokeWidth={2}
            />
          </g>
        )}
      </svg>

      {active && (
        <div className="mt-1 text-xs text-[var(--ink-secondary)]">
          <span className="font-medium">
            {active.point.overall.toFixed(1)}
          </span>{" "}
          on {new Date(active.point.created_at).toLocaleDateString()} · confidence{" "}
          {Math.round(active.point.confidence * 100)}%
        </div>
      )}
    </div>
  );
}
