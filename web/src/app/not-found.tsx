import Link from "next/link";
import { SiteHeader } from "@/components/SiteHeader";
import { SiteFooter } from "@/components/SiteFooter";

export default function NotFound() {
  return (
    <>
      <SiteHeader />
      <main className="flex-1 flex items-center justify-center px-4 py-16">
        <div className="sm-card p-8 max-w-md w-full text-center space-y-3">
          <div className="text-4xl font-bold font-mono-code text-[var(--brand)]">404</div>
          <h1 className="text-lg font-semibold text-[var(--text)]">页面未找到</h1>
          <p className="text-xs text-[var(--text-muted)]">
            您访问的页面不存在，可返回实时状态页查看 StarMoe 各项服务的运行情况。
          </p>
          <div className="pt-2">
            <Link
              href="/"
              className="inline-flex items-center px-4 py-2 rounded-full text-xs font-semibold bg-[var(--brand)] text-[var(--on-brand)]"
            >
              返回状态首页
            </Link>
          </div>
        </div>
      </main>
      <SiteFooter />
    </>
  );
}
