/**
 * Public entry point for the InfluAudit API contract types.
 *
 * `schema.d.ts` is generated from `openapi/influaudit.yaml` by
 * `pnpm --filter @influaudit/contracts generate` (openapi-typescript). Do not
 * edit it by hand. This file only re-exports the raw generated surface plus a
 * set of named aliases so consumers write `AuthResponse` instead of
 * `components["schemas"]["auth.AuthResponse"]`.
 */
import type { components, paths, operations } from "./schema";

export type { components, paths, operations };

type Schemas = components["schemas"];

// --- auth ---
export type RegisterRequest = Schemas["auth.RegisterRequest"];
export type LoginRequest = Schemas["auth.LoginRequest"];
export type LogoutRequest = Schemas["auth.LogoutRequest"];
export type RefreshRequest = Schemas["auth.RefreshRequest"];
export type AuthResponse = Schemas["auth.AuthResponse"];
export type UserResponse = Schemas["auth.UserResponse"];

// --- audits ---
export type SubmitAuditRequest = Schemas["audit.SubmitAuditRequest"];
export type AuditResponse = Schemas["audit.AuditResponse"];
export type PlatformResultResponse = Schemas["audit.PlatformResultResponse"];

// --- reports ---
export type Report = Schemas["report.Report"];
export type ReportScoreBlock = Schemas["report.ScoreBlock"];
export type ReportFraudBlock = Schemas["report.FraudBlock"];
export type ReportNarrative = Schemas["report.Narrative"];
export type PublishResult = Schemas["report.PublishResult"];
export type PublicBadge = Schemas["report.PublicBadge"];

// --- scoring ---
export type ScoreResponse = Schemas["scoring.ScoreResponse"];
export type ScoreHistoryResponse = Schemas["scoring.ScoreHistoryResponse"];
export type ScorePoint = Schemas["scoring.ScorePoint"];
export type FactorPresentation = Schemas["scoring.FactorPresentation"];
export type Subscore = Schemas["scoring.Subscore"];

// --- oauth ---
export type AuthorizeResponse = Schemas["oauth.AuthorizeResponse"];
export type ConnectionResponse = Schemas["oauth.ConnectionResponse"];
export type SignupStartRequest = Schemas["oauth.SignupStartRequest"];
export type AuthSession = Schemas["oauth.AuthSession"];

// --- public funnel (analytics + waitlist) ---
export type AnalyticsIngestRequest = Schemas["analytics.IngestRequest"];
export type WaitlistCaptureRequest = Schemas["waitlist.CaptureRequest"];

// --- influencers ---
export type InfluencerResponse = Schemas["influencer.InfluencerResponse"];
export type CreateInfluencerRequest = Schemas["influencer.CreateInfluencerRequest"];
export type HandleResponse = Schemas["influencer.HandleResponse"];

// --- admin ---
export type DisputeResponse = Schemas["admin.DisputeResponse"];
export type FileDisputeRequest = Schemas["admin.FileDisputeRequest"];
export type ResolveDisputeRequest = Schemas["admin.ResolveDisputeRequest"];
export type CostDashboardResponse = Schemas["admin.CostDashboardResponse"];
export type CostResponse = Schemas["admin.CostResponse"];
export type QueueMonitorResponse = Schemas["admin.QueueMonitorResponse"];
export type QueueSnapshot = Schemas["admin.QueueSnapshot"];
export type LabelExportResponse = Schemas["admin.LabelExportResponse"];
export type TrainingLabel = Schemas["admin.TrainingLabel"];
