import "server-only";
import type {
  ScoreResponse,
  ScoreHistoryResponse,
} from "@influaudit/contracts";
import { backendFetch } from "@/lib/api/http";

/** GET /influencers/{id}/score — the latest composite score. */
export function getLatestScore(
  influencerId: string,
  token: string,
): Promise<ScoreResponse> {
  return backendFetch<ScoreResponse>(
    `/influencers/${encodeURIComponent(influencerId)}/score`,
    { token },
  );
}

/** GET /influencers/{id}/score/history — the score time series (trend). */
export function getScoreHistory(
  influencerId: string,
  token: string,
): Promise<ScoreHistoryResponse> {
  return backendFetch<ScoreHistoryResponse>(
    `/influencers/${encodeURIComponent(influencerId)}/score/history`,
    { token },
  );
}
