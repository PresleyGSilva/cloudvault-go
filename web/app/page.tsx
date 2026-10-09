"use client";
import { useEffect } from "react";
import { useRouter } from "next/navigation";
import { token } from "../lib/api";
export default function Home() {
  const router = useRouter();
  useEffect(() => { router.replace(token() ? "/drive" : "/login"); }, [router]);
  return <p className="text-slate-500">Loading…</p>;
}
