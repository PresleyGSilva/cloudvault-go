"use client";
import { useState } from "react";
import { useRouter } from "next/navigation";
import { API_URL, persistSession } from "../../lib/api";

export default function Login() {
  const router = useRouter();
  const [mode, setMode] = useState<"login" | "register">("login");
  const [name, setName] = useState("");
  const [email, setEmail] = useState("demo@cloudvault.local");
  const [password, setPassword] = useState("Demo1234!");
  const [err, setErr] = useState("");

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setErr("");
    const path = mode === "login" ? "/api/v1/auth/login" : "/api/v1/auth/register";
    const payload = mode === "login" ? { email, password } : { name, email, password };
    const res = await fetch(API_URL + path, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      credentials: "include",
      body: JSON.stringify(payload),
    });
    const body = await res.json().catch(() => ({}));
    if (!res.ok) { setErr(body?.error?.message || "failed"); return; }
    persistSession(body.data.accessToken, body.data.orgs);
    router.push("/drive");
  }

  return (
    <div className="mx-auto max-w-md rounded bg-white p-6 shadow">
      <h1 className="text-2xl font-bold text-slate-800">{mode === "login" ? "Sign in" : "Create account"}</h1>
      <form onSubmit={submit} className="mt-4 space-y-3">
        {mode === "register" && (
          <input className="w-full rounded border p-2" placeholder="Name" value={name} onChange={(e) => setName(e.target.value)} required />
        )}
        <input className="w-full rounded border p-2" placeholder="Email" value={email} onChange={(e) => setEmail(e.target.value)} required />
        <input className="w-full rounded border p-2" placeholder="Password" type="password" value={password} onChange={(e) => setPassword(e.target.value)} required />
        {err && <p className="text-sm text-red-600">{err}</p>}
        <button className="w-full rounded bg-cyan-700 py-2 font-semibold text-white hover:bg-cyan-800" type="submit">
          {mode === "login" ? "Sign in" : "Register"}
        </button>
      </form>
      <button className="mt-3 text-sm text-cyan-700 underline" onClick={() => setMode(mode === "login" ? "register" : "login")}>
        {mode === "login" ? "Need an account? Register" : "Have an account? Sign in"}
      </button>
    </div>
  );
}
