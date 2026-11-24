import { Rss } from "lucide-react";

export function SiteFooter() {
  return (
    <footer className="mt-auto border-t border-[var(--line-divider)] py-7">
      <div className="max-w-4xl mx-auto px-4 sm:px-6 flex flex-col sm:flex-row items-center justify-between gap-4 text-xs text-[var(--text-muted)]">
        <div className="flex items-center gap-2 flex-wrap justify-center sm:justify-start">
          <span className="font-medium text-[var(--text)]">StarMoe Status</span>
          <span>·</span>
          <span>独立故障域监测节点</span>
          <span>·</span>
          <a
            href="/rss.xml"
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-1 text-[var(--link)] hover:underline"
          >
            <Rss className="w-3 h-3" />
            RSS 订阅
          </a>
        </div>

        <div className="flex items-center gap-4">
          <a href="https://star.moe" target="_blank" rel="noopener noreferrer" className="hover:text-[var(--text)] transition-colors">
            StarMoe
          </a>
          <a href="https://pjsk.moe" target="_blank" rel="noopener noreferrer" className="hover:text-[var(--text)] transition-colors">
            MoeSekai
          </a>
          <a href="https://bdon.moe" target="_blank" rel="noopener noreferrer" className="hover:text-[var(--text)] transition-colors">
            MoeNotes
          </a>
        </div>
      </div>
    </footer>
  );
}
