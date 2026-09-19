import { useState, type InputHTMLAttributes, type KeyboardEvent } from "react";

import { useI18n } from "../i18n";

export interface PasswordFieldProps extends Omit<InputHTMLAttributes<HTMLInputElement>, "type" | "onKeyDown"> {
  /** Called when Enter is pressed with a non-empty value and no IME
   * composition in progress. Wails' WebView does not consistently treat
   * Enter in a text input as a native form submit (see Unlock.tsx's
   * original comment to this effect), so every password field needs this
   * explicit handling rather than relying on the surrounding <form>. */
  onEnter?: () => void;
}

/**
 * PasswordField wraps a password `<input>` with a show/hide visibility
 * toggle and a Caps Lock warning (task 7.1's onboarding requirements) -
 * both read from the same key events, so they live together here instead
 * of being wired up twice per screen. Kept independent of Onboarding.tsx
 * so Unlock.tsx can reuse it too without pulling in onboarding-specific
 * code.
 */
export function PasswordField({ onEnter, value, id, ...inputProps }: PasswordFieldProps) {
  const { t } = useI18n();
  const [visible, setVisible] = useState(false);
  const [capsLockOn, setCapsLockOn] = useState(false);

  function trackCapsLock(event: KeyboardEvent<HTMLInputElement>) {
    if (typeof event.getModifierState === "function") {
      setCapsLockOn(event.getModifierState("CapsLock"));
    }
  }

  function handleKeyDown(event: KeyboardEvent<HTMLInputElement>) {
    trackCapsLock(event);
    if (onEnter && event.key === "Enter" && !event.nativeEvent.isComposing && value) {
      event.preventDefault();
      onEnter();
    }
  }

  return (
    <div className="password-field">
      <div className="password-field-row">
        <input
          {...inputProps}
          id={id}
          value={value}
          type={visible ? "text" : "password"}
          onKeyDown={handleKeyDown}
          onKeyUp={trackCapsLock}
        />
        <button
          type="button"
          className="password-visibility-toggle"
          aria-label={visible ? t("common.hide_password") : t("common.show_password")}
          aria-pressed={visible}
          onClick={() => setVisible((current) => !current)}
        >
          {visible ? "🙈" : "👁"}
        </button>
      </div>
      {capsLockOn ? (
        <p className="hint caps-lock-warning" role="status">
          {t("common.caps_lock_warning")}
        </p>
      ) : null}
    </div>
  );
}
