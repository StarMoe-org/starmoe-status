import type {
  AdminOverview,
  CreateIncidentRequest,
  Incident,
  MonitorHistory,
  PublicSummary,
  TestAlertRequest,
  TestAlertResponse,
  TimeRange,
  UpdateIncidentRequest,
} from "./types";

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

function buildHeaders(devToken?: string, isJson = false): HeadersInit {
  const headers: Record<string, string> = {};
  if (isJson) {
    headers["Content-Type"] = "application/json";
  }
  if (devToken && devToken.trim() !== "") {
    headers["Authorization"] = `Bearer ${devToken.trim()}`;
  }
  return headers;
}

async function handleResponse<T>(res: Response): Promise<T> {
  if (!res.ok) {
    let msg = `请求失败 (${res.status})`;
    try {
      const data = await res.json();
      if (data && typeof data.error === "string") {
        msg = data.error;
      }
    } catch {
      // ignore non-json error body
    }
    throw new ApiError(res.status, msg);
  }
  return (await res.json()) as T;
}

export async function fetchSummary(range: TimeRange = "90d"): Promise<PublicSummary> {
  const res = await fetch(`/api/v1/summary?range=${encodeURIComponent(range)}`, {
    cache: "no-store",
  });
  return handleResponse<PublicSummary>(res);
}

export async function fetchMonitorHistory(id: string): Promise<MonitorHistory> {
  const res = await fetch(`/api/v1/monitors/${encodeURIComponent(id)}/history`, {
    cache: "no-store",
  });
  return handleResponse<MonitorHistory>(res);
}

export async function fetchIncidents(): Promise<Incident[]> {
  const res = await fetch("/api/v1/incidents", { cache: "no-store" });
  const data = await handleResponse<{ incidents: Incident[] }>(res);
  return data.incidents ?? [];
}

export async function fetchAdminOverview(devToken?: string): Promise<AdminOverview> {
  const res = await fetch("/api/admin/overview", {
    cache: "no-store",
    headers: buildHeaders(devToken),
  });
  return handleResponse<AdminOverview>(res);
}

export async function createIncident(
  payload: CreateIncidentRequest,
  devToken?: string
): Promise<Incident> {
  const res = await fetch("/api/admin/incidents", {
    method: "POST",
    headers: buildHeaders(devToken, true),
    body: JSON.stringify(payload),
  });
  return handleResponse<Incident>(res);
}

export async function updateIncident(
  id: string,
  payload: UpdateIncidentRequest,
  devToken?: string
): Promise<Incident> {
  const res = await fetch(`/api/admin/incidents/${encodeURIComponent(id)}`, {
    method: "PATCH",
    headers: buildHeaders(devToken, true),
    body: JSON.stringify(payload),
  });
  return handleResponse<Incident>(res);
}

export async function sendTestAlert(
  payload: TestAlertRequest,
  devToken?: string
): Promise<TestAlertResponse> {
  const res = await fetch("/api/admin/alerts/test", {
    method: "POST",
    headers: buildHeaders(devToken, true),
    body: JSON.stringify(payload),
  });
  return handleResponse<TestAlertResponse>(res);
}
