"use client";

import { Monitor, Moon, Sun } from "lucide-react";
import { useEffect, useState } from "react";

type ThemePref = "system" | "dawn" | "night";

const STORAGE_KEY = "starmoe-theme";

function applyTheme(pref: ThemePref) {
  if (pref === "night" || pref === "dawn") {
    document.documentElement.dataset.theme = pref;
  } else {
    delete document.documentElement.dataset.theme;
  }
}

export function ThemeToggle() {
  const [pref, setPref] = useState<ThemePref>("system");
  const [mounted, setMounted] = useState(false);

  useEffect(() => {
    try {
      const saved = localStorage.getItem(STORAGE_KEY);
      if (saved === "night" || saved === "dawn") {
        setPref(saved);
        applyTheme(saved);
      } else {
        setPref("system");
        applyTheme("system");
      }
    } catch {
      setPref("system");
    }
    setMounted(true);
  }, []);

  const select = (next: ThemePref) => {
    setPref(next);
    applyTheme(next);
    try {
      if (next === "system") {
        localStorage.removeItem(STORAGE_KEY);
      } else {
        localStorage.setItem(STORAGE_KEY, next);
      }
    } catch {
      // ignore storage errors
    }
  };

  const options: { key: ThemePref; label: string; icon: typeof Monitor }[] = [
    { key: "system", label: "跟随系统", icon: Monitor },
    { key: "dawn", label: "晨光浅色", icon: Sun },
    { key: "night", label: "星夜深色", icon: Moon },
  ];

  return (
    <div
      role="group"
      aria-label="主题切换"
      className="inline-flex items-center p-0.5 rounded-full border border-[var(--line-divider)] bg-[var(--card)] backdrop-blur-md"
    >
      {options.map(({ key, label, icon: Icon }) => {
        const active = mounted && pref === key;
        return (
          <button
            key={key}
            type="button"
            onClick={() => select(key)}
            aria-label={label}
            aria-pressed={active}
            title={label}
            className={`inline-flex items-center justify-center h-7 w-7 rounded-full transition-colors cursor-pointer ${
              active
                ? "bg-[var(--brand-wash)] text-[var(--brand)] shadow-xs"
                : "text-[var(--text-muted)] hover:text-[var(--text)] hover:bg-[var(--overlay-hover)]"
            }`}
          >
            <Icon className="w-3.5 h-3.5" />
          </button>
        );
      })}
    </div>
  );
}
