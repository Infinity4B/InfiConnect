import type { Point } from "../mygo";

export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let val = bytes;
  let i = 0;
  while (val >= 1024 && i < units.length - 1) {
    val /= 1024;
    i++;
  }
  return `${val >= 100 || i === 0 ? val.toFixed(0) : val.toFixed(1)} ${units[i]}`;
}

export function formatRate(bytesPerSec: number): string {
  return `${formatBytes(bytesPerSec)}/s`;
}

export function formatDuration(connectedAt?: string, nowMs: number = Date.now()): string {
  if (!connectedAt) return "00:00:00";
  const start = Date.parse(connectedAt);
  if (Number.isNaN(start)) return "00:00:00";
  const elapsed = Math.max(0, Math.floor((nowMs - start) / 1000));
  const hrs = Math.floor(elapsed / 3600);
  const mins = Math.floor((elapsed % 3600) / 60);
  const secs = elapsed % 60;
  return [hrs, mins, secs].map((n) => String(n).padStart(2, "0")).join(":");
}

export function formatTimestamp(iso?: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  const hh = String(d.getHours()).padStart(2, "0");
  const mm = String(d.getMinutes()).padStart(2, "0");
  const ss = String(d.getSeconds()).padStart(2, "0");
  const ms = String(d.getMilliseconds()).padStart(3, "0");
  return `${hh}:${mm}:${ss}.${ms}`;
}

/**
 * Converts click coordinates on a rendered <img> element back into the
 * original image's pixel space, clamped strictly within [0, origWidth/origHeight].
 */
export function scaleClickPoint(
  clientX: number,
  clientY: number,
  rect: DOMRect,
  origWidth: number,
  origHeight: number,
): Point {
  if (rect.width <= 0 || rect.height <= 0 || origWidth <= 0 || origHeight <= 0) {
    return { x: 0, y: 0 };
  }
  const relX = (clientX - rect.left) / rect.width;
  const relY = (clientY - rect.top) / rect.height;
  const x = Math.round(Math.min(Math.max(relX, 0), 1) * origWidth);
  const y = Math.round(Math.min(Math.max(relY, 0), 1) * origHeight);
  return { x, y };
}
