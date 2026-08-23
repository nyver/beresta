import { useEffect, useRef, useState, type KeyboardEvent } from "react";

export interface KebabMenuItem {
  label: string;
  onSelect: () => void;
  destructive?: boolean;
  disabled?: boolean;
}

export interface KebabMenuProps {
  /** Accessible label for the trigger button, e.g. "Notebook actions". */
  label: string;
  items: KebabMenuItem[];
  className?: string;
}

/**
 * KebabMenu is a small "⋮" dropdown used wherever a row (notebook, note,
 * attachment) needs a handful of secondary actions without permanently
 * occupying row space. Closes on an outside click or Escape; no focus trap,
 * since these menus are small enough that Tab simply leaving the menu is
 * fine (unlike Modal, which owns the whole screen).
 */
export function KebabMenu({ label, items, className }: KebabMenuProps) {
  const [open, setOpen] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    function handlePointerDown(event: MouseEvent) {
      if (containerRef.current && !containerRef.current.contains(event.target as Node)) {
        setOpen(false);
      }
    }
    document.addEventListener("mousedown", handlePointerDown);
    return () => document.removeEventListener("mousedown", handlePointerDown);
  }, [open]);

  function handleKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    if (event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      setOpen(false);
    }
  }

  return (
    <div
      ref={containerRef}
      className={`kebab-menu-container${className ? ` ${className}` : ""}`}
      onKeyDown={handleKeyDown}
    >
      <button
        type="button"
        className="kebab-button"
        aria-label={label}
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={(event) => {
          event.stopPropagation();
          setOpen((current) => !current);
        }}
      >
        ⋮
      </button>
      {open ? (
        <ul className="kebab-menu" role="menu">
          {items.map((item, index) => (
            <li key={index} role="none">
              <button
                type="button"
                role="menuitem"
                className={item.destructive ? "destructive" : undefined}
                disabled={item.disabled}
                onClick={(event) => {
                  event.stopPropagation();
                  setOpen(false);
                  item.onSelect();
                }}
              >
                {item.label}
              </button>
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}
