"use client";
import { useEffect, useState } from "react";
import { useParams } from "next/navigation";
import { API_URL } from "../../../lib/api";

export default function Share() {
  const params = useParams<{ token: string }>();
  const [info, setInfo] = useState<{ type: string; name: string; mime?: string; expiresAt: string } | null>(null);
  const [err, setErr] = useState("");
  const [url, setUrl] = useState("");

  useEffect(() => {
    fetch(API_URL + "/api/v1/s/" + params.token)
      .then(async (r) => {
        const b = await r.json().catch(() => ({}));
        if (!r.ok) throw new Error(b?.error?.message || "HTTP " + r.status);
        setInfo(b.data);
      })
      .catch((e) => setErr((e as Error).message));
  }, [params.token]);

  async function getFile() {
    const r = await fetch(API_URL + "/api/v1/s/" + params.token + "/download");
    const b = await r.json().catch(() => ({}));
    if (!r.ok) { setErr(b?.error?.message || "failed"); return; }
    setUrl(b.data.url);
    window.open(b.data.url, "_blank");
  }

  if (err) return <div className="rounded bg-white p-6 shadow"><p className="text-red-600">{err}</p><p className="mt-1 text-xs text-slate-500">Links expire after at most 7 days.</p></div>;
  if (!info) return <p className="text-slate-500">Loading shared item…</p>;
  return (
    <div className="rounded bg-white p-6 shadow">
      <h1 className="text-xl font-bold">{info.name}</h1>
      <p className="text-sm text-slate-500">{info.type} · expires {info.expiresAt}</p>
      {info.type === "file" && <button onClick={getFile} className="mt-3 rounded bg-cyan-700 px-4 py-2 text-white">Download</button>}
      {url && <p className="mt-2 break-all text-xs text-slate-400">{url}</p>}
    </div>
  );
}
