import React, { useEffect, useRef, useState } from "react";
import {
  Clock,
  ExternalLink,
  Globe,
  KeyRound,
  MousePointerClick,
  RotateCcw,
  ShieldAlert,
  Trash2,
} from "lucide-react";
import { AppService, type Challenge, type Point } from "../mygo";
import { type Dict, fmt } from "../i18n";
import { scaleClickPoint } from "../utils/format";

interface ClickMarker {
  /** Original image pixel coordinate sent back to core.Engine */
  orig: Point;
  /** Percentage position for overlay pin rendering */
  pctX: number;
  pctY: number;
}

interface ChallengeModalProps {
  challenge: Challenge;
  queueTotal?: number;
  dict: Dict;
  onError: (msg: string) => void;
}

export const ChallengeModal: React.FC<ChallengeModalProps> = ({
  challenge,
  queueTotal = 1,
  dict,
  onError,
}) => {
  const [code, setCode] = useState("");
  const [skipSecondary, setSkipSecondary] = useState(false);
  const [callbackUrl, setCallbackUrl] = useState("");
  const [markers, setMarkers] = useState<ClickMarker[]>([]);
  const [naturalSize, setNaturalSize] = useState<{ w: number; h: number }>({
    w: challenge.imageWidth || 0,
    h: challenge.imageHeight || 0,
  });
  const [submitting, setSubmitting] = useState(false);
  const [secondsLeft, setSecondsLeft] = useState<number>(() =>
    computeRemainingSeconds(challenge.expiresAt),
  );

  const imgRef = useRef<HTMLImageElement | null>(null);

  useEffect(() => {
    setCode("");
    setSkipSecondary(false);
    setCallbackUrl("");
    setMarkers([]);
    setNaturalSize({
      w: challenge.imageWidth || 0,
      h: challenge.imageHeight || 0,
    });
    setSubmitting(false);
    setSecondsLeft(computeRemainingSeconds(challenge.expiresAt));
  }, [challenge.id, challenge.expiresAt, challenge.imageWidth, challenge.imageHeight]);

  useEffect(() => {
    const id = window.setInterval(() => {
      setSecondsLeft(computeRemainingSeconds(challenge.expiresAt));
    }, 1000);
    return () => window.clearInterval(id);
  }, [challenge.expiresAt]);

  const title = (() => {
    switch (challenge.type) {
      case "code":
        return (
          (challenge.codeKind && dict.challenge.codeTitles[challenge.codeKind]) ||
          dict.challenge.title
        );
      case "text_captcha":
        return dict.challenge.textCaptchaTitle;
      case "click_captcha":
        return dict.challenge.clickCaptchaTitle;
      case "external_login":
        return (
          (challenge.externalKind &&
            dict.challenge.externalTitles[challenge.externalKind]) ||
          dict.challenge.title
        );
      default:
        return dict.challenge.title;
    }
  })();

  const imageSrc =
    challenge.imageBase64
      ? `data:${challenge.imageMime || "image/png"};base64,${challenge.imageBase64}`
      : "";

  const handleImageClick = (e: React.MouseEvent<HTMLImageElement>) => {
    const img = imgRef.current;
    if (!img) return;
    const rect = img.getBoundingClientRect();
    const origW = challenge.imageWidth || img.naturalWidth || naturalSize.w;
    const origH = challenge.imageHeight || img.naturalHeight || naturalSize.h;
    if (origW <= 0 || origH <= 0 || rect.width <= 0 || rect.height <= 0) return;

    const orig = scaleClickPoint(e.clientX, e.clientY, rect, origW, origH);
    const pctX = Math.min(Math.max(((e.clientX - rect.left) / rect.width) * 100, 0), 100);
    const pctY = Math.min(Math.max(((e.clientY - rect.top) / rect.height) * 100, 0), 100);
    setMarkers((prev) => [...prev, { orig, pctX, pctY }]);
  };

  const handleSubmit = async (e?: React.FormEvent) => {
    e?.preventDefault();
    if (submitting) return;
    setSubmitting(true);
    try {
      if (challenge.type === "code") {
        await AppService.submitChallenge({
          id: challenge.id,
          code: code.trim(),
          skipSecondaryAuth: skipSecondary,
        });
      } else if (challenge.type === "text_captcha") {
        await AppService.submitChallenge({
          id: challenge.id,
          code: code.trim(),
        });
      } else if (challenge.type === "click_captcha") {
        if (markers.length === 0) {
          onError(dict.challenge.clickEmpty);
          setSubmitting(false);
          return;
        }
        const origW = challenge.imageWidth || naturalSize.w;
        const origH = challenge.imageHeight || naturalSize.h;
        await AppService.submitChallenge({
          id: challenge.id,
          points: markers.map((m) => m.orig),
          width: origW,
          height: origH,
        });
      } else if (challenge.type === "external_login") {
        await AppService.submitChallenge({
          id: challenge.id,
          callbackUrl: callbackUrl.trim(),
        });
      }
    } catch (err) {
      onError(String(err));
      setSubmitting(false);
    }
  };

  const handleCancel = async () => {
    try {
      await AppService.cancelChallenge(challenge.id);
    } catch (err) {
      onError(String(err));
    }
  };

  const handleReopenNative = async () => {
    if (!challenge.loginUrl) return;
    try {
      await AppService.openExternalLoginWindow(
        challenge.id,
        challenge.loginUrl,
        challenge.externalKind || "cas",
      );
    } catch (err) {
      onError(String(err));
    }
  };

  const handleOpenBrowser = async () => {
    if (!challenge.loginUrl) return;
    try {
      await AppService.openExternalURL(challenge.loginUrl);
    } catch (err) {
      onError(String(err));
    }
  };

  const canSubmit = (() => {
    if (submitting || secondsLeft <= 0) return false;
    switch (challenge.type) {
      case "code":
        return skipSecondary || code.trim().length > 0;
      case "text_captcha":
        return code.trim().length > 0;
      case "click_captcha":
        return markers.length > 0;
      case "external_login":
        return callbackUrl.trim().length > 0;
      default:
        return false;
    }
  })();

  return (
    <div className="modal-backdrop">
      <div className="modal-panel" role="dialog" aria-modal="true">
        <div className="modal-header">
          <div style={{ display: "flex", alignItems: "center", gap: 8, flexWrap: "wrap" }}>
            <h3 className="modal-title">
              {challenge.type === "click_captcha" ? (
                <MousePointerClick size={18} color="var(--accent)" />
              ) : challenge.type === "external_login" ? (
                <Globe size={18} color="var(--accent)" />
              ) : (
                <KeyRound size={18} color="var(--accent)" />
              )}
              {title}
            </h3>
            {challenge.profileName && (
              <span className="badge badge-accent">{challenge.profileName}</span>
            )}
            {queueTotal > 1 && (
              <span className="badge badge-neutral">
                {fmt(dict.challenge.queueBadge, { current: 1, total: queueTotal })}
              </span>
            )}
          </div>
          <span
            className={`badge tabular ${secondsLeft <= 30 ? "badge-danger" : "badge-warning"}`}
          >
            <Clock size={12} />
            {secondsLeft > 0
              ? fmt(dict.challenge.expiresIn, { sec: secondsLeft })
              : dict.challenge.expired}
          </span>
        </div>

        <form onSubmit={handleSubmit}>
          <div className="modal-body">
            {challenge.message && (
              <div className="alert-banner alert-info">
                <ShieldAlert size={16} style={{ flexShrink: 0, marginTop: 1 }} />
                <span className="selectable">{challenge.message}</span>
              </div>
            )}

            {challenge.type === "code" && (
              <>
                <div className="field">
                  <label className="field-label">{title}</label>
                  <input
                    className="input mono"
                    type="text"
                    autoFocus
                    disabled={skipSecondary}
                    placeholder={dict.challenge.codePlaceholder}
                    value={code}
                    onChange={(e) => setCode(e.target.value)}
                  />
                </div>
                {challenge.canSkipSecondaryAuth && (
                  <label
                    style={{
                      display: "flex",
                      alignItems: "center",
                      gap: 8,
                      cursor: "pointer",
                      fontSize: 12.5,
                      color: "var(--text-secondary)",
                    }}
                  >
                    <input
                      type="checkbox"
                      checked={skipSecondary}
                      onChange={(e) => setSkipSecondary(e.target.checked)}
                    />
                    {dict.challenge.skipSecondary}
                  </label>
                )}
              </>
            )}

            {challenge.type === "text_captcha" && (
              <>
                {imageSrc && (
                  <div style={{ display: "flex", justifyContent: "center" }}>
                    <img
                      src={imageSrc}
                      alt="Captcha"
                      style={{
                        maxWidth: "100%",
                        maxHeight: 160,
                        borderRadius: 8,
                        border: "1px solid var(--border-strong)",
                        background: "#ffffff",
                      }}
                    />
                  </div>
                )}
                <div className="field">
                  <label className="field-label">{dict.challenge.textCaptchaTitle}</label>
                  <input
                    className="input mono"
                    type="text"
                    autoFocus
                    placeholder={dict.challenge.codePlaceholder}
                    value={code}
                    onChange={(e) => setCode(e.target.value)}
                  />
                </div>
              </>
            )}

            {challenge.type === "click_captcha" && (
              <>
                <div className="field-hint">{dict.challenge.clickHint}</div>
                {imageSrc && (
                  <div style={{ display: "flex", justifyContent: "center" }}>
                    <div className="captcha-stage">
                      <img
                        ref={imgRef}
                        src={imageSrc}
                        alt="Click Captcha"
                        onLoad={(e) => {
                          const el = e.currentTarget;
                          setNaturalSize({
                            w: challenge.imageWidth || el.naturalWidth,
                            h: challenge.imageHeight || el.naturalHeight,
                          });
                        }}
                        onClick={handleImageClick}
                      />
                      {markers.map((m, idx) => (
                        <div
                          key={`${m.orig.x}-${m.orig.y}-${idx}`}
                          className="captcha-pin"
                          style={{ left: `${m.pctX}%`, top: `${m.pctY}%` }}
                        >
                          {idx + 1}
                        </div>
                      ))}
                    </div>
                  </div>
                )}
                <div
                  style={{
                    display: "flex",
                    alignItems: "center",
                    justifyContent: "space-between",
                    gap: 8,
                    flexWrap: "wrap",
                  }}
                >
                  <span className="field-hint mono">
                    {fmt(dict.challenge.clickPoints, { count: markers.length })}
                    {markers.length > 0 &&
                      " · " + markers.map((m) => `(${m.orig.x},${m.orig.y})`).join(" ")}
                  </span>
                  <div style={{ display: "flex", gap: 6 }}>
                    <button
                      type="button"
                      className="btn btn-sm"
                      disabled={markers.length === 0}
                      onClick={() => setMarkers((prev) => prev.slice(0, -1))}
                    >
                      <RotateCcw size={13} />
                      {dict.common.undo}
                    </button>
                    <button
                      type="button"
                      className="btn btn-sm"
                      disabled={markers.length === 0}
                      onClick={() => setMarkers([])}
                    >
                      <Trash2 size={13} />
                      {dict.common.clear}
                    </button>
                  </div>
                </div>
              </>
            )}

            {challenge.type === "external_login" && (
              <>
                <div className="field-hint">{dict.challenge.externalDesc}</div>
                <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
                  <button
                    type="button"
                    className="btn btn-primary"
                    onClick={handleReopenNative}
                  >
                    <Globe size={15} />
                    {dict.challenge.openLoginWindow}
                  </button>
                  <button
                    type="button"
                    className="btn"
                    onClick={handleOpenBrowser}
                  >
                    <ExternalLink size={15} />
                    {dict.challenge.openSystemBrowser}
                  </button>
                </div>
                <div className="field" style={{ marginTop: 4 }}>
                  <label className="field-label">
                    {dict.challenge.manualCallbackLabel}
                  </label>
                  <input
                    className="input mono"
                    type="text"
                    placeholder={dict.challenge.manualCallbackPlaceholder}
                    value={callbackUrl}
                    onChange={(e) => setCallbackUrl(e.target.value)}
                  />
                </div>
              </>
            )}
          </div>

          <div className="modal-footer">
            <button type="button" className="btn btn-danger" onClick={handleCancel}>
              {dict.challenge.cancelAuth}
            </button>
            <button
              type="submit"
              className="btn btn-primary"
              disabled={!canSubmit}
            >
              {dict.common.submit}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
};

function computeRemainingSeconds(expiresAt: string): number {
  const ts = Date.parse(expiresAt);
  if (Number.isNaN(ts)) return 300;
  return Math.max(0, Math.floor((ts - Date.now()) / 1000));
}
