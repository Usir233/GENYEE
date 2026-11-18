"use client";
import { BrandLogo } from "./brand-logo";
import { useRef, useState, useSyncExternalStore } from "react";
import { getThemeSnapshot, subscribeToTheme, toggleTheme } from "./theme";
import { siteNavigation, repositoryUrl, headerIcons, sanitizeExternalHttpUrl } from "../../shared/header";

const safeRepoUrl = sanitizeExternalHttpUrl(repositoryUrl);

function HeaderIcon({ name }: { name: string }) {
  return <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d={headerIcons[name]} /></svg>;
}
function ThemeToggle() {
  const dark = useSyncExternalStore(subscribeToTheme, getThemeSnapshot, () => false);
  const label = dark ? "切换到浅色" : "切换到深色";
  return <button className="gy-theme-toggle" type="button" role="switch" aria-checked={dark} aria-label={label} title={label} onClick={toggleTheme}><HeaderIcon name={dark ? "moon" : "sun"} /></button>;
}
export function Header() {
  const [open, setOpen] = useState(false);
  const [openSubmenu, setOpenSubmenu] = useState<string | null>(null);
  const menu = useRef<HTMLButtonElement>(null);
  const closeMenus = () => {
    setOpen(false);
    setOpenSubmenu(null);
  };
  return <header className="gy-header" onKeyDown={event => {
    if (event.key === "Escape") {
      if (openSubmenu) setOpenSubmenu(null);
      if (open) {
        setOpen(false);
        menu.current?.focus();
      }
    }
  }}>
    <div className="gy-header-inner">
      <a className="gy-brand" href="/" target="_self" aria-label="GENYEE 首页"><BrandLogo priority /></a>
      <nav id="main-navigation" className={`gy-navigation ${open ? "is-open" : ""}`} aria-label="主导航">
        {siteNavigation.map((item, idx) => {
          if (item.children?.length) {
            const submenuOpen = openSubmenu === item.href;
            const submenuId = `gy-submenu-${idx}`;
            return (
              <div
                key={item.href}
                className={`gy-nav-menu ${submenuOpen ? "is-open" : ""}`}
                onBlur={event => {
                  if (!event.currentTarget.contains(event.relatedTarget as Node | null)) {
                    setOpenSubmenu(current => (current === item.href ? null : current));
                  }
                }}
              >
                <a href={item.href} onClick={closeMenus}>{item.label}{item.badge && <span className="gy-new-label">{item.badge}</span>}</a>
                <button
                  type="button"
                  className="gy-nav-caret"
                  aria-label={`展开${item.label}子菜单`}
                  aria-expanded={submenuOpen}
                  aria-controls={submenuId}
                  onClick={() => setOpenSubmenu(submenuOpen ? null : item.href)}
                >
                  <HeaderIcon name="chevron" />
                </button>
                <div id={submenuId} className="gy-nav-dropdown">
                  {item.children.map(child => <a key={child.href} href={child.href} onClick={closeMenus}>{child.label}</a>)}
                </div>
              </div>
            );
          }
          return <a key={item.href} href={item.href} onClick={closeMenus}>{item.label}{item.badge && <span className="gy-new-label">{item.badge}</span>}</a>;
        })}
        {safeRepoUrl && <a className="gy-mobile-github" href={safeRepoUrl} target="_blank" rel="noopener noreferrer" onClick={closeMenus}>GitHub <HeaderIcon name="external" /></a>}
      </nav>
      <ThemeToggle />
      {safeRepoUrl && <a className="gy-header-github" href={safeRepoUrl} target="_blank" rel="noopener noreferrer"><HeaderIcon name="github" /><span>GitHub</span></a>}
      <button ref={menu} type="button" className="gy-menu-toggle" aria-label={open ? "关闭导航" : "打开导航"} aria-expanded={open} aria-controls="main-navigation" onClick={() => setOpen(!open)}><HeaderIcon name={open ? "close" : "menu"} /></button>
    </div>
  </header>;
}
