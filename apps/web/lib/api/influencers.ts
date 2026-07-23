import "server-only";
import type {
  InfluencerResponse,
  CreateInfluencerRequest,
  components,
} from "@influaudit/contracts";
import { backendFetch } from "@/lib/api/http";

type ListInfluencersResponse =
  components["schemas"]["influencer.ListInfluencersResponse"];

/**
 * GET /influencers — the caller's influencer profiles.
 *
 * NOTE: the OpenAPI spec models this operation with a JSON request body
 * (`ListInfluencersRequest`), but a GET request cannot carry a body under the
 * Fetch/undici runtime. We therefore issue a plain GET; pagination, if needed,
 * should move to query parameters on the backend. Flagged for the backend owner.
 */
export function listInfluencers(
  token: string,
): Promise<ListInfluencersResponse> {
  return backendFetch<ListInfluencersResponse>("/influencers", { token });
}

/** GET /influencers/{id} — a single influencer with its handles. */
export function getInfluencer(
  id: string,
  token: string,
): Promise<InfluencerResponse> {
  return backendFetch<InfluencerResponse>(
    `/influencers/${encodeURIComponent(id)}`,
    { token },
  );
}

/** POST /influencers — create an influencer profile to audit. */
export function createInfluencer(
  body: CreateInfluencerRequest,
  token: string,
): Promise<InfluencerResponse> {
  return backendFetch<InfluencerResponse>("/influencers", {
    method: "POST",
    body,
    token,
  });
}
