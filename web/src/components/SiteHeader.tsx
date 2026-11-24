"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { ArrowLeft, Rss } from "lucide-react";
import { StarMoeLogo } from "./StarMoeLogo";
import { ThemeToggle } from "./ThemeToggle";

export function SiteHeader() {
  const pathname = usePathname() || "/";
  const isAdmin = pathname.startsWith("/admin");

  return (
    <header className="w-full pt-5 pb-2">
      <div className="max-w-4xl mx-auto px-4 sm:px-6 flex items-center justify-between gap-3">
        <Link href="/" className="flex items-center gap-3 group shrink-0 min-w-0">
          <StarMoeLogo className="h-[30px] w-auto" />
          <span className="pl-3 border-l border-[var(--line-divider)] text-sm font-medium text-[var(--text-muted)] whitespace-nowrap">
            {isAdmin ? "Status 运维台" : "Status"}
          </span>
        </Link>

        <div className="flex items-center gap-2 shrink-0">
          {isAdmin && (
            <Link
              href="/"
              className="inline-flex items-center gap-1.5 h-8 px-3 rounded-full border border-[var(--line-divider)] bg-[var(--card)] backdrop-blur-md text-xs font-medium text-[var(--text-muted)] hover:text-[var(--text)] hover:bg-[var(--overlay-hover)] transition-colors"
            >
              <ArrowLeft className="w-3.5 h-3.5" />
              返回状态页
            </Link>
          )}

          <a
            href="/rss.xml"
            target="_blank"
            rel="noopener noreferrer"
            title="RSS 订阅故障与维护公告"
            aria-label="RSS 订阅故障与维护公告"
            className="inline-flex items-center justify-center h-8 w-8 rounded-full border border-[var(--line-divider)] bg-[var(--card)] backdrop-blur-md text-[var(--text-muted)] hover:text-[var(--text)] hover:bg-[var(--overlay-hover)] transition-colors"
          >
            <Rss className="w-3.5 h-3.5" />
          </a>

          <ThemeToggle />
        </div>
      </div>
    </header>
  );
}
