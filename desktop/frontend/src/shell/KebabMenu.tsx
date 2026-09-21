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
 * fine (unlike Modal, which owns the whole screen). Closing always returns
 * focus to the trigger button (task 8.4's "logical tab/focus restoration"):
 * without it, closing the menu while a menu item held focus (reached via
 * Tab, or via a click/Escape after that) would unmount that focused
 * element and drop keyboard focus to the document body, losing the user's
 * place entirely.
 */
export function KebabMenu({ label, items, className }: KebabMenuProps) {
  const [open, setOpen] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);
  const buttonRef = useRef<HTMLButtonElement>(null);

  function close() {
    setOpen(false);
    buttonRef.current?.focus();
  }

  useEffect(() => {
    if (!open) return;
    function handlePointerDown(event: MouseEvent) {
      if (containerRef.current && !containerRef.current.contains(event.target as Node)) {
        // No focus() here: an outside click already moved focus (or is
        // about to) wherever the user clicked, so pulling it back to this
        // button would fight that, not restore anything.
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
      close();
    }
  }

  return (
    <div
      ref={containerRef}
      className={`kebab-menu-container${className ? ` ${className}` : ""}`}
      onKeyDown={handleKeyDown}
    >
      <button
        ref={buttonRef}
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
                  close();
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
