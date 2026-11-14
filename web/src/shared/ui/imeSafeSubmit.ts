import {
  useRef,
  type CompositionEventHandler,
  type FocusEventHandler,
  type KeyboardEvent,
  type KeyboardEventHandler
} from 'react';

const IME_PROCESSING_KEYCODE = 229;

/** Event handlers that prevent Enter key submission while an IME composition is active. */
export interface ImeSafeEnterHandlers {
  onCompositionStart: CompositionEventHandler<HTMLInputElement>;
  onCompositionEnd: CompositionEventHandler<HTMLInputElement>;
  onBlur: FocusEventHandler<HTMLInputElement>;
  onKeyDown: KeyboardEventHandler<HTMLInputElement>;
}

/** Returns whether a keyboard event occurred while IME composition was in progress. */
export function isImeComposing(
  event: KeyboardEvent<HTMLInputElement | HTMLTextAreaElement>,
  composing = false
): boolean {
  return (
    composing ||
    event.nativeEvent.isComposing ||
    event.keyCode === IME_PROCESSING_KEYCODE ||
    event.nativeEvent.keyCode === IME_PROCESSING_KEYCODE
  );
}

/** React hook returning input handlers that submit the enclosing form on non-IME Enter. */
export function useImeSafeEnter(): ImeSafeEnterHandlers {
  const composingRef = useRef(false);

  return {
    onCompositionStart: () => {
      composingRef.current = true;
    },
    onCompositionEnd: () => {
      composingRef.current = false;
    },
    onBlur: () => {
      composingRef.current = false;
    },
    onKeyDown: (event) => {
      if (event.key !== 'Enter') return;
      event.preventDefault();
      if (isImeComposing(event, composingRef.current)) return;
      event.currentTarget.form?.requestSubmit();
    }
  };
}
