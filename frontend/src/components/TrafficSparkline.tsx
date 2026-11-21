import React from "react";
import { formatRate } from "../utils/format";

export interface TrafficSample {
  rateSent: number;
  rateRecv: number;
}

interface TrafficSparklineProps {
  samples: TrafficSample[];
  upLabel: string;
  downLabel: string;
}

export const TrafficSparkline: React.FC<TrafficSparklineProps> = ({
  samples,
  upLabel,
  downLabel,
}) => {
  const width = 560;
  const height = 96;
  const padY = 8;

  const padded =
    samples.length >= 2
      ? samples
      : [
          { rateSent: 0, rateRecv: 0 },
          ...(samples.length === 1 ? samples : [{ rateSent: 0, rateRecv: 0 }]),
        ];

  const maxVal = Math.max(
    1024,
    ...padded.map((s) => Math.max(s.rateSent, s.rateRecv)),
  );

  const buildPath = (selector: (s: TrafficSample) => number) => {
    const stepX = width / Math.max(1, padded.length - 1);
    const pts = padded.map((s, i) => {
      const x = i * stepX;
      const ratio = Math.min(1, selector(s) / maxVal);
      const y = height - padY - ratio * (height - padY * 2);
      return `${x.toFixed(1)},${y.toFixed(1)}`;
    });
    return {
      line: `M ${pts.join(" L ")}`,
      area: `M 0,${height} L ${pts.join(" L ")} L ${width},${height} Z`,
    };
  };

  const down = buildPath((s) => s.rateRecv);
  const up = buildPath((s) => s.rateSent);

  return (
    <div>
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "space-between",
          fontSize: 11.5,
          color: "var(--text-muted)",
          marginBottom: 6,
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 14 }}>
          <span style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
            <span
              style={{
                width: 8,
                height: 8,
                borderRadius: "50%",
                background: "var(--success)",
              }}
            />
            {downLabel}
          </span>
          <span style={{ display: "inline-flex", alignItems: "center", gap: 6 }}>
            <span
              style={{
                width: 8,
                height: 8,
                borderRadius: "50%",
                background: "var(--accent)",
              }}
            />
            {upLabel}
          </span>
        </div>
        <span className="mono">Peak {formatRate(maxVal)}</span>
      </div>

      <svg
        viewBox={`0 0 ${width} ${height}`}
        preserveAspectRatio="none"
        style={{
          width: "100%",
          height: 96,
          display: "block",
          borderRadius: 8,
          background: "var(--bg-input)",
          border: "1px solid var(--border-subtle)",
        }}
      >
        <defs>
          <linearGradient id="downGrad" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="var(--success)" stopOpacity="0.28" />
            <stop offset="100%" stopColor="var(--success)" stopOpacity="0.02" />
          </linearGradient>
          <linearGradient id="upGrad" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="var(--accent)" stopOpacity="0.24" />
            <stop offset="100%" stopColor="var(--accent)" stopOpacity="0.02" />
          </linearGradient>
        </defs>
        <line
          x1="0"
          y1={height * 0.5}
          x2={width}
          y2={height * 0.5}
          stroke="var(--border-subtle)"
          strokeDasharray="4 4"
        />
        <path d={down.area} fill="url(#downGrad)" />
        <path d={up.area} fill="url(#upGrad)" />
        <path d={down.line} fill="none" stroke="var(--success)" strokeWidth="2" />
        <path d={up.line} fill="none" stroke="var(--accent)" strokeWidth="2" />
      </svg>
    </div>
  );
};
