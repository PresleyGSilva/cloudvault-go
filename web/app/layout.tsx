import "./globals.css";
export const metadata = { title: "CloudVault", description: "Secure file vault" };
export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>
        <header className="bg-cyan-700 text-white">
          <div className="mx-auto max-w-6xl px-4 py-3 flex items-center gap-2">
            <span className="text-xl font-bold">CloudVault</span>
            <span className="text-cyan-200 text-sm">secure file vault</span>
          </div>
        </header>
        <main className="mx-auto max-w-6xl px-4 py-6">{children}</main>
      </body>
    </html>
  );
}
