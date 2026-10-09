"use client";
export const API_URL = process.env.NEXT_PUBLIC_API_URL || "http://localhost:8087";

export function token() { return typeof window !== "undefined" ? localStorage.getItem("cv_token") || "" : ""; }
export function orgId() { return typeof window !== "undefined" ? localStorage.getItem("cv_org") || "" : ""; }
export function orgs(): { id: string; name: string; role: string }[] {
  try { return JSON.parse(localStorage.getItem("cv_orgs") || "[]"); } catch { return []; }
}
export function persistSession(accessToken: string, all: { id: string; name: string; role: string }[]) {
  localStorage.setItem("cv_token", accessToken);
  localStorage.setItem("cv_orgs", JSON.stringify(all));
  if (all.length > 0 && !localStorage.getItem("cv_org")) localStorage.setItem("cv_org", all[0].id);
}
export function logout() { localStorage.removeItem("cv_token"); }

export async function api(path: string, opts: RequestInit = {}) {
  const headers: Record<string, string> = { "Content-Type": "application/json", ...(opts.headers as Record<string, string> || {}) };
  const t = token();
  if (t) headers["Authorization"] = "Bearer " + t;
  const o = orgId();
  if (o) { headers["X-Org-ID"] = o; headers["X-User"] = "web"; }
  const res = await fetch(API_URL + path, { ...opts, headers, credentials: "include" });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(body?.error?.message || ("HTTP " + res.status));
  return body.data;
}
