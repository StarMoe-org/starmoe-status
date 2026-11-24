import type { Metadata, Viewport } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "StarMoe Status · 服务状态监控",
  description: "StarMoe 平台、MoeSekai (PJSK) 与 MoeNotes (bdon) 实时可用性、双线路状态与故障公告",
};

export const viewport: Viewport = {
  width: "device-width",
  initialScale: 1,
  viewportFit: "cover",
  themeColor: [
    { media: "(prefers-color-scheme: dark)", color: "#05071a" },
    { media: "(prefers-color-scheme: light)", color: "#f8f1e6" },
  ],
};

const THEME_INIT_SCRIPT = `(function(){try{var t=localStorage.getItem("starmoe-theme");if(t==="night"||t==="dawn"){document.documentElement.dataset.theme=t;}}catch(e){}})();`;

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="zh-CN" suppressHydrationWarning>
      <head>
        <script dangerouslySetInnerHTML={{ __html: THEME_INIT_SCRIPT }} />
        <link rel="alternate" type="application/rss+xml" title="StarMoe Status RSS" href="/rss.xml" />
      </head>
      <body className="sm-sky sm-sky--fixed min-h-screen flex flex-col">
        {children}
      </body>
    </html>
  );
}
