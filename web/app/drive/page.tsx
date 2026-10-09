"use client";
import { useCallback, useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { api, logout, orgId, orgs, token } from "../../lib/api";

type Folder = { id: string; parentId: string | null; name: string };
type FileItem = { id: string; folderId: string | null; name: string; mime: string; kind: string; sizeBytes: number };

function fmtBytes(n: number) {
  if (n < 1024) return n + " B";
  if (n < 1048576) return (n / 1024).toFixed(1) + " KB";
  if (n < 1073741824) return (n / 1048576).toFixed(1) + " MB";
  return (n / 1073741824).toFixed(2) + " GB";
}

export default function Drive() {
  const router = useRouter();
  const [folders, setFolders] = useState<Folder[]>([]);
  const [files, setFiles] = useState<FileItem[]>([]);
  const [crumbs, setCrumbs] = useState<Folder[]>([]);
  const [current, setCurrent] = useState<string>("");
  const [q, setQ] = useState("");
  const [kind, setKind] = useState("all");
  const [trash, setTrash] = useState(false);
  const [quota, setQuota] = useState<{ usedBytesTotal: number; quotaBytes: number; percent: number } | null>(null);
  const [activities, setActivities] = useState<{ action: string; actor: string; createdAt: string }[]>([]);
  const [members, setMembers] = useState<{ id: string; email: string; role: string }[]>([]);
  const [err, setErr] = useState("");
  const [preview, setPreview] = useState<{ url: string; mime: string; name: string } | null>(null);
  const [share, setShare] = useState<string>("");

  const load = useCallback(async () => {
    setErr("");
    try {
      const [f, fl, qq, act, mem] = await Promise.all([
        api("/api/v1/folders" + (current ? "?parentId=" + current : "") + (trash ? (current ? "&trash=true" : "?trash=true") : "")),
        api("/api/v1/files?folderId=" + current + "&q=" + encodeURIComponent(q) + "&type=" + kind + (trash ? "&trash=true" : "")),
        api("/api/v1/quota"),
        api("/api/v1/activities"),
        api("/api/v1/members"),
      ]);
      setFolders(f); setFiles(fl); setQuota(qq); setActivities(act); setMembers(mem);
    } catch (e) { setErr((e as Error).message); }
  }, [current, q, kind, trash]);

  useEffect(() => {
    if (!token()) { router.replace("/login"); return; }
    load();
  }, [router, load]);

  async function createFolder() {
    const name = prompt("Folder name:");
    if (!name) return;
    try { await api("/api/v1/folders", { method: "POST", body: JSON.stringify({ name, parentId: current || undefined }) }); load(); }
    catch (e) { alert((e as Error).message); }
  }

  async function upload(ev: React.ChangeEvent<HTMLInputElement>) {
    const f = ev.target.files?.[0];
    if (!f) return;
    try {
      if (f.size <= 12 * 1024 * 1024) {
        const fd = new FormData();
        fd.append("file", f);
        fd.append("filename", f.name);
        fd.append("contentType", f.type || "application/octet-stream");
        if (current) fd.append("folderId", current);
        const headers: Record<string, string> = {};
        const t = token(); const o = orgId();
        if (t) headers["Authorization"] = "Bearer " + t;
        if (o) headers["X-Org-ID"] = o;
        const { API_URL } = await import("../../lib/api");
        const res = await fetch(API_URL + "/api/v1/files/upload", { method: "POST", headers, body: fd });
        if (!res.ok) throw new Error((await res.json().catch(() => ({})))?.error?.message || "upload failed");
      } else {
        const p: { uploadUrl: string; s3Key: string } = await api("/api/v1/files/presign", {
          method: "POST",
          body: JSON.stringify({ folderId: current || undefined, filename: f.name, contentType: f.type || "application/octet-stream", sizeBytes: f.size }),
        });
        const put = await fetch(p.uploadUrl, { method: "PUT", headers: { "Content-Type": f.type || "application/octet-stream" }, body: f });
        if (!put.ok) throw new Error("direct upload to storage failed");
        await api("/api/v1/files/complete", {
          method: "POST",
          body: JSON.stringify({ folderId: current || undefined, filename: f.name, contentType: f.type || "application/octet-stream", s3Key: p.s3Key, sizeBytes: f.size }),
        });
      }
      load();
    } catch (e) { alert((e as Error).message); }
    ev.target.value = "";
  }

  async function download(id: string) {
    const d: { url: string } = await api("/api/v1/files/" + id + "/download");
    window.open(d.url, "_blank");
  }

  async function showPreview(f: FileItem) {
    try {
      const p: { url: string; mime: string } = await api("/api/v1/files/" + f.id + "/preview");
      setPreview({ url: p.url, mime: p.mime, name: f.name });
    } catch (e) { alert((e as Error).message); }
  }

  async function renameFile(f: FileItem) {
    const name = prompt("New name:", f.name);
    if (!name) return;
    try { await api("/api/v1/files/" + f.id, { method: "PATCH", body: JSON.stringify({ name }) }); load(); }
    catch (e) { alert((e as Error).message); }
  }

  async function moveFile(f: FileItem) {
    const dest = prompt("Destination folder id (empty = root):", f.folderId || "");
    if (dest === null) return;
    try { await api("/api/v1/files/" + f.id, { method: "PATCH", body: JSON.stringify({ folderId: dest || null }) }); load(); }
    catch (e) { alert((e as Error).message); }
  }

  async function trashFile(id: string, permanent: boolean) {
    if (!confirm(permanent ? "Delete permanently?" : "Move to trash?")) return;
    await api("/api/v1/files/" + id + (permanent ? "?permanent=true" : ""), { method: "DELETE" });
    load();
  }

  async function restoreFile(id: string) {
    await api("/api/v1/files/" + id + "/restore", { method: "POST" });
    load();
  }

  async function shareFile(id: string) {
    try {
      const s: { token: string } = await api("/api/v1/files/" + id + "/shares", { method: "POST", body: JSON.stringify({ expiresInHours: 24 }) });
      setShare(window.location.origin + "/s/" + s.token);
    } catch (e) { alert((e as Error).message); }
  }

  async function grant(id: string, isFolder: boolean) {
    const email = prompt("Grantee email (must be org member):");
    if (!email) return;
    const m = members.find((x) => x.email === email);
    if (!m) { alert("No org member with that email"); return; }
    const level = prompt("Level (viewer/editor):", "viewer") || "viewer";
    const base = isFolder ? "/api/v1/folders/" : "/api/v1/files/";
    try { await api(base + id + "/permissions", { method: "PUT", body: JSON.stringify({ userId: m.id, level }) }); alert("Granted"); }
    catch (e) { alert((e as Error).message); }
  }

  function enter(f: Folder) { setCrumbs([...crumbs, f]); setCurrent(f.id); }
  function up() { const c = [...crumbs]; c.pop(); setCrumbs(c); setCurrent(c.length ? c[c.length - 1].id : ""); }

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2 rounded bg-white p-3 shadow">
        <button onClick={() => { setTrash(!trash); }} className={"rounded px-3 py-1 text-sm " + (trash ? "bg-slate-700 text-white" : "bg-slate-200")}>
          {trash ? "Trash (on)" : "Trash"}
        </button>
        <select value={kind} onChange={(e) => setKind(e.target.value)} className="rounded border p-1 text-sm">
          <option value="all">all types</option>
          <option value="image">images</option>
          <option value="video">video</option>
          <option value="audio">audio</option>
          <option value="pdf">PDF</option>
          <option value="doc">docs</option>
          <option value="other">other</option>
        </select>
        <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search files…" className="rounded border p-1 text-sm" />
        <button onClick={createFolder} className="rounded bg-cyan-700 px-3 py-1 text-sm text-white">+ Folder</button>
        <label className="cursor-pointer rounded bg-cyan-700 px-3 py-1 text-sm text-white">
          Upload
          <input type="file" className="hidden" onChange={upload} />
        </label>
        <span className="ml-auto text-xs text-slate-500">orgs: {orgs().map((o) => o.name).join(", ")}</span>
        <button onClick={() => { logout(); router.push("/login"); }} className="text-xs text-slate-500 underline">logout</button>
      </div>

      {err && <p className="text-sm text-red-600">{err}</p>}
      {share && (
        <div className="rounded bg-cyan-50 p-3 text-sm">
          Share link (24h): <a className="text-cyan-700 underline" href={share}>{share}</a>
          <button className="ml-2 underline" onClick={() => setShare("")}>close</button>
        </div>
      )}

      {quota && (
        <div className="rounded bg-white p-3 shadow">
          <div className="flex justify-between text-xs text-slate-600">
            <span>Quota: {fmtBytes(quota.usedBytesTotal)} / {fmtBytes(quota.quotaBytes)}</span>
            <span>{quota.percent.toFixed(1)}%</span>
          </div>
          <div className="mt-1 h-2 rounded bg-slate-200">
            <div className="h-2 rounded bg-cyan-600" style={{ width: Math.min(100, quota.percent) + "%" }} />
          </div>
        </div>
      )}

      <div className="rounded bg-white p-3 shadow">
        <div className="mb-2 text-sm text-slate-500">
          <button className="underline" onClick={() => { setCrumbs([]); setCurrent(""); }}>root</button>
          {crumbs.map((c) => (<span key={c.id}> / {c.name}</span>))}
          {crumbs.length > 0 && <button className="ml-2 underline" onClick={up}>up</button>}
        </div>
        <div className="flex flex-wrap gap-2">
          {folders.map((f) => (
            <div key={f.id} className="flex items-center gap-1 rounded border border-slate-200 px-2 py-1 text-sm">
              <button className="font-medium text-cyan-800" onClick={() => enter(f)}>{f.name}</button>
              <button title="grant access" className="text-xs text-slate-400" onClick={() => grant(f.id, true)}>🔑</button>
            </div>
          ))}
          {folders.length === 0 && <span className="text-sm text-slate-400">No folders</span>}
        </div>
      </div>

      <div className="overflow-x-auto rounded bg-white shadow">
        <table className="w-full text-sm">
          <thead>
            <tr className="bg-slate-800 text-left text-white">
              <th className="p-2">Name</th><th className="p-2">Type</th><th className="p-2">Size</th><th className="p-2">Actions</th>
            </tr>
          </thead>
          <tbody>
            {files.map((f) => (
              <tr key={f.id} className="border-t">
                <td className="p-2 font-medium">{f.name}</td>
                <td className="p-2 text-slate-500">{f.kind} · {f.mime}</td>
                <td className="p-2 text-slate-500">{fmtBytes(f.sizeBytes)}</td>
                <td className="space-x-1 p-2">
                  {!trash ? (
                    <>
                      <button className="text-cyan-700 underline" onClick={() => download(f.id)}>get</button>
                      {(f.kind === "image" || f.kind === "pdf") && <button className="text-cyan-700 underline" onClick={() => showPreview(f)}>preview</button>}
                      <button className="text-cyan-700 underline" onClick={() => renameFile(f)}>rename</button>
                      <button className="text-cyan-700 underline" onClick={() => moveFile(f)}>move</button>
                      <button className="text-cyan-700 underline" onClick={() => shareFile(f.id)}>share</button>
                      <button className="text-cyan-700 underline" onClick={() => grant(f.id, false)}>grant</button>
                      <button className="text-red-600 underline" onClick={() => trashFile(f.id, false)}>trash</button>
                    </>
                  ) : (
                    <>
                      <button className="text-cyan-700 underline" onClick={() => restoreFile(f.id)}>restore</button>
                      <button className="text-red-600 underline" onClick={() => trashFile(f.id, true)}>delete</button>
                    </>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {files.length === 0 && <p className="p-3 text-sm text-slate-400">No files</p>}
      </div>

      <div className="rounded bg-white p-3 shadow">
        <h2 className="font-semibold text-slate-700">Activity</h2>
        <ul className="mt-1 max-h-48 overflow-y-auto text-xs text-slate-600">
          {activities.map((a, i) => (<li key={i}>{a.createdAt} — {a.action} ({a.actor})</li>))}
        </ul>
      </div>

      {preview && (
        <div className="fixed inset-0 flex items-center justify-center bg-black/60 p-4" onClick={() => setPreview(null)}>
          <div className="max-h-[90vh] max-w-3xl overflow-auto rounded bg-white p-3" onClick={(e) => e.stopPropagation()}>
            <div className="mb-2 flex justify-between">
              <span className="text-sm font-medium">{preview.name}</span>
              <button className="text-sm underline" onClick={() => setPreview(null)}>close</button>
            </div>
            {preview.mime.startsWith("image/") ? (
              // eslint-disable-next-line @next/next/no-img-element
              <img src={preview.url} alt={preview.name} className="max-h-[75vh]" />
            ) : (
              <iframe src={preview.url} className="h-[75vh] w-[70vw]" title={preview.name} />
            )}
          </div>
        </div>
      )}
    </div>
  );
}
